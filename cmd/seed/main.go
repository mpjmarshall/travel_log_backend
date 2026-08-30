// `make seed` — the captured client log into PostgreSQL, and the only command
// in this repository that writes data nobody asked for.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"travellog/internal/auth"
	"travellog/internal/logbook"
	"travellog/internal/media"
	"travellog/internal/seed"
)

// defaultFixture is where the captured document lives.
const defaultFixture = "internal/logbook/testdata/client_sample_log.json"

// devMarker is guard 2, spelled once so the refusal and the flag cannot
// drift.
const devMarker = "i-know-this-is-a-dev-database"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nmake seed: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	dsn       string
	dev       bool
	fixture   string
	email     string
	s3        media.Config
	timeout   time.Duration
	skipMedia bool
}

func run() error {
	var o options
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.StringVar(&o.dsn, "dsn", "", "PostgreSQL URL to load into (REQUIRED; there is no environment fallback)")
	fs.BoolVar(&o.dev, devMarker, false, "assert that -dsn points at a development database")
	fs.StringVar(&o.fixture, "fixture", defaultFixture, "the captured client log; its imagery/ sits beside it")
	fs.StringVar(&o.email, "email", "seed@travellog.test", "the address the seeded traveller signs in with")
	fs.StringVar(&o.s3.InternalEndpoint, "s3-endpoint", "", "the bucket's URL, scheme and all (REQUIRED unless -skip-media)")
	fs.StringVar(&o.s3.Bucket, "s3-bucket", "", "the bucket the photographs go in")
	fs.StringVar(&o.s3.AccessKey, "s3-access-key", "", "")
	fs.StringVar(&o.s3.SecretKey, "s3-secret-key", "", "")
	fs.StringVar(&o.s3.Region, "s3-region", "us-east-1", "")
	fs.BoolVar(&o.skipMedia, "skip-media", false,
		"write the media_objects rows without uploading the bytes — the log then "+
			"references two objects the bucket does not hold, and every mint 404s")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if strings.TrimSpace(o.dsn) == "" {
		fs.Usage()
		return errors.New("-dsn is required and has no environment fallback (PD-02): " +
			"an ambient DATABASE_URL must not be able to aim this at a real database")
	}

	if !o.dev {
		return fmt.Errorf("refusing to load a captured logbook into %s\n"+
			"    without --%s.\n\n"+
			"    A DSN cannot tell a development database from a production one, and\n"+
			"    this command writes ten tables of somebody else's geography. Nothing in\n"+
			"    this application can delete a city (DEC-57), so a load into a real\n"+
			"    record leaves twelve of them standing with no control able to reach them.",
			redactDSN(o.dsn), devMarker)
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()

	db, err := sql.Open("pgx", o.dsn)
	if err != nil {
		return fmt.Errorf("opening %s: %w", redactDSN(o.dsn), err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to %s: %w", redactDSN(o.dsn), err)
	}

	if existing, err := seed.ExistingTraveller(ctx, db); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("%w\n\n    the database: %s\n    the traveller: %s <%s>",
			existing, redactDSN(o.dsn), existing.TravellerID, existing.Email)
	}

	raw, err := os.ReadFile(o.fixture)
	if err != nil {
		return fmt.Errorf("reading the captured log: %w", err)
	}
	envelope, err := logbook.DecodeEnvelope(raw)
	if err != nil {
		return fmt.Errorf("the captured log at %s: %w", o.fixture, err)
	}

	var travellerID string
	if err := db.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&travellerID); err != nil {
		return fmt.Errorf("asking the database for a traveller id: %w", err)
	}

	assets, err := readImagery(filepath.Join(filepath.Dir(o.fixture), "imagery"))
	if err != nil {
		return err
	}
	fmt.Printf("  %d asset%s, addressed by the sha256 of their own bytes:\n", len(assets), plural(len(assets)))
	for _, a := range assets {
		fmt.Printf("    %s  %s  %d bytes\n", a.digest, a.locator, a.size)
	}

	if o.skipMedia {
		fmt.Printf("  -skip-media: the bytes are NOT uploaded, so every mint for these two\n" +
			"    objects will 404 until something puts them in the bucket.\n")
	} else if err := uploadAll(ctx, o.s3, travellerID, assets); err != nil {
		return err
	}

	document, err := logbook.RewriteAssets(envelope.Logbook, mappingOf(assets))
	if err != nil {
		return err
	}

	passphrase, err := newPassphrase()
	if err != nil {
		return err
	}
	hash, err := auth.Argon2id{Params: auth.DefaultParams}.Hash(passphrase)
	if err != nil {
		return fmt.Errorf("hashing the generated passphrase: %w", err)
	}

	now := time.Now().UTC()
	traveller := seed.Traveller{
		ID: travellerID, Email: o.email, PassphraseHash: hash, CreatedAt: now,
	}
	dataset, err := seed.FromDocument(traveller, objectsOf(travellerID, assets, now), document)
	if err != nil {
		return err
	}

	report, err := seed.Load(ctx, db, dataset, seed.LoadOptions{})
	if err != nil {
		var existing *seed.TravellerExistsError
		if errors.As(err, &existing) {
			return fmt.Errorf("%w\n\n    the database: %s\n    the traveller: %s <%s>",
				existing, redactDSN(o.dsn), existing.TravellerID, existing.Email)
		}
		return err
	}

	printReport(o, travellerID, passphrase, report)
	return nil
}

type asset struct {
	locator string // what the client's own document calls it
	path    string // where the bytes are on disk
	digest  string // the sha256 of those bytes, in hex — computed, never written
	size    int64
	kind    string
	bytes   []byte
}

// readImagery turns the directory beside the fixture into the asset table.
func readImagery(dir string) ([]asset, error) {
	names := []string{"card-ireland.png", "hero-mountain.png"}

	out := make([]asset, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading the imagery the captured log addresses: %w", err)
		}
		sum := sha256.Sum256(body)
		out = append(out, asset{
			locator: "assets/imagery/" + name,
			path:    path,
			digest:  hex.EncodeToString(sum[:]),
			size:    int64(len(body)),
			kind:    "image/png",
			bytes:   body,
		})
	}
	return out, nil
}

func mappingOf(assets []asset) map[string]string {
	out := make(map[string]string, len(assets))
	for _, a := range assets {
		out[a.locator] = a.digest
	}
	return out
}

// objectsOf is the media_objects half, with `uploaded_at` SET — which is what
// makes a seeded object indistinguishable from an uploaded one.
func objectsOf(travellerID string, assets []asset, at time.Time) []seed.MediaObject {
	out := make([]seed.MediaObject, 0, len(assets))
	for _, a := range assets {
		uploaded := at
		out = append(out, seed.MediaObject{
			TravellerID: travellerID, ID: a.digest, ByteSize: a.size,
			ContentType: a.kind, CreatedAt: at, UploadedAt: &uploaded,
		})
	}
	return out
}

// uploadAll puts the bytes in the bucket through internal/media's own
// signing, so the seed's upload is the same capability a phone gets.
func uploadAll(ctx context.Context, cfg media.Config, travellerID string, assets []asset) error {
	if strings.TrimSpace(cfg.InternalEndpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return errors.New("-s3-endpoint and -s3-bucket are required unless -skip-media; " +
			"`make seed` derives all four from the running container")
	}
	cfg.PublicBaseURL = cfg.InternalEndpoint
	cfg.TTLPrivate, cfg.TTLPublic = 5*time.Minute, 5*time.Minute

	store, err := media.New(cfg)
	if err != nil {
		return fmt.Errorf("the bucket: %w", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("the bucket %q: %w", cfg.Bucket, err)
	}

	for _, a := range assets {
		if err := upload(ctx, store, travellerID, a); err != nil {
			return err
		}
	}
	return nil
}

func upload(ctx context.Context, store media.Store, travellerID string, a asset) error {
	key := media.Key{Traveller: travellerID, Object: a.digest}
	signed, headers, err := store.PresignPut(ctx, key,
		media.Upload{SHA256: a.digest, ByteSize: a.size, ContentType: a.kind})
	if err != nil {
		return fmt.Errorf("signing the upload for %s: %w", a.locator, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed, strings.NewReader(string(a.bytes)))
	if err != nil {
		return fmt.Errorf("building the upload for %s: %w", a.locator, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.ContentLength = a.size

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading %s: %w", a.locator, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))

	switch {
	case res.StatusCode == http.StatusPreconditionFailed:
		fmt.Printf("    %s is already in the bucket (412, write-once) — the bytes are its own address\n", a.digest)
		return nil
	case res.StatusCode >= 200 && res.StatusCode < 300:
		fmt.Printf("    uploaded %s (%d bytes)\n", a.digest, a.size)
		return nil
	default:
		return fmt.Errorf("uploading %s answered %d: %s", a.locator, res.StatusCode, strings.TrimSpace(string(body)))
	}
}

// passphraseWords is the alphabet the share ids already use, minus the
// characters that are ambiguous in a terminal font.
const passphraseAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// passphraseLength is 24 characters over a 31-character alphabet, which is
// about 118 bits.
const passphraseLength = 24

func newPassphrase() (string, error) {
	raw := make([]byte, passphraseLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("drawing a passphrase: %w", err)
	}
	out := make([]byte, passphraseLength)
	for i, b := range raw {
		out[i] = passphraseAlphabet[int(b)%len(passphraseAlphabet)]
	}
	return string(out), nil
}

func printReport(o options, travellerID, passphrase string, report seed.Report) {
	fmt.Printf("\n  loaded in %s\n", report.Duration.Round(time.Millisecond))
	for _, table := range []string{
		"travellers", "media_objects", "cities", "trips", "trip_cities",
		"places", "visits", "photos", "walks", "share_links",
	} {
		fmt.Printf("    %-14s %6d\n", table, report.Rows[table])
	}
	fmt.Printf("\n  the database:  %s\n", redactDSN(o.dsn))
	fmt.Printf("  the traveller: %s\n", travellerID)
	fmt.Printf("  sign in with:  %s / %s\n", o.email, passphrase)
	fmt.Printf("\n  THE PASSPHRASE IS GENERATED PER RUN AND IS PRINTED ONLY HERE (DEC-97).\n" +
		"  Nothing stores it: the column holds an argon2id hash. If this scrolls\n" +
		"  away, the way back is a database with no traveller in it.\n" +
		"  Registration is CLOSED behind this account (DEC-86).\n")
}

// redactDSN keeps everything that identifies WHICH database and drops the one
// thing that identifies nothing.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "<a DSN this command could not parse>"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "…")
		}
	}
	return u.Redacted()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
