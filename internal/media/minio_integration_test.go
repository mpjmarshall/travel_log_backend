//go:build integration

// The integration tier: internal/media against a REAL MinIO.
//
// WHY IT IS A REAL SERVER AND NOT A FAKE. Every claim this package makes is a
// claim about what a bucket does with a signature, and a fake would answer
// whatever the author believed. Three of the four blockers the storage lens
// raised against this step were invisible to reasoning and visible in one
// round trip. DEC-43 is the standing form of the argument: MinIO is a more
// forgiving verifier than real S3, so a REFUSAL measured here is strong
// evidence and an ACCEPTANCE is weak — which is why the legs below assert
// refusals and why each carries a positive control so the refusal cannot be
// "this server refuses everything".
//
// It is behind a build tag AND behind TEST_S3_ENDPOINT, so `make check` on a
// machine with no Docker is an honest green.
//
//	make test-s3
//	TEST_S3_ENDPOINT=... go test -tags integration ./internal/media/ -count=1
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

	// A fixed traveller so every key in this file is readable in `mc ls`, and
	// a UUID because that is what media_objects.traveller_id is.
	travellerID = "0f9c1f36-2e5d-4a1c-9a3f-6d2b1c7e4a80"

	// The two lifetimes, DEC-47's pair with DEC-84's number. They are given to
	// the store here rather than read from config, because internal/media does
	// not read the environment — internal/config does, and it is the only
	// package that may (spec L30).
	ttlPrivate = 2 * time.Minute
	ttlPublic  = 15 * time.Minute
)

// tb is the slice of *testing.T this file's skip path needs, as an interface
// so the skip itself can be exercised. internal/postgres/testdb makes the same
// move for the same reason: a leg that only asserted the skip STRING would
// leave "does it actually skip?" proven by nothing.
type tb interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// realMinIO answers a store pointed at the server TEST_S3_ENDPOINT names, in a
// bucket of this run's own, and SKIPS — loudly enough to read — when there is
// none.
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

// freshBucket is a bucket per RUN, per test function. The legs create objects
// and DEC-88 makes a content address write-once, so a bucket shared with an
// earlier run answers 412 for a reason that has nothing to do with the leg.
//
// THE UNIQUE PART GOES FIRST, AND THAT IS A CORRECTION RATHER THAN A STYLE.
// The first version appended a timestamp to the test's name and truncated the
// whole thing to S3's 63-character bucket limit — which cut the timestamp off
// and gave every run the same bucket. The naive-signer run (see ban_test.go)
// left an object behind, and the next run's checksum leg answered 412
// PreconditionFailed instead of the checksum refusal it was written for: a leg
// reporting the wrong control because of its own fixture.
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

// s3Error is the XML body every S3-compatible server answers a refusal with.
// THE CODE IS THE ASSERTION AND THE STATUS CLASS IS NOT, and that is the whole
// lesson of the storage lens's first blocker: four distinct failure modes land
// in 4xx and only one of them is any given leg's control.
type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// put replays exactly the headers PresignPut handed back and answers the S3
// error code and the status. A 2xx answers an empty code.
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
	// net/http fills ContentLength from the reader; -1 forces chunked.
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
		// A refusal with no XML body is still an answer; report what there is
		// rather than failing here, so the leg's own message is what prints.
		return fmt.Sprintf("<unparseable body: %q>", string(raw)), resp.StatusCode
	}
	return parsed.Code, resp.StatusCode
}

// THE UPLOAD MUST BE REFUSED BY THE BUCKET, NOT BY US. A presigned PUT whose
// signature does not cover the client's digest lets a lying client write
// arbitrary bytes at an address that claims to be their hash — and every later
// reader trusts the address. Two of minio-go's three presign calls produce
// exactly that URL (ban_test.go names them) and both look perfectly correct
// until somebody uploads the wrong bytes, which is why this leg is written
// against a REAL server: MinIO is a forgiving verifier, and it is still the
// strictest one we have short of S3 itself.
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

	// THE HEADERS MUST BE REPLAYED OR THIS LEG CANNOT SEE THE CHECKSUM AT ALL.
	// A version that set none answered 400 AccessDenied ("There were headers
	// present in the request which were not signed") for LYING bytes AND for
	// HONEST bytes alike — so the leg was GREEN with zero digest verification
	// happening anywhere. FOUR failure modes land in the 4xx class and only
	// the last is the control: AccessDenied (header not replayed),
	// InvalidArgument (hex where base64 belongs), SignatureDoesNotMatch (wrong
	// signed-header set or a changed value), XAmzContentChecksumMismatch (the
	// actual thing). Assert the CODE.
	//
	// AND THE LYING BYTES ARE THE SAME LENGTH AS THE HONEST ONES, WHICH IS A
	// CORRECTION TO THIS LEG AND NOT A DETAIL. The version this was written
	// from used a 45-byte lie against a 29-byte signature — but DEC-51 signs
	// `content-length` too, so that request is refused by the LENGTH control
	// with SignatureDoesNotMatch and the digest is never reached. Two controls
	// means each leg must vary exactly one thing: this one varies the body and
	// holds every signed header, length included, constant.
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

	// And the object must not be there at all: a refused PUT that still wrote
	// something is the same defect wearing a 4xx.
	if _, err := store.Stat(t.Context(), key); err == nil {
		t.Fatal("the object exists after a refused upload")
	}
}

// AND THE ATTACKER DOES NOT REPLAY ANYTHING — WHICH IS THE LEG THAT ACTUALLY
// CATCHES THE BAN, and it is here because the leg above turned out not to.
//
// MEASURED, and it was a surprise: with the banned presigner in place (host
// signed and nothing else) and the four headers still SENT, MinIO validates
// the digest anyway and answers XAmzContentChecksumMismatch — so the checksum
// leg above stays GREEN under that mutation. It exercises an honest client.
// An attacker holding the same URL simply OMITS the digest header, and with
// only `host` in the signature there is then nothing left to refuse: 200, and
// arbitrary bytes at an address claiming to be their hash.
//
// So this is the leg the ban is falsifiable by at this tier. `make check`'s
// ban_test.go is the other half, and neither replaces the other: one asserts
// the code does not call the thing, this one asserts what happens if it does.
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

	// Everything replayed EXCEPT the digest, and bytes that are not it.
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

// DEC-51 asked for content-length SIGNED INTO the PUT "so the BUCKET enforces
// it rather than the API hoping". What SigV4 can express is an EXACT value,
// not a ceiling — so what this leg proves is that a minted URL is not an
// unbounded write, and MEDIA_MAX_BYTES is a separate, API-side refusal to
// mint. Both sentences are needed and neither is true alone.
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

	// POSITIVE CONTROL FIRST, and it is not decoration: without it every
	// assertion below is satisfied by a server that refuses everything. It
	// also establishes that the replayed header set is COMPLETE — if it were
	// not, this 200 would be a 400 AccessDenied.
	if code, status := put(t, url, headers, honest); status != 200 {
		t.Fatalf("the honest bytes were refused: %d %s — the leg is measuring the "+
			"header replay rather than the length, and every assertion below is "+
			"then vacuous", status, code)
	}
	if st, err := store.Stat(t.Context(), key); err != nil || st.Size != int64(len(honest)) {
		t.Fatalf("Stat after the honest PUT: %v / %d", err, st.Size)
	}

	// THE LENGTH. A fresh key, because DEC-88 makes the first one write-once
	// and a second PUT there answers 412 for a reason that has nothing to do
	// with length — which is exactly the confusion this leg exists to remove.
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

	// AND NO CHUNKED BYPASS. Omitting Content-Length is the obvious way around
	// a signed one; MinIO refuses it outright rather than signing nothing.
	if code, status := putChunked(t, url2, headers2, oversized); code != "MissingContentLength" {
		t.Errorf("chunked upload answered %d %s, want 411 MissingContentLength — an "+
			"unsigned length is a signed length that was not sent", status, code)
	}
}

// THE MAP AND THE SIGNATURE CANNOT DRIFT (DEC-88). Without this leg, a header
// added to one and not the other is 400 AccessDenied on every upload for ever
// — and both halves look perfectly right read on their own.
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

	// The list itself, written out, so that adding a header is a deliberate
	// change to a test and not a silent widening of a capability.
	if strings.Join(want, ";") != "content-length;content-type;if-none-match;x-amz-checksum-sha256" {
		t.Errorf("the signature covers %v; the four are the digest (DEC-38), the "+
			"length (DEC-51), the type (DEC-87) and the write-once (DEC-88)", want)
	}
}

// A CONTENT ADDRESS IS WRITE-ONCE AT THE BUCKET (DEC-88). The third assertion
// is the attack, and it is the one a size check alone cannot see: bytes B with
// a self-consistent checksum FOR B, at a key naming sha256(A).
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

	// THE ATTACK. Different bytes, a checksum that is honest ABOUT THOSE BYTES,
	// signed against a key that names somebody else's digest. Before
	// If-None-Match this answered 200 and the address stopped holding its own
	// contents.
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
	// The signature covers the honest digest; the attacker cannot change it
	// without a new signature, so this is the strongest form available: the
	// bytes and their own checksum agree with each other and not with the key.
	// (Replacing the header value alone answers SignatureDoesNotMatch, which
	// is a different — and also correct — refusal.)
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

// THE TYPE IS INSIDE THE SIGNATURE (DEC-87), so the allowlist reaches the
// OBJECT and not only the database row. Before this, a row reading image/png
// could address an object the bucket served as text/html.
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

	// Vary ONLY the type. Every other signed header is replayed as given.
	lying := map[string]string{}
	for k, v := range headers {
		lying[k] = v
	}
	lying["content-type"] = "text/html"
	if code, status := put(t, url, lying, body); code != "SignatureDoesNotMatch" {
		t.Fatalf("a PUT declaring text/html against a URL signed for image/png "+
			"answered %d %s, want SignatureDoesNotMatch", status, code)
	}

	// The positive control, and the thing that is actually stored.
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

// The round trip, end to end: mint, upload, stat, mint a read, fetch, compare.
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
	// THE STORED DIGEST, WHICH IS FREE AND IS A RUNTIME GUARD ON THE BAN. An
	// object uploaded through either banned call carries NO checksum at all,
	// so a commit requiring a non-empty matching value cannot be satisfied by
	// one. It needs StatObjectOptions{Checksum: true} — without the flag the
	// field is silently empty and the check passes nothing.
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
	// PD-10's residual, and it is SIGNED, so the URL's holder cannot strip it:
	// a mislabelled object is downloaded rather than rendered on the storage
	// origin. Deliberately NOT asserted beside it: X-Content-Type-Options.
	// MinIO sets nosniff of its own accord and real S3 cannot, so a leg here
	// would measure MinIO's default and stop being true in production.
	if cd := header.Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("Content-Disposition is %q, want attachment", cd)
	}
}

// WHICH LIFETIME EACH AUDIENCE GETS (DEC-84), read off the URL the signer
// produced rather than compared to the config it came from. v7.1's leg
// compared the two configured values to each other, so a call site reaching
// for the private lifetime where the public one belongs reddened nothing.
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

// THE UPLOAD URL'S OWN WINDOW IS THE PRIVATE ONE, AND ExpiresIn IS WHAT THE
// BEGIN RESPONSE READS IT WITH.
//
// PresignPut TAKES NO AUDIENCE — an upload capability belongs to the
// authenticated traveller who asked for it and nothing public ever writes —
// so the lifetime is a fact about the signer rather than about a parameter,
// and `POST /v1/media`'s `expiresAt` is derived from the URL rather than from
// a second copy of the number. Getting that wrong is SILENT: the client is
// told a window the signature does not carry, and the upload dies with
// SignatureDoesNotMatch some minutes later.
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
	// AND IT AGREES WITH WHAT THE URL LITERALLY SAYS, so this leg is about
	// ExpiresIn as well as about the signer.
	if want := expires(t, url); want != "120" {
		t.Errorf("X-Amz-Expires=%s on the URL itself, want 120", want)
	}
}

// PRESIGNING AGAINST A BUCKET THAT DOES NOT EXIST SUCCEEDS, and that is the
// fail-open shape DEC-98 is about.
//
// TWO LENSES MEASURED OPPOSITE THINGS HERE and the plan asked for the
// reconciliation to be observed rather than reasoned. What this run observes
// is the SUCCEEDING one, and the reason is in New: the region is pinned on
// both clients, so minio-go never issues its `?location=` round trip and
// presigning is a local HMAC with nothing to fail. The storage lens saw the
// other branch because its client had no region and its cache was cold.
// Either way the bucket is not consulted, which is why the boot-time
// EnsureBucket and not a healthcheck is what closes it.
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

// EnsureBucket is what turns the leg above into a working upload (DEC-98), and
// it is idempotent because it runs at EVERY boot and `restart: always` means
// that is a lot of boots.
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

// ONE TRAVELLER'S KEY CANNOT ADDRESS ANOTHER'S OBJECT. The signature covers
// the PATH, so editing the traveller segment of a minted URL invalidates it —
// which is what makes the prefix a boundary rather than a naming convention.
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

// THE SKIP SAYS WHAT TO DO. A skipped leg that does not say it skipped is a
// green that means nothing, and this is the leg that proves the skip happens
// rather than the message merely existing.
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

// expires reads X-Amz-Expires off a presigned URL. It is a DELTA in seconds
// from X-Amz-Date, so nothing here needs a clock.
func expires(t *testing.T, presigned string) string {
	t.Helper()
	u, err := neturl.Parse(presigned)
	if err != nil {
		t.Fatalf("parsing the presigned URL: %v", err)
	}
	return u.Query().Get("X-Amz-Expires")
}
