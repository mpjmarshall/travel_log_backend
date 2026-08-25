// The one read and the one write, over the real mux, the real middleware chain
// and the real auth, with a fake store. Test-first.
//
// IT RUNS WITHOUT A DATABASE ON PURPOSE, for the reason auth_handlers_test.go
// gives: the legs that matter here are about what leaves the process — an
// empty 304 body, a tag with both halves, a document that was never assembled
// — and putting them behind a TEST_DATABASE_URL skip is putting them where
// legs go to stop being run. What only a real PostgreSQL can say is in
// internal/postgres/logbook_store_test.go and is not repeated here.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// === the fake ===

// fakeLogbook is a whole little store rather than a stub, and it earns that:
// THE SPLICE LEG (V4-B1) needs a PUT to change what a following GET returns,
// which a stub answering constants cannot do.
type fakeLogbook struct {
	mu        sync.Mutex
	version   int64
	doc       logbook.Document
	assembles int
	deletes   int
	lastWrite logbook.TripWrite
	failWith  error
}

func (f *fakeLogbook) Read(_ context.Context, _ string, assemble func(int64) bool) (logbook.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Snapshot{}, f.failWith
	}
	snap := logbook.Snapshot{Version: f.version}
	if assemble(f.version) {
		f.assembles++
		doc := f.doc
		snap.Document = &doc
	}
	return snap, nil
}

func (f *fakeLogbook) PutTrip(_ context.Context, _ string, w logbook.TripWrite) (logbook.Trip, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Trip{}, 0, f.failWith
	}
	f.lastWrite = w

	// THE FAKE HONOURS DEC-89 TOO, and it has to: a fake that applies every
	// field regardless would make every handler leg green against the contract
	// the store was just fixed to keep. `leave` is the one-line spelling of
	// "absent means leave alone".
	id := ""
	if w.ID != nil {
		id = *w.ID
	}
	// A TRIP CREATED THROUGH THIS FAKE CARRIES THE CLIENT'S OWN SHARING
	// DEFAULTS, WHICH ARE NOT GO'S ZERO VALUES. Migration 0002 gave
	// `share_photos` and `share_notes` a DEFAULT of true, so a trip created on
	// the server is born with photo and note sharing on; a fake born at
	// false/false/false makes every "the other two switches were left alone"
	// assertion pass against an implementation that reset them.
	next := logbook.Trip{ID: id, SharePhotos: true, ShareNotes: true}
	replaced := false
	for i, existing := range f.doc.Trips {
		if existing.ID != id {
			continue
		}
		next = existing
		// The sharing group survives a write that does not name it, exactly as
		// the upsert's column list makes it survive in PostgreSQL.
		next.ShareLinkID = existing.ShareLinkID
		next.SharePhotos, next.ShareNotes, next.ShareCoordinates =
			existing.SharePhotos, existing.ShareNotes, existing.ShareCoordinates
		f.doc.Trips[i] = next
		replaced = true
	}
	applyTripWrite(&next, w)
	if replaced {
		for i, existing := range f.doc.Trips {
			if existing.ID == id {
				f.doc.Trips[i] = next
			}
		}
	} else {
		f.doc.Trips = append(f.doc.Trips, next)
	}
	f.version++
	return next, f.version, nil
}

// applyTripWrite is the fake's half of DEC-89: only the fields the body
// carried are written over the trip as it stands.
func applyTripWrite(t *logbook.Trip, w logbook.TripWrite) {
	if w.Name != nil {
		t.Name = *w.Name
	}
	if w.CityIDs != nil {
		t.CityIDs = *w.CityIDs
	}
	if logbook.Sent(w.Start) {
		t.Start = logbook.Value(w.Start)
	}
	if logbook.Sent(w.End) {
		t.End = logbook.Value(w.End)
	}
	if logbook.Sent(w.Summary) {
		t.Summary = logbook.Value(w.Summary)
	}
	if logbook.Sent(w.CoverAsset) {
		t.CoverAsset = logbook.Value(w.CoverAsset)
	}
}

// DeleteTrip is the fake's D3: it removes the trip and the rows the schema's
// cascades would remove, and it KEEPS every place — which is the sheet's own
// promise and the one thing a fake that "just deletes the trip" would get
// right by accident and a fake that deletes too much would get wrong.
//
// A MISS MOVES NO VERSION HERE EITHER. The store's contract says so, and a
// fake that bumped anyway would make TestARepeatedDeleteAnswersTheSameTag
// green against a store that did.
func (f *fakeLogbook) DeleteTrip(_ context.Context, _ string, tripID string) (logbook.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Snapshot{}, f.failWith
	}

	held := false
	trips := f.doc.Trips[:0:0]
	for _, trip := range f.doc.Trips {
		if trip.ID == tripID {
			held = true
			continue
		}
		trips = append(trips, trip)
	}
	if held {
		f.doc.Trips = trips
		photos := f.doc.Photos[:0:0]
		for _, photo := range f.doc.Photos {
			if photo.TripID != tripID {
				photos = append(photos, photo)
			}
		}
		f.doc.Photos = photos
		walks := f.doc.Walks[:0:0]
		for _, walk := range f.doc.Walks {
			if walk.TripID != tripID {
				walks = append(walks, walk)
			}
		}
		f.doc.Walks = walks
		for i := range f.doc.Places {
			visits := f.doc.Places[i].Visits[:0:0]
			for _, visit := range f.doc.Places[i].Visits {
				if visit.TripID != tripID {
					visits = append(visits, visit)
				}
			}
			f.doc.Places[i].Visits = visits
		}
		f.version++
	}
	f.deletes++
	doc := f.doc
	return logbook.Snapshot{Version: f.version, Document: &doc}, nil
}

// SetTravellerName honours the trim and the refusal, because those are the
// store's contract rather than the handler's — a fake that accepted "   "
// would make the 422 leg green against a store that wrote it.
func (f *fakeLogbook) SetTravellerName(_ context.Context, _ string, name string) (logbook.Traveller, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Traveller{}, 0, f.failWith
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return logbook.Traveller{}, 0, logbook.InvalidFieldError{Field: "name",
			Why: "a traveller needs a name, and an empty one is not a way to clear it"}
	}
	f.doc.Traveller = &logbook.Traveller{Name: trimmed}
	f.version++
	return *f.doc.Traveller, f.version, nil
}

// === the share twin ===

// fakeShare is logbook.ShareStore, and it honours DEC-89's pointer contract
// and the reset for the reason fakeLogbook honours the trip one: a twin that
// applies every field regardless makes every handler leg green against the
// contract the store was written to keep.
type fakeShare struct {
	mu    sync.Mutex
	books *fakeLogbook
}

func (f *fakeShare) SetShareOptions(_ context.Context, _, tripID string, w logbook.ShareWrite) (logbook.Trip, int64, error) {
	return f.change(tripID, func(t *logbook.Trip) {
		if w.SharePhotos != nil {
			t.SharePhotos = *w.SharePhotos
		}
		if w.ShareNotes != nil {
			t.ShareNotes = *w.ShareNotes
		}
		if w.ShareCoordinates != nil {
			t.ShareCoordinates = *w.ShareCoordinates
		}
	})
}

func (f *fakeShare) NewShareLink(_ context.Context, _, tripID, token string) (logbook.Trip, int64, error) {
	return f.change(tripID, func(t *logbook.Trip) {
		t.ShareLinkID = &token
		t.Shared = true
	})
}

func (f *fakeShare) StopSharing(_ context.Context, _, tripID string) (logbook.Trip, int64, error) {
	return f.change(tripID, func(t *logbook.Trip) {
		t.ShareLinkID = nil
		t.Shared = false
		// The client's own three defaults, written by name — see
		// resetShareFlagsSQL.
		t.SharePhotos, t.ShareNotes, t.ShareCoordinates = true, true, false
	})
}

// change is the shape all three share, including the 404: a share write is a
// SETTER, and a set asks for a value the log then has to hold.
func (f *fakeShare) change(tripID string, apply func(*logbook.Trip)) (logbook.Trip, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.books.mu.Lock()
	defer f.books.mu.Unlock()
	if f.books.failWith != nil {
		return logbook.Trip{}, 0, f.books.failWith
	}
	for i, trip := range f.books.doc.Trips {
		if trip.ID != tripID {
			continue
		}
		apply(&trip)
		f.books.doc.Trips[i] = trip
		f.books.version++
		return trip, f.books.version, nil
	}
	return logbook.Trip{}, 0, fmt.Errorf("%w: %s", logbook.ErrNoTrip, tripID)
}

func (f *fakeLogbook) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deletes
}

func (f *fakeLogbook) assembleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.assembles
}

// === the fixture ===

const aTrip = `{"id":"kyoto","name":"Kyoto in May","cityIds":["kyoto"],
	"start":"2027-05-12T00:00:00.000Z","end":"2027-05-15T00:00:00.000Z",
	"summary":null,"coverAsset":null}`

// bearer registers a traveller and signs in, through the real routes, and
// answers the Authorization header a protected route wants.
func bearer(t *testing.T, h *harness) string {
	t.Helper()
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.post(t, "/v1/auth/session", registered)
	if issued.status != http.StatusCreated {
		t.Fatalf("sign in = %d %s", issued.status, issued.body)
	}
	token, held := issued.decode(t)["token"].(string)
	if !held {
		t.Fatalf("the sign-in answered no token: %s", issued.body)
	}
	return "Bearer " + token
}

// bearerFor is `bearer` for any address: register, sign in, answer the header.
// A leg about two travellers needs two of these and one of `bearer`'s email.
func bearerFor(t *testing.T, h *harness, email string) string {
	t.Helper()
	body := credentialsFor(email)
	if got := h.post(t, "/v1/auth/register", body); got.status != http.StatusCreated {
		t.Fatalf("register %s = %d %s", email, got.status, got.body)
	}
	return signInAs(t, h, email)
}

// signInAs signs a traveller who ALREADY EXISTS in again, so a leg can hold two
// live tokens for one traveller. VS6 mints a new token per sign-in and asserts
// it; this is what a second device looks like from the server's side.
func signInAs(t *testing.T, h *harness, email string) string {
	t.Helper()
	issued := h.post(t, "/v1/auth/session", credentialsFor(email))
	if issued.status != http.StatusCreated {
		t.Fatalf("sign in %s = %d %s", email, issued.status, issued.body)
	}
	token, held := issued.decode(t)["token"].(string)
	if !held {
		t.Fatalf("the sign-in answered no token: %s", issued.body)
	}
	return "Bearer " + token
}

func (h *harness) get(t *testing.T, path, bearer string, headers map[string]string) answer {
	t.Helper()
	return h.doWithHeaders(t, http.MethodGet, path, "", bearer, headers)
}

// === the read ===

func TestTheLogbookComesBackAsTheClientsOwnEnvelope(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.get(t, "/v1/logbook", token, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/logbook = %d %s", got.status, got.body)
	}

	body := got.decode(t)
	if body["version"] != float64(logbook.FormatVersion) {
		t.Errorf("version = %v, want %d", body["version"], logbook.FormatVersion)
	}
	inner, held := body["logbook"].(map[string]any)
	if !held {
		t.Fatalf("no `logbook` object: %s", got.body)
	}
	for _, key := range []string{"trips", "cities", "places", "photos", "walks"} {
		list, isList := inner[key].([]any)
		if !isList {
			t.Errorf("%s = %#v, want a list — an unimplemented list is EMPTY, not absent "+
				"and not null", key, inner[key])
			continue
		}
		if len(list) != 0 {
			t.Errorf("%s holds %d items on a fresh log", key, len(list))
		}
	}
	if traveller, held := inner["traveller"]; !held || traveller != nil {
		t.Errorf("traveller = %#v (held=%v), want null", traveller, held)
	}
}

// DEC-49's first half, asserted ON THE CONSTANT so it cannot be quietly
// dropped: a tag naming only the data validates the data and not the code that
// rendered it.
func TestA200CarriesATagWithBothHalves(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.version = 7

	got := h.get(t, "/v1/logbook", token, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET = %d %s", got.status, got.body)
	}
	tag := got.header.Get("ETag")
	emitter, data, ok := parseTag(t, tag)
	if !ok {
		t.Fatalf("ETag = %q, which does not parse as W/\"<emitter>-<logbook>\"", tag)
	}
	if emitter != logbook.EmitterVersion {
		t.Errorf("the emitter half is %d, want %d", emitter, logbook.EmitterVersion)
	}
	if data != 7 {
		t.Errorf("the logbook half is %d, want 7", data)
	}
}

func TestAMatchingIfNoneMatchAnswers304WithAnEmptyBody(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.version = 7

	first := h.get(t, "/v1/logbook", token, nil)
	tag := first.header.Get("ETag")

	again := h.get(t, "/v1/logbook", token, map[string]string{"If-None-Match": tag})
	if again.status != http.StatusNotModified {
		t.Fatalf("a revalidation answered %d %s, want 304", again.status, again.body)
	}
	if len(again.body) != 0 {
		t.Errorf("the 304 carries %d bytes: %q — a 304 has no body", len(again.body), again.body)
	}
	if got := again.header.Get("ETag"); got != tag {
		t.Errorf("the 304's ETag = %q, want %q", got, tag)
	}
}

// THE LEG THE SLICE NAMES: the 304 must not assemble the document. A body
// built and then thrown away saves bandwidth and no server work at all, which
// is half the point of DEC-31.
func TestA304NeverAssemblesTheDocument(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.version = 7

	first := h.get(t, "/v1/logbook", token, nil)
	if h.logbook.assembleCount() != 1 {
		t.Fatalf("the first read assembled %d times, want 1", h.logbook.assembleCount())
	}

	for range 3 {
		h.get(t, "/v1/logbook", token, map[string]string{"If-None-Match": first.header.Get("ETag")})
	}
	if got := h.logbook.assembleCount(); got != 1 {
		t.Errorf("after three revalidations the store assembled %d times, want 1", got)
	}
}

// The other direction, and it is what stops the leg above being satisfied by a
// handler that answers 304 to everything.
func TestAStaleIfNoneMatchAnswers200(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.version = 7

	got := h.get(t, "/v1/logbook", token, map[string]string{"If-None-Match": `W/"1-6"`})
	if got.status != http.StatusOK {
		t.Errorf("a stale tag answered %d, want 200", got.status)
	}
}

// DEC-49(a), AND IT IS THE DEFECT THE FIRST HALF OF THE TAG EXISTS FOR: a
// deploy that changes the emitted document moves no data. A client holding a
// tag minted by a DIFFERENT emitter must be answered 200, not 304, or it keeps
// serving the old shape until somebody happens to write.
func TestATagFromAnotherEmitterDoesNotRevalidate(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.version = 7

	got := h.get(t, "/v1/logbook", token,
		map[string]string{"If-None-Match": `W/"99-7"`})
	if got.status != http.StatusOK {
		t.Errorf("a tag from emitter 99 at the same data version answered %d, want 200 — "+
			"bumping the emitter constant alone must invalidate every cached client",
			got.status)
	}
}

func TestAWriteBetweenTwoReadsYieldsANewTag(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	before := h.get(t, "/v1/logbook", token, nil).header.Get("ETag")
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}
	after := h.get(t, "/v1/logbook", token, nil)

	if after.header.Get("ETag") == before {
		t.Errorf("the tag is %q before and after a write", before)
	}
	if after.status != http.StatusOK {
		t.Errorf("the read after a write = %d, want 200", after.status)
	}
	stale := h.get(t, "/v1/logbook", token, map[string]string{"If-None-Match": before})
	if stale.status != http.StatusOK {
		t.Errorf("the tag from before the write still revalidates (%d)", stale.status)
	}
}

// A traveller who has never written has logbook_version 0, and DEC-49's tag
// needs BOTH halves — FormatETag panics on a zero. The read answers 200 with
// no tag rather than a tag with a half missing, which is the shape a cache
// cannot act on and would rather not be given.
func TestAnUnwrittenLogIsServedWithNoTagAtAll(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.get(t, "/v1/logbook", token, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET = %d %s", got.status, got.body)
	}
	if tag := got.header.Get("ETag"); tag != "" {
		t.Errorf("ETag = %q at logbook_version 0, want none", tag)
	}
	if got := h.get(t, "/v1/logbook", token, map[string]string{"If-None-Match": "*"}); got.status != http.StatusOK {
		t.Errorf("`If-None-Match: *` against an untagged log answered %d, want 200 — "+
			"a 304 here hands the client an empty body it reads as unchanged", got.status)
	}
}

// DEC-30 disclaims byte stability across serialisations; within one build it
// must hold, or the tag is a claim the server cannot keep.
func TestTwoReadsWithNoWriteBetweenThemAreByteIdentical(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	first := h.get(t, "/v1/logbook", token, nil)
	for i := range 4 {
		again := h.get(t, "/v1/logbook", token, nil)
		if string(again.body) != string(first.body) {
			t.Fatalf("read %d differs from the first with no write between them", i+2)
		}
	}
}

// A presigned URL changes on every mint, so a body carrying one differs every
// request and 304 never fires. Deterministic here because the slice mints none.
func TestNoValueOnTheWireIsAURL(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	body := string(h.get(t, "/v1/logbook", token, nil).body)
	for _, scheme := range []string{`"http://`, `"https://`} {
		if strings.Contains(body, scheme) {
			t.Errorf("the emitted document carries %s — a URL in the payload kills the cache", scheme)
		}
	}
}

// === DEC-53 ===

func TestAFormatThisBuildCannotEmitAnswers406AndNamesWhatItCan(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	for _, asked := range []string{"3", "1", "banana", "2.0", "-1"} {
		got := h.get(t, "/v1/logbook", token, map[string]string{formatHeader: asked})
		if got.status != http.StatusNotAcceptable {
			t.Errorf("X-Logbook-Format: %q = %d, want 406", asked, got.status)
			continue
		}
		if code := got.decode(t)["code"]; code != string(httpx.CodeUnsupportedFormat) {
			t.Errorf("code = %v, want %q", code, httpx.CodeUnsupportedFormat)
		}
		if named := got.header.Get(formatHeader); named != "2" {
			t.Errorf("the 406 names %q as what it can emit, want \"2\" — an old client "+
				"turns that into 'update the app' instead of a silent refetch loop", named)
		}
	}
}

// AND A 406 MUST NOT ASSEMBLE IT EITHER. This leg exists because the mutation
// that removed the handler's early gate SURVIVED: Emit refuses the version on
// its own and writeLogbookFailure maps that to the same 406 with the same
// header, so the two paths agree on every byte the client sees. What they do
// not agree on is the work: without the gate the read opens a snapshot and
// builds the whole document before refusing it.
func TestA406NeverAssemblesTheDocument(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.get(t, "/v1/logbook", token, map[string]string{formatHeader: "3"})
	if got.status != http.StatusNotAcceptable {
		t.Fatalf("GET with X-Logbook-Format: 3 = %d %s, want 406", got.status, got.body)
	}
	if n := h.logbook.assembleCount(); n != 0 {
		t.Errorf("a refused format assembled the log %d time(s), want 0 — the check "+
			"belongs before the snapshot, not after it", n)
	}
}

// DEC-53: a missing header is treated as the current version, so the header is
// additive and a client that never learned to send it is no worse off.
func TestAMissingFormatHeaderIsTheCurrentVersion(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusOK {
		t.Errorf("a read with no X-Logbook-Format = %d, want 200", got.status)
	}
	if got := h.get(t, "/v1/logbook", token, map[string]string{formatHeader: "2"}); got.status != http.StatusOK {
		t.Errorf("a read asking for 2 = %d, want 200", got.status)
	}
	// AN EMPTY VALUE TAKES THE SAME BRANCH, and that is a decision rather than
	// an accident: `Header.Get` cannot tell `X-Logbook-Format:` with nothing
	// after it from a header that was never sent, and a 406 for a client that
	// declared nothing would refuse a request DEC-53 says to serve.
	if got := h.get(t, "/v1/logbook", token, map[string]string{formatHeader: ""}); got.status != http.StatusOK {
		t.Errorf("a read whose format header is empty = %d, want 200", got.status)
	}
}

// === the write ===

func TestAPutAnswers200WithTheTripAndTheNewTag(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.put(t, "/v1/trips/kyoto", aTrip, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	body := got.decode(t)
	if body["id"] != "kyoto" || body["name"] != "Kyoto in May" {
		t.Errorf("the response is not the trip: %s", got.body)
	}
	if body["start"] != "2027-05-12T00:00:00.000Z" {
		t.Errorf("start = %v, want the date the client sent, rendered its way", body["start"])
	}
	emitter, data, ok := parseTag(t, got.header.Get("ETag"))
	if !ok || emitter != logbook.EmitterVersion || data < 1 {
		t.Errorf("ETag = %q, want the new version under this emitter", got.header.Get("ETag"))
	}
}

// FOUND BY RUNNING IT, NOT BY A TEST. Against the real server a PUT with an
// empty cityIds answered `"cityIds":null`, because the write path answers a
// bare entity and does not go through Emit — which is where the nil-slice
// normalisation lived. The client reads cityIds as `as List<dynamic>`, with no
// null branch, so it throws. The splice leg could not see it: it appends the
// returned trip to a document and re-emits, and Emit normalised it on the way.
func TestATripWrittenWithNoCitiesComesBackWithAnEmptyList(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	// The body OMITS cityIds, which is what makes the slice nil. A body
	// carrying `"cityIds":[]` decodes to an empty non-nil slice and marshals as
	// `[]` with or without the normalisation, so a leg written that way cannot
	// fail — measured: it survived the mutation that removed EmitTrip.
	got := h.put(t, "/v1/trips/kyoto", `{"id":"kyoto","name":"Kyoto"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}
	if !strings.Contains(string(got.body), `"cityIds":[]`) {
		t.Errorf("the write answered %s\n    want `\"cityIds\":[]` — null is the one shape "+
			"the client's `as List<dynamic>` throws on", got.body)
	}
}

// THE ACCEPTANCE CHECK: a PUT body carrying shareCoordinates leaves the stored
// flag UNCHANGED. Two halves, and the second is the one a status assertion
// cannot see — the request must not even reach the store carrying it.
func TestAPutCarryingShareCoordinatesLeavesTheFlagAlone(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT (create) = %d %s", got.status, got.body)
	}
	h.logbook.mu.Lock()
	if len(h.logbook.doc.Trips) != 1 {
		h.logbook.mu.Unlock()
		t.Fatalf("the store holds %d trips after a create, want 1", len(h.logbook.doc.Trips))
	}
	h.logbook.doc.Trips[0].ShareCoordinates = true
	h.logbook.doc.Trips[0].SharePhotos = true
	h.logbook.mu.Unlock()

	got := h.put(t, "/v1/trips/kyoto",
		`{"id":"kyoto","name":"Kyoto again","cityIds":["kyoto"],"shareCoordinates":false,"sharePhotos":false}`,
		token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT (replace) = %d %s", got.status, got.body)
	}

	body := got.decode(t)
	if body["shareCoordinates"] != true || body["sharePhotos"] != true {
		t.Errorf("the sharing flags came back %v/%v, want both true — the write reset a "+
			"group the client writes through copyWithShare", body["shareCoordinates"], body["sharePhotos"])
	}
	if body["name"] != "Kyoto again" {
		t.Errorf("the rename did not land: %s", got.body)
	}
}

// THE SPLICE LEG (V4-B1). A phone that PUTs a trip splices the returned entity
// into its cached document rather than re-fetching the whole log — 95,586 bytes
// at fixture scale through this build, measured, and NOT the 85,422 of the
// client's own file (DEC-32, DEC-102). If the two
// disagree, the cache is wrong and nothing on either side finds out.
//
// It compares DECODED DOCUMENTS rather than bytes, because DEC-30 disclaims
// byte stability — and the splice is put through the SAME emitter, so a
// difference is a difference in the log rather than in the rendering.
func TestTheSplicedDocumentEqualsTheOneTheServerEmits(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	cached := decodeEnvelope(t, h.get(t, "/v1/logbook", token, nil).body)

	written := h.put(t, "/v1/trips/kyoto", aTrip, token)
	if written.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", written.status, written.body)
	}
	var spliced logbook.Trip
	if err := json.Unmarshal(written.body, &spliced); err != nil {
		t.Fatalf("decoding the write's answer: %v", err)
	}
	cached.Logbook.Trips = append(cached.Logbook.Trips, spliced)

	rendered, err := logbook.Emit(logbook.FormatVersion, cached.Logbook)
	if err != nil {
		t.Fatalf("re-emitting the spliced document: %v", err)
	}
	mine, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	theirs := h.get(t, "/v1/logbook", token, nil).body
	if string(mine) != string(theirs) {
		t.Errorf("the spliced document and the server's disagree\n  spliced: %s\n  served:  %s",
			mine, theirs)
	}
}

func TestAPutWhoseBodyIdDisagreesWithThePathIsRefused(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.put(t, "/v1/trips/kyoto",
		`{"id":"osaka","name":"Osaka","cityIds":[]}`, token)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("PUT with a mismatched id = %d %s, want 422", got.status, got.body)
	}
	body := got.decode(t)
	if body["code"] != string(httpx.CodeInvalidField) || body["field"] != "id" {
		t.Errorf("the refusal is %s, want invalid_field on `id`", got.body)
	}
}

// A body with no id at all is the path's, which is what makes the route an
// upsert on a client-minted key rather than a create.
func TestAPutWithNoIdInTheBodyTakesThePaths(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.put(t, "/v1/trips/kyoto", `{"name":"Kyoto","cityIds":[]}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}
	if got.decode(t)["id"] != "kyoto" {
		t.Errorf("id = %v, want kyoto", got.decode(t)["id"])
	}
}

func TestAPutRefusesAFieldAndNamesIt(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.put(t, "/v1/trips/kyoto", `{"id":"kyoto","name":"","cityIds":[]}`, token)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("PUT with no name = %d %s, want 422", got.status, got.body)
	}
	if field := got.decode(t)["field"]; field != "name" {
		t.Errorf("field = %v, want name", field)
	}
}

// A slug id, not a twelve-character generated one (DEC-02). The arc PUTs
// `/v1/trips/kyoto` and this is the leg that says the route table's pattern
// does not quietly constrain it.
func TestThePathIdIsASlug(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	for _, id := range []string{"kyoto", "autumn-crossing", "magome-trailhead", "japan-2026"} {
		got := h.put(t, "/v1/trips/"+id,
			`{"name":"A trip","cityIds":[]}`, token)
		if got.status != http.StatusOK {
			t.Errorf("PUT /v1/trips/%s = %d %s", id, got.status, got.body)
		}
	}
}

func TestAPutWithARubbishBodyIsRefusedAsInvalidBody(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.put(t, "/v1/trips/kyoto", `{"id":`, token)
	if got.status != http.StatusBadRequest {
		t.Fatalf("PUT with truncated JSON = %d %s, want 400", got.status, got.body)
	}
	if code := got.decode(t)["code"]; code != string(httpx.CodeInvalidBody) {
		t.Errorf("code = %v, want invalid_body", code)
	}
}

// === what both routes share ===

func TestBothLogbookRoutesRefuseAMissingCredential(t *testing.T) {
	h := newHarness(t, options{})

	for _, call := range []struct {
		name string
		got  answer
	}{
		{"GET /v1/logbook", h.get(t, "/v1/logbook", "", nil)},
		{"PUT /v1/trips/kyoto", h.put(t, "/v1/trips/kyoto", aTrip, "")},
	} {
		if call.got.status != http.StatusUnauthorized {
			t.Errorf("%s with no Authorization = %d %s, want 401", call.name, call.got.status, call.got.body)
		}
		if code := call.got.decode(t)["code"]; code != string(httpx.CodeUnauthenticated) {
			t.Errorf("%s answered %v, want unauthenticated", call.name, code)
		}
	}
}

// A database that has gone away is a 500 and never a 401: the phone's answer
// to a 401 is to discard a session it cannot get back.
func TestAStoreFailureIsA500ThatSaysNothingAboutIt(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.failWith = errors.New("dial tcp 127.0.0.1:5434: connect: connection refused")

	for _, call := range []struct {
		name string
		got  answer
	}{
		{"GET /v1/logbook", h.get(t, "/v1/logbook", token, nil)},
		{"PUT /v1/trips/kyoto", h.put(t, "/v1/trips/kyoto", aTrip, token)},
	} {
		if call.got.status != http.StatusInternalServerError {
			t.Errorf("%s = %d %s, want 500", call.name, call.got.status, call.got.body)
		}
		if strings.Contains(string(call.got.body), "5434") {
			t.Errorf("%s put the driver error on the wire: %s", call.name, call.got.body)
		}
	}
	if !strings.Contains(h.logs.String(), "5434") {
		t.Errorf("the detail reached neither the body nor the log")
	}
}

// A traveller deleted between the credential being accepted and the query
// running is a credential that is not live: a 401, and the phone signs in
// again. Reporting it as a 500 would have it wait for a server that is fine.
func TestATravellerWhoHasGoneIsA401(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.failWith = logbook.ErrNoTraveller

	if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusUnauthorized {
		t.Errorf("GET for a traveller who has gone = %d %s, want 401", got.status, got.body)
	}
}

// === helpers ===

func parseTag(t *testing.T, tag string) (emitter, data int64, ok bool) {
	t.Helper()
	return httpx.ParseETag(tag)
}

func decodeEnvelope(t *testing.T, raw []byte) logbook.Envelope {
	t.Helper()
	var out logbook.Envelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding an envelope: %v", err)
	}
	return out
}

// DEC-89 AT THE WIRE, AND THE BODY IS THE ONE THE CLIENT SENDS. The store
// legs prove the statement; this proves the DECODE — that a key the client
// left out arrives at the store as nil rather than as a zero value, which is
// the half a store test written against a Go literal cannot see.
func TestATwoKeyRenameArrivesAtTheStoreAsAbsenceAndNotAsEmptiness(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("the create answered %d", got.status)
	}
	// T4's pencil. Two keys, and nothing else is in the body at all.
	if got := h.put(t, "/v1/trips/kyoto", `{"id":"kyoto","name":"Kyoto in May, renamed"}`, token); got.status != http.StatusOK {
		t.Fatalf("the rename answered %d: %s", got.status, got.body)
	}

	w := h.logbook.lastWrite
	if w.Name == nil || *w.Name != "Kyoto in May, renamed" {
		t.Errorf("Name = %v, want the sent one — the field that WAS sent must arrive", w.Name)
	}
	for _, absent := range []struct {
		field string
		sent  bool
	}{
		{"cityIds", w.CityIDs != nil},
		{"start", logbook.Sent(w.Start)},
		{"end", logbook.Sent(w.End)},
		{"summary", logbook.Sent(w.Summary)},
		{"coverAsset", logbook.Sent(w.CoverAsset)},
	} {
		if absent.sent {
			t.Errorf("%s arrived as SENT from a body that does not contain the key — "+
				"absence and emptiness are the same value again, which is the whole "+
				"of the defect", absent.field)
		}
	}
}

// AND THE ONE SHAPE THAT MUST STILL BE HEARD: an EMPTY list is a client saying
// "this trip visits nowhere", which T5 can produce by removing the last city.
// It is not the same as omitting the key, and a contract that conflated them
// would make the itinerary un-emptyable — the mirror of the defect.
func TestAnEmptyCityListIsHeardWhileAnAbsentOneIsNot(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("the create answered %d", got.status)
	}
	if got := h.put(t, "/v1/trips/kyoto", `{"id":"kyoto","cityIds":[]}`, token); got.status != http.StatusOK {
		t.Fatalf("the empty-list write answered %d: %s", got.status, got.body)
	}
	w := h.logbook.lastWrite
	if w.CityIDs == nil {
		t.Fatalf("cityIds arrived as absent from a body carrying `[]`")
	}
	if len(*w.CityIDs) != 0 {
		t.Errorf("cityIds = %v, want an empty list", *w.CityIDs)
	}
}

// === DEC-96: an outage is a 503 with Retry-After, not a 500 ===

// MEASURED by the operations lens with Postgres killed: every route answered
// `500 {"code":"internal"}` with no Retry-After. A 500 tells a client the
// server has a bug and the request is poison; the truth is "the dependency is
// down, try again shortly", and the difference decides whether a phone retries
// or gives up. It is also unanswerable afterwards, because a 500 count then
// conflates handler bugs with outages.
//
// THE LEG COVERS BOTH HALVES OF THE API. The store failure reaches the read and
// the write through writeLogbookFailure, and it reaches the auth middleware
// through a separate switch — three `default: CodeInternal` branches, and a fix
// applied to one of them is the shape of miss this table exists to prevent.
func TestAnUnreachableDatabaseIs503WithRetryAfterAndNot500(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a driver connect failure", fmt.Errorf("postgres: reading trips: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})},
		{"a connection database/sql has already closed", fmt.Errorf("postgres: %w", sql.ErrConnDone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, options{})
			token := bearer(t, h)
			h.logbook.failWith = tc.err

			for _, call := range []struct {
				what string
				got  answer
			}{
				{"GET /v1/logbook", h.get(t, "/v1/logbook", token, nil)},
				{"PUT /v1/trips/kyoto", h.put(t, "/v1/trips/kyoto", aTrip, token)},
			} {
				if call.got.status != http.StatusServiceUnavailable {
					t.Errorf("%s = %d, want 503 — a request that cannot reach the "+
						"database has not encountered a handler bug, and 500 tells "+
						"a client not to retry", call.what, call.got.status)
				}
				if got := call.got.header.Get("Retry-After"); got != "5" {
					t.Errorf("%s carries Retry-After %q, want \"5\" — 'try again "+
						"shortly' is the whole difference between this and a "+
						"poison request", call.what, got)
				}
			}
		})
	}
}

// AND THE CONTROL. A genuine handler fault is still a 500 with no Retry-After,
// or the classification above is a classifier that calls everything an outage.
func TestAGenuineFaultIsStill500WithNoRetryAfter(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logbook.failWith = errors.New("sql: Scan error on column index 3")

	got := h.get(t, "/v1/logbook", token, nil)
	if got.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", got.status)
	}
	if retry := got.header.Get("Retry-After"); retry != "" {
		t.Errorf("Retry-After = %q on a handler fault — retrying a poison request "+
			"fails identically for ever", retry)
	}
}

// === DEC-101, and it is a rule for R5-R8 rather than a fact about this file ===

// EVERY 500 EMITS EXACTLY ONE ERROR LINE CARRYING THE requestId AND THE
// UNDERLYING ERROR, AND THE LEG COUNTS LINES RATHER THAN GREPPING FOR ONE.
//
// A grep passes against a handler that logs the same failure three times from
// three layers, which is how a 3am log becomes unreadable; and it passes
// against a handler that logs nothing, if any OTHER line happens to match. So
// the assertion is a count. `auth_handlers.go` already did this and it was
// observed working under a killed Postgres, joined by requestId; R5-R8 write
// fourteen more handlers, and a 500 whose only line is the access line is a
// dead end.
func TestEvery500EmitsExactlyOneErrorLine(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logs.Reset()
	h.logbook.failWith = errors.New("sql: Scan error on column index 3")

	got := h.get(t, "/v1/logbook", token, nil)
	if got.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — this leg is about the 500's LOG", got.status)
	}

	var errorLines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("a log line is not JSON: %q", line)
		}
		// THE ACCESS LINE IS NOT ONE OF THEM, and excluding it is a
		// definition rather than a convenience: a 500's access line is
		// already at ERROR (accessLevel), and it carries no `err` — DEC-101
		// asks for the line that carries the requestId AND THE UNDERLYING
		// ERROR, which is the diagnostic one. Counting both would make the
		// answer two for a correct handler and the leg meaningless.
		if _, isAccess := decoded["durationUs"]; isAccess {
			continue
		}
		if decoded["level"] == "ERROR" {
			errorLines = append(errorLines, decoded)
		}
	}
	if len(errorLines) != 1 {
		t.Fatalf("%d diagnostic ERROR lines for one 500, want exactly 1 — a grep "+
			"would have passed against both 0 and 3:\n%s",
			len(errorLines), h.logs.String())
	}
	line := errorLines[0]
	if id, _ := line["requestId"].(string); id == "" {
		t.Errorf("the ERROR line carries no requestId, so nothing joins it to the "+
			"access line: %v", line)
	}
	if detail, _ := line["err"].(string); !strings.Contains(detail, "Scan error") {
		t.Errorf("the ERROR line does not carry the underlying error: %v — the "+
			"body cannot, so this is the only place the detail exists", line)
	}
}

// AND THE ACCESS LINE NAMES THE ROUTE PATTERN AND THE TRAVELLER, through the
// real Mount and the real auth middleware rather than through a hand-recorded
// slot. The httpx legs prove the mechanism; this proves the WIRING, which is
// the half that was missing — `route` comes from the table in Mount and
// `travellerId` from RequireTraveller, and neither is reachable from httpx.
func TestTheAccessLineForARealRouteNamesThePatternAndTheTraveller(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	h.logs.Reset()

	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	var access map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			continue
		}
		if _, isAccess := decoded["durationUs"]; isAccess {
			access = decoded
		}
	}
	if access == nil {
		t.Fatalf("no access line:\n%s", h.logs.String())
	}
	if access["route"] != "PUT /v1/trips/{id}" {
		t.Errorf("route = %v, want the matched pattern — `path` alone is the raw "+
			"URL, so every trip is its own line and nothing aggregates",
			access["route"])
	}
	if access["path"] != "/v1/trips/kyoto" {
		t.Errorf("path = %v, want the raw path beside the pattern", access["path"])
	}
	if id, _ := access["travellerId"].(string); id == "" {
		t.Errorf("no travellerId on an authenticated line: %v", access)
	}
}
