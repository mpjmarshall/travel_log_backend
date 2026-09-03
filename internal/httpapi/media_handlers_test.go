// The three media routes, over the real mux, the real middleware chain and
// the real auth, against media.Memory and a fake row store.
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
// Large enough that the fixture bytes fit.
const testMediaMaxBytes = int64(1 << 20)

// fakeMedia is media_objects, and it honours's two rules the real statement
// honours.
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

// uploadBytes is what the client does with what begin handed it.
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
func (h *harness) travellerID(t *testing.T, _ string) string {
	t.Helper()
	for _, id := range h.store.TravellerIDs() {
		return id
	}
	t.Fatal("the twin holds no traveller")
	return ""
}

func (f *fakeMedia) beginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins
}

// an asset is referenceable only after it is committed, and the positive half
// is what makes this leg worth anything.
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

	minted := h.do(t, http.MethodPost, "/v1/media/mint", `{"ids":["`+digest+`"]}`, token)
	if minted.status != http.StatusOK {
		t.Fatalf("mint = %d %s, want 200", minted.status, minted.body)
	}
	urls, ok := decode(t, minted)["urls"].([]any)
	if !ok || len(urls) != 1 {
		t.Fatalf("urls = %v, want one", decode(t, minted)["urls"])
	}
}

// A second begin for A committed digest answers alreadyExists and mints no
// second write capability.
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
	for _, key := range []string{"uploadUrl", "uploadHeaders", "expiresAt"} {
		if _, present := third[key]; present {
			t.Errorf("%s is present on an alreadyExists response: %v", key, third[key])
		}
	}
}

// A second commit is 200 and not 409, and the row is unchanged.
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

// The header map's key set equals the url's X-Amz-SignedHeaders minus `host`.
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

// The begin response'S expiresAt agrees with the signature's own window.
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

	if lifetime != h.objects.TTL[media.Private] {
		t.Errorf("the upload URL is signed for %s and the PRIVATE lifetime is %s",
			lifetime, h.objects.TTL[media.Private])
	}
}

// Every presigned get carries `response-content-disposition=attachment`.
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

// A mint of an uncommitted id is refused, and an unknown one is refused
// differently.
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

// The allowlist and the bound are 422s that name the field, and they happen
// before anything is signed.
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

// A commit for an id nothing holds is a 404, and A malformed path is a 422.
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

// The commit verifies the stored digest as well as the size, and the empty
// checksum is what turns the ban into A runtime guard (the free half).
func TestACommitRefusesAnObjectThatCarriesNoStoredChecksum(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/png"), token)

	h.objects.PutWithoutChecksum(media.Key{Traveller: traveller, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(fixtureBytes)), ContentType: "image/png"},
		fixtureBytes)

	got := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if got.status != http.StatusConflict {
		t.Fatalf("commit of an object with no stored checksum = %d %s, want 409",
			got.status, got.body)
	}

	other := append([]byte("control "), fixtureBytes...)
	otherDigest := digestOf(other)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(otherDigest, len(other), "image/png"), token)
	h.uploadBytes(t, traveller, otherDigest, other)
	if ok := h.do(t, http.MethodPost, "/v1/media/"+otherDigest+"/commit", "", token); ok.status != http.StatusOK {
		t.Fatalf("the control commit = %d %s, want 200", ok.status, ok.body)
	}
}

// The commit refuses an object stored as something other than what the row
// declares (the other half).
func TestACommitRefusesAnObjectStoredAsSomethingElse(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes), "image/jpeg"), token)
	if err := h.objects.Put(
		media.Key{Traveller: traveller, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(fixtureBytes)), ContentType: "image/png"},
		fixtureBytes); err != nil {
		t.Fatalf("uploading through the twin: %v", err)
	}

	got := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if got.status != http.StatusConflict {
		t.Fatalf("commit of an object stored as image/png behind a row declaring "+
			"image/jpeg = %d %s, want 409 — the row is what the allowlist "+
			"constrains, and the OBJECT is what the bucket serves",
			got.status, got.body)
	}
}

// A commit must refuse an object whose size differs from what was begun.
func TestACommitRefusesAnObjectOfADifferentSize(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	traveller := h.travellerID(t, token)

	digest := digestOf(fixtureBytes)
	h.do(t, http.MethodPost, "/v1/media", beginRequest(digest, len(fixtureBytes)+1, "image/png"), token)
	h.uploadBytes(t, traveller, digest, fixtureBytes)

	got := h.do(t, http.MethodPost, "/v1/media/"+digest+"/commit", "", token)
	if got.status != http.StatusConflict {
		t.Fatalf("commit of %d bytes behind a row declaring %d = %d %s, want 409",
			len(fixtureBytes), len(fixtureBytes)+1, got.status, got.body)
	}
}
