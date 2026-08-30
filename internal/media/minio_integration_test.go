//go:build integration

// The integration tier: internal/media against a real MinIO.
package media_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"travellog/internal/media"
)

const (
	endpointVar = "TEST_S3_ENDPOINT"

	travellerID = "0f9c1f36-2e5d-4a1c-9a3f-6d2b1c7e4a80"

	ttlPrivate = 2 * time.Minute
	ttlPublic  = 15 * time.Minute
)

// tb is the slice of *testing.T this file's skip path needs, as an interface
// The skip itself can be exercised.
type tb interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// realMinIO answers a store pointed at the server TEST_S3_ENDPOINT names, in
// a bucket of this run's own.
func realMinIO(t tb, bucket string) *media.MinIO {
	t.Helper()

	endpoint := strings.TrimSpace(os.Getenv(endpointVar))
	if endpoint == "" {
		t.Skipf("%s is not set, so there is no bucket to run against.\n"+
			"    Bring one up and export it:  make test-s3", endpointVar)
		return nil
	}

	store, err := media.New(media.Config{
		InternalEndpoint: endpoint,
		PublicBaseURL:    endpoint,
		Region:           "us-east-1",
		Bucket:           bucket,
		AccessKey:        envOr("TEST_S3_ACCESS_KEY", "travellog"),
		SecretKey:        envOr("TEST_S3_SECRET_KEY", "travellogsecret"),
		TTLPrivate:       ttlPrivate,
		TTLPublic:        ttlPublic,
	})
	if err != nil {
		t.Fatalf("media.New: %v", err)
		return nil
	}
	return store
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// freshBucket is a bucket per run, per test function.
func freshBucket(t *testing.T) *media.MinIO {
	t.Helper()

	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		t.Fatalf("naming a bucket: %v", err)
	}
	name := fmt.Sprintf("r2-%s-%s", hex.EncodeToString(salt[:]),
		strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-")))
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}

	store := realMinIO(t, name)
	if err := store.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return store
}

// s3Error is the xml body every S3-compatible server answers a refusal with.
type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// put replays exactly the headers PresignPut handed back and answers the S3
// error code and the status.
func put(t *testing.T, url string, headers map[string]string, body []byte) (code string, status int) {
	t.Helper()
	return do(t, url, headers, body, false)
}

// putChunked sends the same request with no Content-Length, which is the
// obvious way around a signed one.
func putChunked(t *testing.T, url string, headers map[string]string, body []byte) (code string, status int) {
	t.Helper()
	return do(t, url, headers, body, true)
}

func do(t *testing.T, url string, headers map[string]string, body []byte, chunked bool) (string, int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the PUT: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if chunked {
		req.ContentLength = -1
		req.Header.Del("content-length")
	} else {
		req.ContentLength = int64(len(body))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 300 {
		return "", resp.StatusCode
	}
	var parsed s3Error
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return fmt.Sprintf("<unparseable body: %q>", string(raw)), resp.StatusCode
	}
	return parsed.Code, resp.StatusCode
}

// The upload must be refused by the bucket, not by us.
func TestABodyThatDoesNotMatchThePresignedDigestIsRefusedByTheBucket(t *testing.T) {
	store := freshBucket(t)

	honest := []byte("the bytes the client promised")
	sum := sha256.Sum256(honest)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(honest)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	lying := bytes.Repeat([]byte("x"), len(honest))
	if len(lying) != len(honest) {
		t.Fatalf("the lie is %d bytes against %d honest ones, so the signed "+
			"length refuses it before the digest is looked at", len(lying), len(honest))
	}
	if bytes.Equal(lying, honest) {
		t.Fatal("the lie is the honest bytes")
	}

	code, status := put(t, url, headers, lying)
	if code != "XAmzContentChecksumMismatch" {
		t.Fatalf("the bucket answered %d %s for bytes that are not the digest the URL was "+
			"signed for; want XAmzContentChecksumMismatch. AccessDenied here means the "+
			"leg did not replay a signed header and is measuring nothing; "+
			"InvalidArgument means the digest was signed as hex rather than base64",
			status, code)
	}

	if _, err := store.Stat(t.Context(), key); err == nil {
		t.Fatal("the object exists after a refused upload")
	}
}

// The attacker does not replay anything — which is the leg that actually
// catches the ban, and it is here because the leg above turned out not to.
func TestAnUploadThatOmitsTheDigestIsRefused(t *testing.T) {
	store := freshBucket(t)

	honest := []byte("the bytes the client promised")
	sum := sha256.Sum256(honest)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(honest)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	stripped := map[string]string{}
	for k, v := range headers {
		if k == "x-amz-checksum-sha256" {
			continue
		}
		stripped[k] = v
	}
	if len(stripped) != len(headers)-1 {
		t.Fatal("the digest header is not in the map, so this leg strips nothing")
	}

	lying := bytes.Repeat([]byte("x"), len(honest))
	code, status := put(t, url, stripped, lying)
	if code != "AccessDenied" {
		t.Fatalf("a PUT that simply omitted the signed digest answered %d %s, want "+
			"AccessDenied. A 200 here means the URL signs `host` and nothing else "+
			"— the fail-open shape ban_test.go exists for — and the object below "+
			"is arbitrary bytes at an address claiming to be their hash",
			status, code)
	}
	if _, err := store.Stat(t.Context(), key); err == nil {
		t.Fatal("the object exists after an upload that carried no digest at all")
	}
}

// asked for content-length signed into the PUT "so the bucket enforces it
// Than the API hoping".
func TestABodyLongerThanTheSignedLengthIsRefusedByTheBucket(t *testing.T) {
	store := freshBucket(t)

	honest := []byte("the bytes the client promised")
	sum := sha256.Sum256(honest)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(honest)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	if code, status := put(t, url, headers, honest); status != 200 {
		t.Fatalf("the honest bytes were refused: %d %s — the leg is measuring the "+
			"header replay rather than the length, and every assertion below is "+
			"then vacuous", status, code)
	}
	if st, err := store.Stat(t.Context(), key); err != nil || st.Size != int64(len(honest)) {
		t.Fatalf("Stat after the honest PUT: %v / %d", err, st.Size)
	}

	other := []byte("a different promise entirely")
	otherSum := sha256.Sum256(other)
	otherDigest := hex.EncodeToString(otherSum[:])
	otherKey := media.Key{Traveller: travellerID, Object: otherDigest}
	url2, headers2, err := store.PresignPut(t.Context(), otherKey, media.Upload{
		SHA256: otherDigest, ByteSize: int64(len(other)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	oversized := bytes.Repeat([]byte("x"), len(other)*1000)
	code, status := put(t, url2, headers2, oversized) // EVERY signed header replayed
	if code != "SignatureDoesNotMatch" {
		t.Fatalf("the bucket answered %d %s to %d bytes against a URL signed for %d; "+
			"want SignatureDoesNotMatch. AccessDenied here means a signed header was "+
			"not replayed and the leg is measuring nothing about length. "+
			"XAmzContentChecksumMismatch means the CHECKSUM refused it and the length "+
			"is unsigned — which is the mutation this leg exists to catch, because one "+
			"legitimately-minted upload URL would then be an unbounded write that "+
			"nothing in these eight steps reclaims",
			status, code, len(oversized), len(other))
	}
	if _, err := store.Stat(t.Context(), otherKey); err == nil {
		t.Fatal("the object exists after a refused upload — a refused PUT that still " +
			"wrote something is the same defect wearing a 4xx")
	}

	if code, status := putChunked(t, url2, headers2, oversized); code != "MissingContentLength" {
		t.Errorf("chunked upload answered %d %s, want 411 MissingContentLength — an "+
			"unsigned length is a signed length that was not sent", status, code)
	}
}

// The map and the signature cannot drift.
func TestTheHeaderMapIsExactlyTheSignedHeadersMinusHost(t *testing.T) {
	store := freshBucket(t)

	body := []byte("whatever the client promised")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	url, headers, err := store.PresignPut(t.Context(),
		media.Key{Traveller: travellerID, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png"})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	signed, err := media.SignedHeaders(url)
	if err != nil {
		t.Fatalf("SignedHeaders: %v", err)
	}

	var want []string
	sawHost := false
	for _, name := range signed {
		if name == "host" {
			sawHost = true
			continue
		}
		want = append(want, name)
	}
	if !sawHost {
		t.Fatal("the URL's X-Amz-SignedHeaders does not include host, which " +
			"every SigV4 signature covers — this leg is reading the wrong thing")
	}

	var got []string
	for name := range headers {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("uploadHeaders has %v and the signature covers %v (minus host).\n"+
			"    A client replays this map verbatim; a key on one side only is a "+
			"400 AccessDenied on every upload, for ever.", got, want)
	}

	if strings.Join(want, ";") != "content-length;content-type;if-none-match;x-amz-checksum-sha256" {
		t.Errorf("the signature covers %v; the four are the digest (DEC-38), the "+
			"length (DEC-51), the type (DEC-87) and the write-once (DEC-88)", want)
	}
}

// A content address is write-once at the bucket.
func TestAContentAddressIsWriteOnce(t *testing.T) {
	store := freshBucket(t)

	honest := []byte("the bytes the client promised")
	sum := sha256.Sum256(honest)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(honest)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	if code, status := put(t, url, headers, honest); status != 200 {
		t.Fatalf("the first PUT was refused: %d %s", status, code)
	}
	if code, status := put(t, url, headers, honest); code != "PreconditionFailed" {
		t.Fatalf("the SAME bytes again answered %d %s, want 412 PreconditionFailed. "+
			"The client's retry story depends on this: a re-upload after an "+
			"unacknowledged success means 'already there', not 'failed'", status, code)
	}

	lies := []byte("entirely different bytes, honestly checksummed")
	lieSum := sha256.Sum256(lies)
	lieDigest := hex.EncodeToString(lieSum[:])
	_, lieChecksum, err := media.Address(travellerID, lieDigest)
	if err != nil {
		t.Fatalf("Address: %v", err)
	}
	attack, attackHeaders, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(lies)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	_ = lieChecksum
	if code, status := put(t, attack, attackHeaders, lies); status < 400 {
		t.Fatalf("bytes whose digest is not the address answered %d %s", status, code)
	}

	st, err := store.Stat(t.Context(), key)
	if err != nil {
		t.Fatalf("Stat after the attack: %v", err)
	}
	if st.Size != int64(len(honest)) {
		t.Fatalf("the object is %d bytes and the honest ones are %d — the address "+
			"no longer holds its own contents", st.Size, len(honest))
	}
	if st.SHA256 != digest {
		t.Fatalf("the bucket stored digest %q against an address naming %q", st.SHA256, digest)
	}
}

// The type is inside the signature, so the allowlist reaches the object and
// not only the database row.
func TestTheContentTypeIsSignedAndTheBucketKeepsIt(t *testing.T) {
	store := freshBucket(t)

	body := []byte("<html>not a photograph at all</html>")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	lying := map[string]string{}
	for k, v := range headers {
		lying[k] = v
	}
	lying["content-type"] = "text/html"
	if code, status := put(t, url, lying, body); code != "SignatureDoesNotMatch" {
		t.Fatalf("a PUT declaring text/html against a URL signed for image/png "+
			"answered %d %s, want SignatureDoesNotMatch", status, code)
	}

	if code, status := put(t, url, headers, body); status != 200 {
		t.Fatalf("the honest type was refused: %d %s", status, code)
	}
	st, err := store.Stat(t.Context(), key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.ContentType != "image/png" {
		t.Fatalf("the bucket stored content-type %q; the whole point of signing it "+
			"is that the bucket keeps the validated value", st.ContentType)
	}
}

// The round trip, end to end: mint, upload, stat, mint a read, fetch,
// compare.
func TestTheRoundTrip(t *testing.T) {
	store := freshBucket(t)

	body := []byte("a photograph, for the purposes of this leg")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if code, status := put(t, url, headers, body); status != 200 {
		t.Fatalf("PUT: %d %s", status, code)
	}

	st, err := store.Stat(t.Context(), key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size != int64(len(body)) {
		t.Errorf("Stat size %d, want %d", st.Size, len(body))
	}
	if st.SHA256 != digest {
		t.Errorf("the bucket's stored digest is %q, want %q", st.SHA256, digest)
	}

	read, err := store.PresignGet(t.Context(), key, media.Public)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	status, got, header := fetch(t, read)
	if status != 200 {
		t.Fatalf("GET: %d %s", status, string(got))
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("the bytes came back different: %d against %d", len(got), len(body))
	}
	if cd := header.Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("Content-Disposition is %q, want attachment", cd)
	}
}

// Lifetime each audience gets, read off the URL the signer produced
// Than compared to the config it came from.
func TestEachAudienceGetsItsOwnLifetime(t *testing.T) {
	store := freshBucket(t)

	body := []byte("a photograph, for the purposes of this leg")
	sum := sha256.Sum256(body)
	key := media.Key{Traveller: travellerID, Object: hex.EncodeToString(sum[:])}

	for _, c := range []struct {
		aud  media.Audience
		want string
	}{
		{media.Private, "120"}, // DEC-44, the revocation knob
		{media.Public, "900"},  // DEC-84, fifteen minutes
	} {
		url, err := store.PresignGet(t.Context(), key, c.aud)
		if err != nil {
			t.Fatalf("PresignGet(%s): %v", c.aud, err)
		}
		if got := expires(t, url); got != c.want {
			t.Errorf("the %s audience got X-Amz-Expires=%s, want %s — the public "+
				"envelope has nothing to re-mint its URLs with, and the private "+
				"lifetime is the revocation knob; swapping them is either a share "+
				"page that dies mid-scroll or a revocation window seven times "+
				"longer than the copy promises", c.aud, got, c.want)
		}
	}
}

// The upload url's own window is the private one, and ExpiresIn is what the
// begin response reads it with.
func TestTheUploadURLCarriesThePrivateWindowAndExpiresInReadsIt(t *testing.T) {
	store := freshBucket(t)

	body := []byte("a photograph, for the purposes of this leg")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	url, _, err := store.PresignPut(t.Context(),
		media.Key{Traveller: travellerID, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png"})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	got, err := media.ExpiresIn(url)
	if err != nil {
		t.Fatalf("ExpiresIn: %v", err)
	}
	if got != ttlPrivate {
		t.Errorf("the upload URL is signed for %s, want the PRIVATE lifetime %s — "+
			"the public one is what GET /l/{token} embeds and it is seven and a "+
			"half times longer", got, ttlPrivate)
	}
	if want := expires(t, url); want != "120" {
		t.Errorf("X-Amz-Expires=%s on the URL itself, want 120", want)
	}
}

// presigning against A bucket that does not exist succeeds, and that is the
// fail-open shape is about.
func TestPresigningAgainstAMissingBucketSucceedsAndTheUploadIsWhatFails(t *testing.T) {
	store := realMinIO(t, "r2-no-such-bucket-anywhere")

	body := []byte("a photograph nobody can store")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut against a missing bucket answered an error: %v.\n"+
			"    That is the COLD-CACHE branch, and it means the region is not "+
			"pinned on the signing client — see media.New.", err)
	}
	if code, status := put(t, url, headers, body); code != "NoSuchBucket" {
		t.Fatalf("the upload answered %d %s, want NoSuchBucket — which is what a "+
			"phone would see, three steps after a green acceptance check", status, code)
	}
}

// EnsureBucket is what turns the leg above into a working upload, and it is
// idempotent because it runs at every boot and `restart.
func TestEnsureBucketMakesTheSameUploadWorkAndIsANoOpTwice(t *testing.T) {
	var salt [4]byte
	if _, err := rand.Read(salt[:]); err != nil {
		t.Fatalf("naming a bucket: %v", err)
	}
	store := realMinIO(t, "r2-ensure-"+hex.EncodeToString(salt[:]))

	body := []byte("a photograph that needs somewhere to live")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}
	up := media.Upload{SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png"}

	before, beforeHeaders, err := store.PresignPut(t.Context(), key, up)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if code, status := put(t, before, beforeHeaders, body); code != "NoSuchBucket" {
		t.Fatalf("before EnsureBucket the upload answered %d %s, want NoSuchBucket — "+
			"if the bucket is already there this leg proves nothing", status, code)
	}

	if err := store.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	if err := store.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket a second time: %v — it runs at every boot", err)
	}

	after, afterHeaders, err := store.PresignPut(t.Context(), key, up)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if code, status := put(t, after, afterHeaders, body); status != 200 {
		t.Fatalf("after EnsureBucket the upload answered %d %s", status, code)
	}
	if st, err := store.Stat(t.Context(), key); err != nil || st.Size != int64(len(body)) {
		t.Fatalf("the round trip through the created bucket: %v / %d", err, st.Size)
	}
}

// One traveller's key cannot address another's object.
func TestOneTravellersUrlCannotBeRepointedAtAnothers(t *testing.T) {
	store := freshBucket(t)

	body := []byte("somebody else's photograph")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := media.Key{Traveller: travellerID, Object: digest}

	url, headers, err := store.PresignPut(t.Context(), key, media.Upload{
		SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if code, status := put(t, url, headers, body); status != 200 {
		t.Fatalf("the control upload was refused: %d %s", status, code)
	}

	const otherTraveller = "11111111-2222-3333-4444-555555555555"
	read, err := store.PresignGet(t.Context(), key, media.Private)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if status, _, _ := fetch(t, read); status != 200 {
		t.Fatalf("the control read answered %d", status)
	}

	repointed := strings.Replace(read, travellerID, otherTraveller, 1)
	if repointed == read {
		t.Fatal("the traveller segment is not in the URL, so this leg is editing nothing")
	}
	if status, body, _ := fetch(t, repointed); status != 403 {
		t.Fatalf("a URL repointed at another traveller answered %d: %s", status, string(body))
	}
}

// The skip says what to do.
func TestTheSkipNamesTheVariableAndTheTarget(t *testing.T) {
	t.Setenv(endpointVar, "")
	if err := os.Unsetenv(endpointVar); err != nil {
		t.Fatalf("unsetting %s: %v", endpointVar, err)
	}

	spy := &skipSpy{}
	realMinIO(spy, "whatever")

	if !spy.skipped {
		t.Fatal("with no endpoint set, realMinIO did not skip — every leg in this " +
			"file would then FAIL on a machine with no Docker rather than saying " +
			"there is nothing to run against")
	}
	for _, want := range []string{endpointVar, "make test-s3"} {
		if !strings.Contains(spy.message, want) {
			t.Errorf("the skip message does not name %q: %s", want, spy.message)
		}
	}
}

type skipSpy struct {
	skipped bool
	message string
}

func (s *skipSpy) Helper() {}
func (s *skipSpy) Skipf(format string, args ...any) {
	s.skipped = true
	s.message = fmt.Sprintf(format, args...)
}
func (s *skipSpy) Fatalf(format string, args ...any) {
	panic("realMinIO called Fatalf: " + fmt.Sprintf(format, args...))
}

// fetch GETs a presigned read URL and answers the status, the body and the
// response headers.
func fetch(t *testing.T, url string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building the GET: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Header
}

// expires reads X-Amz-Expires off a presigned URL.
func expires(t *testing.T, presigned string) string {
	t.Helper()
	u, err := neturl.Parse(presigned)
	if err != nil {
		t.Fatalf("parsing the presigned URL: %v", err)
	}
	return u.Query().Get("X-Amz-Expires")
}
