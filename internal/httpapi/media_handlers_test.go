// The three media routes, over the real mux, the real middleware chain and the
// real auth, against media.Memory and a fake row store. Test-first.
//
// IT RUNS WITHOUT A DATABASE AND WITHOUT MinIO ON PURPOSE, and the split is
// DEC-16's line drawn once more. What is here is what leaves the PROCESS: the
// statuses, the codes, the field names, the header map's key set against the
// URL's own signed-header list, and the two headers DEC-51 asks for. What only
// a real bucket can say — that a signature is right, that MinIO answers
// XAmzContentChecksumMismatch — is in internal/media's integration tier and is
// not repeated. What only a real PostgreSQL can say — that the conflict branch
// is bounded — is in internal/postgres and is not repeated either.
//
// media.Memory IS NOT A STUB THAT SAYS YES, which is what makes these legs
// worth anything: it enforces the digest, the exact length, the content type,
// the write-once and a bucket that has to exist, with the S3 codes the real
// server answers.
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/logbook"
	"travellog/internal/media"
)

// testMediaMaxBytes is small enough that a leg can go over it with a literal
// and large enough that the fixture bytes fit. It is NOT the deployed number —
// deploy/.env.example carries 26214400 — and nothing here asserts that one,
// which is stated rather than left implicit.
const testMediaMaxBytes = int64(1 << 20)

// === the fake row store ===

// fakeMedia is media_objects, and it honours the two rules the real statement
// honours: the conflict branch is BOUNDED by `uploaded_at IS NULL`, and
// `alreadyExists` is derived from uploaded_at and never from row presence.
//
// A FAKE THAT DID NOT WOULD MAKE EVERY LEG BELOW GREEN AGAINST THE CONTRACT
// THE STORE EXISTS TO KEEP. That is the same reason fakeLogbook honours
// DEC-89: a fake applying every field regardless proves the handler compiles.
type fakeMedia struct {
	mu       sync.Mutex
	rows     map[string]logbook.MediaObject
	failWith error
	begins   int
}

func newFakeMedia() *fakeMedia {
	return &fakeMedia{rows: map[string]logbook.MediaObject{}}
}

func (f *fakeMedia) BeginMedia(_ context.Context, _ string, b logbook.MediaBegin) (logbook.MediaObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.MediaObject{}, f.failWith
	}
	f.begins++

	existing, held := f.rows[*b.SHA256]
	if held && existing.Committed() {
		// THE BOUNDED CONFLICT BRANCH. Without it a client re-beginning an
		// already-committed digest rewrites what those bytes ARE.
		return existing, nil
	}
	row := logbook.MediaObject{
		ID:          *b.SHA256,
		ByteSize:    *b.ByteSize,
		ContentType: *b.ContentType,
		CreatedAt:   time.Unix(0, 0).UTC(),
	}
	if held {
		row.CreatedAt = existing.CreatedAt
	}
	f.rows[row.ID] = row
	return row, nil
}

func (f *fakeMedia) MediaObjects(_ context.Context, _ string, ids []string) ([]logbook.MediaObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []logbook.MediaObject
	for _, id := range ids {
		if row, held := f.rows[id]; held {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeMedia) MarkMediaUploaded(_ context.Context, _ string, id string) (logbook.MediaObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.MediaObject{}, f.failWith
	}
	row, held := f.rows[id]
	if !held {
		return logbook.MediaObject{}, fmt.Errorf("%w: %s", logbook.ErrNoMediaObject, id)
	}
	if !row.Committed() {
		at := time.Unix(1, 0).UTC()
		row.UploadedAt = &at
		f.rows[id] = row
	}
	return row, nil
}

// === helpers ===

var fixtureBytes = []byte("a photograph, as far as this package is concerned\n")

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func beginRequest(digest string, size int, contentType string) string {
	body, _ := json.Marshal(map[string]any{
		"sha256": digest, "byteSize": size, "contentType": contentType,
	})
	return string(body)
}

func decode(t *testing.T, got answer) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(got.body, &out); err != nil {
		t.Fatalf("decoding %s: %v", got.body, err)
	}
	return out
}

// uploadBytes is what the client does with what begin handed it: PUT the bytes
// at the address, through a twin that applies the same four refusals the
// signature makes real MinIO apply.
func (h *harness) uploadBytes(t *testing.T, travellerID, digest string, body []byte) {
	t.Helper()
	if err := h.objects.Put(
		media.Key{Traveller: travellerID, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(body)), ContentType: "image/png"},
		body); err != nil {
		t.Fatalf("uploading through the twin: %v", err)
	}
}

// travellerID is the id the bucket key is built from, read back through the
// only route that hands it out.
//
// IT GOES THROUGH THE ROUTE RATHER THAN INTO THE FAKE STORE, and the reason is
// the bucket key: `media.Key.Traveller` is a PATH SEGMENT, so a leg that
// invented an id would be uploading to a prefix no handler ever addresses and
// every commit would 409 for a reason that has nothing to do with what is
// being tested.
func (h *harness) travellerID(t *testing.T, _ string) string {
	t.Helper()
	// The sign-in body carries only the token (it is the one place a plaintext
	// token is ever written), so the id comes off the store the harness holds
	// — which is the one place in this package that knows how a traveller is
	// minted, and it is a fake either way.
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	for _, tr := range h.store.travellers {
		return tr.ID
	}
	t.Fatal("the fake store holds no traveller")
	return ""
}

func (f *fakeMedia) beginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins
}

// === the legs ===

// AN ASSET IS REFERENCEABLE ONLY AFTER IT IS COMMITTED, AND THE POSITIVE HALF
// IS WHAT MAKES THIS LEG WORTH ANYTHING.
//
// v6's own note: a validator that rejects everything passes "an uncommitted
// asset is refused" perfectly. So both halves are in one leg, and the mutation
// that proves it is "make it reject everything" — which reddens the POSITIVE
// half.
//
// THE REFERENCE IS `PUT /v1/trips/{id}`'s `coverAsset`, AND THE ENFORCEMENT IS
// THE SCHEMA'S RATHER THAN THIS PACKAGE'S (DEC-58): all four asset columns
// carry a real foreign key to media_objects, so a reference to an unbegun
// object is refused by the database. THAT MEANS THIS PARTICULAR LEG CANNOT
// LIVE HERE — the handler tier has no foreign keys — and it is in
// internal/postgres/schema_test.go, where it always was. What IS here is the
// half this tier owns: the three routes' own answers.
func TestBeginMintsAnUploadCapabilityAndCommitTurnsItIntoAnAsset(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	begun := h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token)
	if begun.status != http.StatusCreated {
		t.Fatalf("POST /v1/media = %d %s, want 201", begun.status, begun.body)
	}
	body := decode(t, begun)

	// THE ADDRESS IS THE DIGEST. Answering a server-minted id would make the
	// client's own de-duplication impossible and would make a retry a second
	// upload.
	if body["id"] != digest {
		t.Errorf("id = %v, want the content hash %s — the address IS the digest", body["id"], digest)
	}
	if body["alreadyExists"] != false {
		t.Errorf("alreadyExists = %v on a first begin, want false", body["alreadyExists"])
	}
	if body["uploadUrl"] == nil || body["uploadUrl"] == "" {
		t.Fatalf("no uploadUrl on a first begin: %v", body)
	}
	if body["expiresAt"] == nil {
		t.Errorf("no expiresAt — the client has no way to know how long it has")
	}

	// COMMIT BEFORE THE UPLOAD IS A 409 AND NAMES `upload_incomplete`. The
	// object EXISTS as a row and the request is well-formed; what is wrong is
	// its state.
	early := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if early.status != http.StatusConflict {
		t.Fatalf("commit before upload = %d %s, want 409", early.status, early.body)
	}
	if code := decode(t, early)["code"]; code != "upload_incomplete" {
		t.Errorf("code = %v, want upload_incomplete", code)
	}

	h.uploadBytes(t, traveller, body["id"].(string), fixtureBytes)

	committed := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if committed.status != http.StatusOK {
		t.Fatalf("commit = %d %s, want 200", committed.status, committed.body)
	}
	after := decode(t, committed)
	if after["alreadyExists"] != true {
		t.Errorf("alreadyExists = %v after a commit, want true", after["alreadyExists"])
	}
	if after["uploadedAt"] == nil {
		t.Errorf("uploadedAt is null after a commit — `alreadyExists` is DERIVED " +
			"from it, so a null one makes every later begin report false")
	}

	// AND THE POSITIVE HALF OF THE MINT: a committed object is mintable.
	minted := h.do(t, http.MethodPost, "/v1/media/mint", `{"ids":["`+digest+`"]}`, token)
	if minted.status != http.StatusOK {
		t.Fatalf("mint = %d %s, want 200", minted.status, minted.body)
	}
	urls, ok := decode(t, minted)["urls"].([]any)
	if !ok || len(urls) != 1 {
		t.Fatalf("urls = %v, want one", decode(t, minted)["urls"])
	}
}

// A SECOND BEGIN FOR A COMMITTED DIGEST ANSWERS alreadyExists AND MINTS NO
// SECOND WRITE CAPABILITY (V4-SF5a).
//
// `alreadyExists` IS DERIVED FROM uploaded_at AND NEVER FROM ROW PRESENCE, and
// the leg proves both directions: after the FIRST begin it is false even
// though a row exists, and after the commit it is true. Derive it from row
// existence and the first assertion reddens — which is the state where the
// client skips an upload that never happened and the commit 409s with no way
// forward.
func TestASecondBeginAnswersAlreadyExistsAndMintsNoSecondUploadURL(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	first := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))
	if first["alreadyExists"] != false {
		t.Fatalf("alreadyExists = %v on a first begin — a ROW exists, and that is "+
			"not what the field means", first["alreadyExists"])
	}

	// A BEGIN THAT NEVER UPLOADED IS STILL false. This is the assertion that
	// separates "derived from uploaded_at" from "derived from row presence".
	again := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))
	if again["alreadyExists"] != false {
		t.Errorf("alreadyExists = %v after a begin that never uploaded, want false", again["alreadyExists"])
	}
	if again["uploadUrl"] == nil {
		t.Errorf("no uploadUrl on a re-begin of an object that has NOT landed — " +
			"that is the retry content addressing exists to make free")
	}

	h.uploadBytes(t, traveller, first["id"].(string), fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)

	third := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))
	if third["alreadyExists"] != true {
		t.Fatalf("alreadyExists = %v on a committed digest, want true", third["alreadyExists"])
	}
	// NO SECOND WRITE CAPABILITY. Handing out a live PUT against a committed
	// address is worse than a merely redundant one: what stops it landing is
	// `If-None-Match: *`, which is a property of the SIGNATURE and not of this
	// response, so an omitted URL is the only thing this response controls.
	for _, key := range []string{"uploadUrl", "uploadHeaders", "expiresAt"} {
		if _, present := third[key]; present {
			t.Errorf("%s is present on an alreadyExists response: %v", key, third[key])
		}
	}
}

// A SECOND COMMIT IS 200 AND NOT 409, AND THE ROW IS UNCHANGED (SAF-MIN-12).
//
// The bucket-versus-database seam is the only non-atomic one in the plan: the
// bucket confirms, the database update fails, and the object exists with
// uploaded_at NULL — bytes the user has uploaded and cannot attach. This route
// is the retry, and it is only a retry if asking twice is allowed.
func TestCommittingTwiceIsASuccessAndChangesNothing(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	begun := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))
	h.uploadBytes(t, traveller, begun["id"].(string), fixtureBytes)

	first := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if first.status != http.StatusOK {
		t.Fatalf("first commit = %d %s", first.status, first.body)
	}
	second := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if second.status != http.StatusOK {
		t.Fatalf("second commit = %d %s, want 200 — a client that lost the first "+
			"response has no route to retry that says so", second.status, second.body)
	}
	if string(first.body) != string(second.body) {
		t.Errorf("the two commits answered different rows:\n  %s\n  %s", first.body, second.body)
	}
}

// THE HEADER MAP'S KEY SET EQUALS THE URL'S X-Amz-SignedHeaders MINUS `host`
// (DEC-88).
//
// IT IS EQUALITY IN BOTH DIRECTIONS AND NOT A SUBSET, because a map with an
// EXTRA header is as broken as one with a missing header and only equality
// catches both. Without this leg the map and the signature drift the first
// time a header is added to one and not the other, and every upload 400s with
// AccessDenied for ever — a failure no unit test can see, because both halves
// look right in isolation.
func TestTheUploadHeadersAreExactlyTheHeadersTheSignatureCovers(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	digest := digestOf(fixtureBytes)
	body := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))

	signed, err := media.SignedHeaders(body["uploadUrl"].(string))
	if err != nil {
		t.Fatalf("reading X-Amz-SignedHeaders back off the URL: %v", err)
	}
	var want []string
	for _, name := range signed {
		if name != "host" {
			want = append(want, name)
		}
	}
	sort.Strings(want)

	handed, ok := body["uploadHeaders"].(map[string]any)
	if !ok {
		t.Fatalf("uploadHeaders is %T, want a map", body["uploadHeaders"])
	}
	var got []string
	for name := range handed {
		got = append(got, name)
	}
	sort.Strings(got)

	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("uploadHeaders keys = %v, the URL signs %v (minus host)\n"+
			"    a map with an extra header is as broken as one with a missing "+
			"header, and a client handed either gets 400 AccessDenied for ever",
			got, want)
	}
	if len(want) == 0 {
		t.Fatal("the URL signs `host` and nothing else, so this leg compared two " +
			"empty sets — that is the shape a URL from one of the two BANNED " +
			"presign calls has")
	}

	// AND THE VALUE THE CLIENT CANNOT DERIVE. The checksum header is the
	// BASE64 digest while `id` is HEX, so a client handed only the URL has no
	// way to produce it — which is the whole of DEC-88's second finding.
	checksum, held := handed["x-amz-checksum-sha256"].(string)
	if !held {
		t.Fatal("no x-amz-checksum-sha256 in the map")
	}
	if checksum == digest {
		t.Error("the checksum header carries the HEX digest, which MinIO answers " +
			"400 InvalidArgument for — it is base64 by the S3 protocol")
	}
	raw, err := base64.StdEncoding.DecodeString(checksum)
	if err != nil || hex.EncodeToString(raw) != digest {
		t.Errorf("x-amz-checksum-sha256 = %q, which does not decode to %s", checksum, digest)
	}
}

// THE begin RESPONSE'S expiresAt AGREES WITH THE SIGNATURE'S OWN WINDOW.
//
// A handler computing it from a second copy of the lifetime would be two
// variables holding one fact, and the failure is SILENT: the client is told a
// window that is not the window the signature carries, and the upload dies
// with SignatureDoesNotMatch some minutes later.
func TestExpiresAtIsTheWindowTheSignatureActuallyCarries(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	digest := digestOf(fixtureBytes)
	body := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token))

	lifetime, err := media.ExpiresIn(body["uploadUrl"].(string))
	if err != nil {
		t.Fatalf("ExpiresIn: %v", err)
	}
	at, err := time.Parse(time.RFC3339, body["expiresAt"].(string))
	if err != nil {
		t.Fatalf("expiresAt = %v: %v", body["expiresAt"], err)
	}
	now := h.deps.Clock()()
	if got := at.Sub(now); got != lifetime {
		t.Errorf("expiresAt is %s from now and the URL is signed for %s", got, lifetime)
	}

	// AND IT IS THE PRIVATE LIFETIME, not the public one. An upload capability
	// belongs to the traveller who asked for it; nothing public ever writes.
	if lifetime != h.objects.TTL[media.Private] {
		t.Errorf("the upload URL is signed for %s and the PRIVATE lifetime is %s",
			lifetime, h.objects.TTL[media.Private])
	}
}

// EVERY PRESIGNED GET CARRIES `response-content-disposition=attachment`
// (DEC-51).
//
// THE DEFENCE IS THE DISPOSITION PLUS THE ALLOWLIST, and the residual is named
// rather than hidden: a MISLABELLED object is downloaded, never rendered. The
// allowlist half is in internal/logbook and migration 0003; this is the other
// half, at the one route that hands a read capability to a phone.
//
// `X-Content-Type-Options: nosniff` IS DELIBERATELY NOT ASSERTED and the reason
// is carried in internal/media/minio.go: S3's response-header override set is
// CLOSED and nosniff is not in it, so a leg asserting it would pass locally by
// measuring MinIO's own default and would silently stop being true in
// production.
func TestEveryMintedReadURLIsMarkedAsADownload(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	var ids []string
	for _, body := range [][]byte{fixtureBytes, append([]byte("second "), fixtureBytes...)} {
		digest := digestOf(body)
		begun := decode(t, h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(body), "image/png"), token))
		h.uploadBytes(t, traveller, digest, body)
		h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
		_ = begun
		ids = append(ids, digest)
	}

	minted := h.do(t, http.MethodPost, "/v1/media/mint", `{"ids":["`+ids[0]+`","`+ids[1]+`"]}`, token)
	if minted.status != http.StatusOK {
		t.Fatalf("mint = %d %s", minted.status, minted.body)
	}
	urls := decode(t, minted)["urls"].([]any)
	if len(urls) != 2 {
		t.Fatalf("urls = %v, want two", urls)
	}

	// ONE URL PER ID, IN ORDER. The client pairs by index, so a reordering is
	// every photograph in the grid showing somebody else's.
	for i, raw := range urls {
		parsed, err := url.Parse(raw.(string))
		if err != nil {
			t.Fatalf("parsing %v: %v", raw, err)
		}
		if !strings.Contains(parsed.Path, ids[i]) {
			t.Errorf("url %d addresses %s, want the object %s", i, parsed.Path, ids[i])
		}
		if got := parsed.Query().Get("response-content-disposition"); got != "attachment" {
			t.Errorf("url %d carries response-content-disposition=%q, want attachment — "+
				"an object stored as text/html is served AS HTML from the bucket origin",
				i, got)
		}
	}
}

// A MINT OF AN UNCOMMITTED ID IS REFUSED, AND AN UNKNOWN ONE IS REFUSED
// DIFFERENTLY.
//
// TWO MISSES, TWO ANSWERS, AND THE CLIENT ACTS ON THEM DIFFERENTLY. An id this
// traveller has never begun is `not_found` — a wrong reference, nothing to
// wait for. An id begun and not committed is `upload_incomplete` — the answer
// is to finish the upload and ask again. Collapsing them would tell a client
// to retry a reference that can never resolve.
func TestMintingRefusesAnObjectThatIsNotThereYet(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	unknown := strings.Repeat("b", 64)
	got := h.do(t, http.MethodPost, "/v1/media/mint", `{"ids":["`+unknown+`"]}`, token)
	if got.status != http.StatusNotFound {
		t.Errorf("minting an id nothing holds = %d %s, want 404", got.status, got.body)
	}
	if code := decode(t, got)["code"]; code != "not_found" {
		t.Errorf("code = %v, want not_found", code)
	}

	begun := digestOf(fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(begun, len(fixtureBytes), "image/png"), token)
	pending := h.do(t, http.MethodPost, "/v1/media/mint", `{"ids":["`+begun+`"]}`, token)
	if pending.status != http.StatusConflict {
		t.Errorf("minting a begun-but-uncommitted id = %d %s, want 409", pending.status, pending.body)
	}
	if code := decode(t, pending)["code"]; code != "upload_incomplete" {
		t.Errorf("code = %v, want upload_incomplete", code)
	}
}

// THE ALLOWLIST AND THE BOUND ARE 422s THAT NAME THE FIELD, AND THEY HAPPEN
// BEFORE ANYTHING IS SIGNED.
//
// The second half is the one worth asserting: a 422 taken AFTER a mint is a
// capability that exists, and MEDIA_MAX_BYTES is defined as the refusal to
// mint. The leg counts begins, so a handler that signed first would redden.
func TestBeginRefusesAWrongTypeOrAnOversizeBeforeItMintsAnything(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	digest := digestOf(fixtureBytes)

	for _, c := range []struct {
		name  string
		body  string
		field string
	}{
		{"text/html", beginRequest(digest, 10, "text/html"), "contentType"},
		{"image/heic, which DEC-104 took out", beginRequest(digest, 10, "image/heic"), "contentType"},
		{"over MEDIA_MAX_BYTES", beginRequest(digest, int(testMediaMaxBytes)+1, "image/png"), "byteSize"},
		{"a digest that is not one", beginRequest("kyoto", 10, "image/png"), "sha256"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := h.media.beginCount()
			got := h.do(t, http.MethodPost, "/v1/media", c.body, token)
			if got.status != http.StatusUnprocessableEntity {
				t.Fatalf("= %d %s, want 422", got.status, got.body)
			}
			answered := decode(t, got)
			if answered["code"] != "invalid_field" || answered["field"] != c.field {
				t.Errorf("= %v, want invalid_field on %s", answered, c.field)
			}
			if after := h.media.beginCount(); after != before {
				t.Errorf("the store was asked to begin %d time(s) for a body that "+
					"was refused — MEDIA_MAX_BYTES is a refusal to MINT, taken "+
					"BEFORE the capability exists", after-before)
			}
		})
	}
}

// A COMMIT FOR AN ID NOTHING HOLDS IS A 404, AND A MALFORMED PATH IS A 422.
//
// The path is untrusted — a stale route, a bad push, a deep link — and it
// reaches a store with no validator between it and a query unless the
// handler's first line puts one there.
func TestCommittingAnIDNothingHoldsIsRefusedByName(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	unknown := h.do(t, http.MethodPost, "/v1/media/"+strings.Repeat("c", 64)+"/commit", "", token)
	if unknown.status != http.StatusNotFound {
		t.Errorf("commit of an unknown id = %d %s, want 404", unknown.status, unknown.body)
	}
	if code := decode(t, unknown)["code"]; code != "not_found" {
		t.Errorf("code = %v, want not_found", code)
	}

	malformed := h.do(t, http.MethodPost, "/v1/media/kyoto/commit", "", token)
	if malformed.status != http.StatusUnprocessableEntity {
		t.Errorf("commit of a path that is not a digest = %d %s, want 422",
			malformed.status, malformed.body)
	}
}

// THE COMMIT VERIFIES THE STORED DIGEST AS WELL AS THE SIZE, AND THE EMPTY
// CHECKSUM IS WHAT TURNS THE BAN INTO A RUNTIME GUARD (DEC-98's free half).
//
// `StatObject` with `Checksum: true` returns the digest the BUCKET stored, at
// no extra call — and an object uploaded through either of the two BANNED
// presign calls carries NO checksum at all. So a commit that requires a
// non-empty matching value refuses such an object at the moment it would
// otherwise become referenceable. Note the flag: without `Checksum: true` the
// field comes back empty and this check passes nothing, which is why
// internal/media's Stat sets it and says so.
func TestACommitRefusesAnObjectThatCarriesNoStoredChecksum(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token)

	// What an upload through a `host`-only signature leaves behind: the right
	// bytes at the right address, with no checksum recorded against them.
	h.objects.PutWithoutChecksum(media.Key{Traveller: traveller, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(fixtureBytes)), ContentType: "image/png"},
		fixtureBytes)

	got := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if got.status != http.StatusConflict {
		t.Fatalf("commit of an object with no stored checksum = %d %s, want 409",
			got.status, got.body)
	}

	// THE POSITIVE CONTROL, in the same function. Without it this leg is
	// satisfied by a commit path that refuses everything.
	other := append([]byte("control "), fixtureBytes...)
	otherDigest := digestOf(other)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(otherDigest, len(other), "image/png"), token)
	h.uploadBytes(t, traveller, otherDigest, other)
	if ok := h.do(t, http.MethodPost, "/v1/media/"+otherDigest+"/commit", "", token); ok.status != http.StatusOK {
		t.Fatalf("the control commit = %d %s, want 200", ok.status, ok.body)
	}
}
