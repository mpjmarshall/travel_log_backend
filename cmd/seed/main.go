// `make seed` — the captured client log into PostgreSQL, and the only command
// in this repository that writes data nobody asked for.
//
// IT IS A DEVELOPER COMMAND (DEC-75). Mock data on the SERVER, run deliberately
// by a developer, is not the same thing as an app that ships fake trips baked
// in: the first is a fixture and the second is the "stranger's data" objection
// the client's own record makes. NOTHING IN cmd/api IMPORTS internal/seed and
// cmd/api/imports_test.go says so.
//
// TWO GUARDS, AND NEITHER IS THE ONE THE PLAN WROTE (DEC-97).
//
//  1. IT REFUSES WHEN ANY TRAVELLER ROW EXISTS. Not "when the log is
//     non-empty" — a freshly migrated database IS empty, so a log-shaped
//     predicate does not fire on `make up && make seed`, which is the obvious
//     first thing an operator types. DEC-86 closes registration once any
//     traveller exists, so that run would hand the deployment's only account a
//     passphrase printed in a terminal, with a 600/min budget, and no way for
//     the real traveller ever to create theirs. It is the same predicate
//     register uses, so it covers the other direction too: a traveller who has
//     registered and written nothing is not an empty database.
//
//  2. IT REFUSES WITHOUT `--i-know-this-is-a-dev-database`. A DSN cannot tell
//     a development database from a production one, and PD-02's required
//     `-dsn` with no environment fallback stops an ambient DATABASE_URL from
//     aiming it — but it cannot stop a correct-looking URL pointing somewhere
//     real. The marker is the operator saying so.
//
// AND BOTH REFUSALS PRINT WHERE THEY WERE POINTED (SAF-MAJ-8), because an
// operator who has aimed at the wrong database has to learn it from the
// refusal rather than from what happens next. What is at stake is not
// recoverable by any control the client has: NOTHING IN THIS APPLICATION CAN
// DELETE A CITY (DEC-57), so a load into somebody's record leaves twelve
// cities and two media objects standing permanently, with no route, no sheet
// and no control able to reach them.
//
// THE PASSPHRASE IS GENERATED PER RUN, so a fixture cannot become a shipped
// credential.
//
// THE ORDER IS MEDIA FIRST, DATABASE SECOND (DEC-78), and the seam is stated
// rather than hidden: the two PNGs are uploaded to the bucket BEFORE the
// ten-table transaction, so a load that fails at table seven rolls the database
// back and LEAVES THE OBJECTS. That is intended — content addressing makes the
// re-upload idempotent, and a database referencing bytes nobody uploaded is the
// worse failure — but it has to be written down, because once `-sweep-media`
// has a grace window an uploaded-not-committed object is indistinguishable from
// an orphan.
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

// defaultFixture is where DEC-75's captured document lives. It is a FLAG with
// a default rather than an embed: embedding it would put 1.1 MB of PNG and an
// 85 KB log into every binary that links internal/logbook, cmd/api included.
const defaultFixture = "internal/logbook/testdata/client_sample_log.json"

// devMarker is guard 2, spelled once so the refusal and the flag cannot drift.
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

	// GUARD 2, AND IT IS CHECKED BEFORE THE FIRST CONNECTION. The refusal says
	// where it was pointed, so an operator who has aimed at the wrong database
	// learns it here rather than afterwards.
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

	// GUARD 1, EARLY. Load takes it again INSIDE the transaction, which is
	// where it is a guard; this one is so a refused run does not upload two
	// photographs to a bucket first.
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

	// THE TRAVELLER ID COMES FROM THE DATABASE, not from a uuid package. It is
	// `gen_random_uuid()` exactly as `POST /v1/auth/register` uses, and it has
	// to exist BEFORE the upload because the bucket key is
	// `<traveller>/<digest>` (DEC-38).
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

	// MEDIA FIRST (DEC-78). See the file comment for what a failure between
	// here and the commit leaves behind.
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

// === the two photographs ===

type asset struct {
	locator string // what the client's own document calls it
	path    string // where the bytes are on disk
	digest  string // the sha256 of those bytes, in hex — computed, never written
	size    int64
	kind    string
	bytes   []byte
}

// readImagery turns the directory beside the fixture into the asset table.
//
// THE DIGEST IS COMPUTED FROM THE BYTES AND IS NEVER A LITERAL, and that is
// what makes the ONE input the round trip cannot check unfalsifiable. The round
// trip applies RewriteAssets once and compares its own output against what came
// back, so a mapping that pointed one locator at the other's digest would agree
// with itself on both sides. Here it cannot: the address IS the content, so a
// wrong mapping is bytes whose sha256 is not the key they were signed for, and
// DEC-88's signed checksum refuses the PUT.
func readImagery(dir string) ([]asset, error) {
	// The client's own locators, which are a fact about a document this
	// repository did not write. `assets/imagery/` is the Flutter bundle prefix.
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
			// media_objects_content_type_ck is `IN ('image/jpeg', 'image/png')`
			// since migration 0003 (DEC-104), and both of these are PNG.
			kind:  "image/png",
			bytes: body,
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
// makes a seeded object indistinguishable from an uploaded one. `alreadyExists`
// and every cover check in R3 read that column and never row presence, so a
// seeded row with it NULL is a cover no route would accept.
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

// uploadAll puts the bytes in the bucket through internal/media's own signing,
// so the seed's upload is the same capability a phone gets.
func uploadAll(ctx context.Context, cfg media.Config, travellerID string, assets []asset) error {
	if strings.TrimSpace(cfg.InternalEndpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return errors.New("-s3-endpoint and -s3-bucket are required unless -skip-media; " +
			"`make seed` derives all four from the running container")
	}
	// The public base is the same host here: the seed runs where it can reach
	// the bucket, and it PUTs the URL it signs rather than handing it to a
	// phone. TTLs are minutes because the upload happens immediately.
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
		// DEC-88: `If-None-Match: *` is signed into the PUT, so a second upload
		// of the same address answers 412 with the ORIGINAL BYTES INTACT. THE
		// COMMIT PATH READS 412 AS SUCCESS and so does this: the object is
		// already there, at an address that is its own checksum.
		fmt.Printf("    %s is already in the bucket (412, write-once) — the bytes are its own address\n", a.digest)
		return nil
	case res.StatusCode >= 200 && res.StatusCode < 300:
		fmt.Printf("    uploaded %s (%d bytes)\n", a.digest, a.size)
		return nil
	default:
		return fmt.Errorf("uploading %s answered %d: %s", a.locator, res.StatusCode, strings.TrimSpace(string(body)))
	}
}

// === the generated credential ===

// passphraseWords is the alphabet DEC-67's share ids already use, minus the
// characters that are ambiguous in a terminal font. It is not a wordlist: the
// passphrase is read once, out of a terminal, and typed into a phone.
const passphraseAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// passphraseLength is 24 characters over a 31-character alphabet, which is
// about 118 bits. auth.MinPassphraseBytes is 8, and this is not a number
// chosen against that floor: it is chosen so that a credential printed in a
// terminal and never rotated is not the weak link.
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

// === what a completed run says ===

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
//
// DEC-97 asks for the DSN in the refusal so an operator who has pointed at the
// wrong database learns it there. The host, the port, the database name and the
// user are the whole of that; the password adds nothing to the identification
// and does go into terminal scrollback. It is a narrowing of the ruling and it
// is stated rather than silent.
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
