package media

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is what MinIO needs, and every field of it comes from internal/config
// — this package reads no environment of its own (spec L30).
type Config struct {
	// InternalEndpoint is what the API DIALS, and PublicBaseURL is what a
	// signature COVERS (DEC-42). They differ in exactly one deployment shape
	// and it is the one this repository runs in: inside compose the API
	// reaches the bucket at http://minio:9000 and a phone cannot.
	//
	// A SIGNATURE COVERS THE HOST, so an address baked into signing code is
	// not a value anything downstream can change: a proxy cannot rewrite it, a
	// Host header cannot correct it, a CNAME cannot rescue it. The signature
	// simply fails.
	InternalEndpoint string
	PublicBaseURL    string

	Region    string
	Bucket    string
	AccessKey string
	SecretKey string

	TTLPrivate time.Duration
	TTLPublic  time.Duration
}

// MinIO is the one implementation that talks to a bucket.
type MinIO struct {
	api    *minio.Client
	signer *minio.Client
	bucket string
	ttl    map[Audience]time.Duration
}

var _ Store = (*MinIO)(nil)

// New builds the two clients this package needs.
//
// TWO CLIENTS AND NOT ONE, because DEC-42's two addresses are two hosts and a
// SigV4 signature covers the host: `api` dials the internal endpoint for the
// calls the server itself makes, and `signer` mints URLs against the public
// base so the thing a phone connects to is the thing that was signed.
//
// THE REGION IS SET ON BOTH, AND THAT IS WHAT MAKES PRESIGNING OFFLINE. With
// `Region` empty, minio-go resolves the bucket's region with a REAL NETWORK
// ROUND TRIP on the first presign per bucket and caches it
// (bucket-cache.go: "Region set then no need to fetch bucket location"). Two
// lenses measured opposite things from that one branch — one got
// "The specified bucket does not exist" AT PRESIGN TIME, the other got a
// perfectly-formed URL that failed on the phone — and both were right, on a
// cold and a warm cache. Setting the region removes the branch: presigning is
// a local HMAC from the first request, which is what R3's mint route needs
// when it signs twelve URLs for one grid.
func New(cfg Config) (*MinIO, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("media: no bucket name")
	}
	if cfg.TTLPrivate <= 0 || cfg.TTLPublic <= 0 {
		return nil, fmt.Errorf("media: both presign lifetimes must be positive, "+
			"got private=%s public=%s", cfg.TTLPrivate, cfg.TTLPublic)
	}

	api, err := client(cfg.InternalEndpoint, cfg)
	if err != nil {
		return nil, fmt.Errorf("media: the internal endpoint: %w", err)
	}
	signer, err := client(cfg.PublicBaseURL, cfg)
	if err != nil {
		return nil, fmt.Errorf("media: the public base URL: %w", err)
	}

	return &MinIO{
		api:    api,
		signer: signer,
		bucket: cfg.Bucket,
		ttl: map[Audience]time.Duration{
			Private: cfg.TTLPrivate,
			Public:  cfg.TTLPublic,
		},
	}, nil
}

// client parses one of the two addresses into what minio.New wants: a host,
// and whether to speak TLS. The scheme is REQUIRED rather than defaulted,
// because "minio:9000" and "https://minio:9000" differ by a transport and the
// wrong guess is a signature that fails against a server that is right there.
func client(address string, cfg Config) (*minio.Client, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return nil, fmt.Errorf("parsing %q: %w", address, err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%q must be an http:// or https:// address with a host", address)
	}
	return minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: u.Scheme == "https",
		Region: cfg.Region,
	})
}

// EnsureBucket is DEC-98, and it runs AT BOOT.
//
// NOTHING ELSE CREATES THE BUCKET. Measured against the official image at a
// pinned tag with a fresh volume and no init: /minio/health/live 200,
// /minio/health/ready 200, `ls /data` -> `.minio.sys` only, ZERO buckets.
// `MINIO_DEFAULT_BUCKETS` is a Bitnami variable and not a MinIO one. So three
// healthy services is not a bucket, and because a presigned URL is offline
// arithmetic once the region is pinned, the failure would surface on the
// PHONE as NoSuchBucket rather than anywhere an operator is looking.
//
// It is the same argument run() already makes for its Postgres ping: a failure
// here is a real misconfiguration and the process should not come up
// pretending otherwise. It is also the only call in this package that proves
// the CREDENTIALS are right — presigning cannot, since a wrong secret signs
// just as happily as a right one.
func (m *MinIO) EnsureBucket(ctx context.Context) error {
	exists, err := m.api.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("media: asking whether the bucket %q exists: %w", m.bucket, err)
	}
	if exists {
		return nil
	}
	if err := m.api.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
		// Two processes booting together is a normal thing, not a fault: the
		// loser is told the bucket is already there and that is the outcome it
		// wanted. Anything else is a real failure.
		if minio.ToErrorResponse(err).Code == "BucketAlreadyOwnedByYou" {
			return nil
		}
		return fmt.Errorf("media: creating the bucket %q: %w", m.bucket, err)
	}
	return nil
}

// PresignPut mints the upload capability.
//
// THE BAN, AND IT IS THE SHARPEST THING IN THIS FILE. minio-go offers three
// ways to presign a PUT and TWO OF THEM FAIL OPEN: both call presignURL with a
// nil extra-header set — confirmed in api-presigned.go, not assumed — so
// `X-Amz-SignedHeaders=host` is the whole of what they cover. Measured against
// real MinIO through one of them: a PUT of entirely different bytes to a key
// naming sha256(X) answers **200** and the object is stored at the poisoned
// address. Every later reader trusts that address. `PresignHeader` is the only
// one of the three that signs extra headers, and it is why this function
// exists at all rather than being one line.
//
// THE TWO BANNED NAMES ARE NOT WRITTEN ANYWHERE IN THIS DIRECTORY, and that is
// deliberate rather than coy: the step's acceptance check greps
// `internal/media/` for them, and a grep that matches its own explanation
// cannot fail for the reason it was written. ban_test.go holds them, spelled
// in halves, with an AST walk that puts the ban inside `make check` instead of
// leaving it in a document.
//
// WHAT THIS SIGNATURE COVERS, IN THE SPELLING THAT GOES ON THE WIRE, because
// this is the signing site and a reader here should not have to go looking:
//
//	x-amz-checksum-sha256   the digest             (DEC-38)
//	content-length          the EXACT size         (DEC-51)
//	content-type            the validated type     (DEC-87)
//	if-none-match: *        write-once at the key  (DEC-88)
//
// plus `host`, which SigV4 always covers. The values come from uploadHeaders
// in keys.go — ONE construction, so the map the client replays and the map
// the signature covers cannot drift, and a leg asserts the key set equals
// this URL's own X-Amz-SignedHeaders minus host.
//
// THE LIFETIME IS THE PRIVATE ONE AND THERE IS NO AUDIENCE PARAMETER. An
// upload capability belongs to the authenticated traveller who asked for it;
// nothing public ever writes. X-Amz-Expires bounds when the request must
// ARRIVE rather than when it must finish, so two minutes is a window to start
// a 25 MiB upload in and not a budget to complete one.
func (m *MinIO) PresignPut(ctx context.Context, key Key, up Upload) (string, map[string]string, error) {
	if err := checkUpload(key, up); err != nil {
		return "", nil, err
	}
	path, checksum, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", nil, err
	}

	headers := uploadHeaders(up, checksum)

	// http.Header canonicalises on Set, and minio-go copies the map's keys
	// into the request verbatim before signing — so the SignedHeaders list is
	// lowercased by the signer either way and the map above stays the single
	// source of both the signature and what the client replays.
	extra := http.Header{}
	for name, value := range headers {
		extra.Set(name, value)
	}

	u, err := m.signer.PresignHeader(ctx, http.MethodPut, m.bucket, path, m.ttl[Private], nil, extra)
	if err != nil {
		return "", nil, fmt.Errorf("media: signing an upload for %s: %w", path, err)
	}
	return u.String(), headers, nil
}

// SignedHeaders reads the header names a presigned URL's signature covers back
// off the URL itself, lowercase and sorted, `host` included.
//
// IT EXISTS FOR ONE LEG (DEC-88): the uploadHeaders map's key set must equal
// this minus `host`. Without that leg the map and the signature drift the
// first time a header is added to one and not the other, and every upload
// 400s with AccessDenied for ever — a failure no unit test can see, because
// both halves look right in isolation.
func SignedHeaders(presigned string) ([]string, error) {
	u, err := url.Parse(presigned)
	if err != nil {
		return nil, fmt.Errorf("media: parsing the presigned URL: %w", err)
	}
	raw := u.Query().Get("X-Amz-SignedHeaders")
	if raw == "" {
		return nil, errors.New("media: the URL carries no X-Amz-SignedHeaders, " +
			"which is what a URL signed by one of the banned calls looks like")
	}
	return strings.Split(raw, ";"), nil
}

// PresignGet mints a read capability for one audience.
func (m *MinIO) PresignGet(ctx context.Context, key Key, aud Audience) (string, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", err
	}
	ttl, ok := m.ttl[aud]
	if !ok {
		return "", fmt.Errorf("media: %d is not an audience", aud)
	}

	// `response-content-disposition: attachment` is DEC-51's residual made
	// real: a mislabelled object is DOWNLOADED rather than rendered on the
	// storage origin. It is inside the signature, so the URL's holder cannot
	// strip it — measured: deleting the query parameter answers 403
	// SignatureDoesNotMatch.
	//
	// AND ONE THING THAT MUST NOT BE ASSERTED, recorded here so it is not
	// re-litigated by somebody who curls MinIO and sees a header: MinIO sets
	// `X-Content-Type-Options: nosniff` on presigned GETs of its own accord,
	// and real S3 CANNOT — its response-header override set is closed
	// (content-type, -language, -expires, -cache-control, -disposition,
	// -encoding) and nosniff is not in it. A leg asserting nosniff would pass
	// locally by measuring MinIO's default and would silently stop being true
	// in production. That is DEC-43's "MinIO is a more forgiving verifier"
	// hazard running in the OPPOSITE direction.
	params := url.Values{}
	params.Set("response-content-disposition", "attachment")

	u, err := m.signer.PresignedGetObject(ctx, m.bucket, path, ttl, params)
	if err != nil {
		return "", fmt.Errorf("media: signing a %s read for %s: %w", aud, path, err)
	}
	return u.String(), nil
}

// Stat answers what the bucket holds, or ErrNoSuchObject.
func (m *MinIO) Stat(ctx context.Context, key Key) (Attributes, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return Attributes{}, err
	}

	// `Checksum: true` sends x-amz-checksum-mode: ENABLED. WITHOUT IT THE
	// FIELD COMES BACK EMPTY AND A COMMIT-TIME CHECK PASSES NOTHING — which is
	// the trap worth naming, because the wrong version of this call looks
	// identical and answers a struct with one silent zero in it.
	info, err := m.api.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{Checksum: true})
	if err != nil {
		if code := minio.ToErrorResponse(err).Code; code == "NoSuchKey" || code == "NoSuchBucket" {
			return Attributes{}, fmt.Errorf("%w: %s: %s", ErrNoSuchObject, path, code)
		}
		return Attributes{}, fmt.Errorf("media: stat %s: %w", path, err)
	}

	return Attributes{
		Size:        info.Size,
		ContentType: info.ContentType,
		SHA256:      hexOfBase64(info.ChecksumSHA256),
	}, nil
}

// hexOfBase64 turns the bucket's base64 checksum back into the hex the rest of
// this system speaks, and answers "" for an object that carries none — which
// is what an upload through the banned call leaves behind.
func hexOfBase64(b64 string) string {
	if b64 == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

// ExpiresIn reads `X-Amz-Expires` back off a presigned URL, as a duration.
//
// IT EXISTS SO THE BEGIN RESPONSE'S `expiresAt` HAS ONE SOURCE. A handler
// computing it from a configured lifetime would be two variables holding one
// fact — the mistake `Key.Object` and `Upload.SHA256` are deliberately allowed
// to express so that a leg can redden it — and the failure here is silent: the
// client is told a window that is not the window the SIGNATURE carries, and
// the upload dies with SignatureDoesNotMatch some minutes later with nothing
// on either side saying why.
//
// It is also what DEC-84's audience leg asserts against, in both
// implementations, which is why Memory's fake URLs carry the parameter too.
func ExpiresIn(presigned string) (time.Duration, error) {
	u, err := url.Parse(presigned)
	if err != nil {
		return 0, fmt.Errorf("media: parsing the presigned URL: %w", err)
	}
	raw := u.Query().Get("X-Amz-Expires")
	if raw == "" {
		return 0, errors.New("media: the URL carries no X-Amz-Expires, so there is " +
			"nothing to tell the client about how long it has")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("media: X-Amz-Expires is %q, which is not a positive "+
			"number of seconds", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}
