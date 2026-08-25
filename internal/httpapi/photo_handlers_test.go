// R7's five routes over the real mux, the real middleware chain and the real
// auth, with fake stores. Test-first.
//
// WHAT IS HERE AND WHAT IS DELIBERATELY NOT. These legs are about what leaves
// the process: which shape a re-file answers, that a bare Walk never reaches
// the wire carrying `"points":null`, that an over-long track is refused BY
// NAME, that a snooze answers `[]` rather than `null`, and that a 204 still
// carries an ETag. What only a real PostgreSQL can say — that a caption-only
// write does not unfile a photograph, that a `{dismissed:true}` body leaves a
// track alone, that the count does not fall — is in internal/postgres and
// internal/seed and is NOT repeated here.
//
// R5 WROTE THAT RULE DOWN AFTER PAYING FOR IT: "a leg over a twin cannot guard
// a statement the twin does not execute". So the fakes below are honest little
// stores — they honour DEC-89's pointer contract and they refuse to write a
// half-filed pair — and nothing in this file is offered as evidence about a
// statement.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/logbook"
)

// === the fakes ===

// fakeMoments satisfies logbook.PhotoStore AND logbook.WalkStore over the same
// little document the trip fake writes to, so a photograph written here is a
// photograph a later re-file can name.
//
// IT HONOURS DEC-89 BECAUSE IT HAS TO, and on `Points` that is not a
// formality: a fake that wrote an empty track on a `{dismissed:true}` body
// would make the handler legs green against the contract this whole step is
// about. It also refuses to write `place_id` without `visit_id`, which is the
// pair rule DEC-83 leaves in Go — a twin that could express the half-filed
// state would let a leg pass over a shape no row may hold.
type fakeMoments struct {
	mu       sync.Mutex
	books    *fakeLogbook
	failWith error

	// lastPhotoWrite and lastWalkWrite are what the handler handed the store,
	// so a leg can assert that an OMITTED key arrived as nil rather than as a
	// zero value — the difference the whole step turns on, and one that is
	// invisible in the response.
	lastPhotoWrite logbook.PhotoWrite
	lastWalkWrite  logbook.WalkWrite

	// snoozes counts the calls, so a leg can prove the store was not reached
	// at all when the body was refused.
	snoozes int

	// mintOnRefile makes the next re-file answer the WHOLE ENVELOPE, which is
	// what the real store does when the client named an occasion the log does
	// not hold.
	mintOnRefile bool
}

func (f *fakeMoments) PutPhoto(_ context.Context, _ string, w logbook.PhotoWrite) (logbook.Photo, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Photo{}, 0, f.failWith
	}
	f.lastPhotoWrite = w

	id := ""
	if w.ID != nil {
		id = *w.ID
	}
	next := logbook.Photo{ID: id}
	found := -1
	for i, existing := range f.books.doc.Photos {
		if existing.ID == id {
			next, found = existing, i
		}
	}
	if found < 0 {
		// The store's own create-time refusals, so a leg about them is a leg
		// about the same rule the real store enforces under the lock.
		switch {
		case w.TripID == nil:
			return logbook.Photo{}, 0, logbook.InvalidFieldError{Field: "tripId",
				Why: "a photograph that is not in this log yet has no trip to leave alone"}
		case w.CityID == nil:
			return logbook.Photo{}, 0, logbook.InvalidFieldError{Field: "cityId",
				Why: "a photograph that is not in this log yet has no city to leave alone"}
		case w.TakenAt == nil:
			return logbook.Photo{}, 0, logbook.InvalidFieldError{Field: "takenAt",
				Why: "a photograph that is not in this log yet has no moment to leave alone"}
		case w.Asset == nil:
			return logbook.Photo{}, 0, logbook.InvalidFieldError{Field: "asset",
				Why: "a photograph that is not in this log yet has no asset to leave alone"}
		}
	}
	if w.TripID != nil {
		next.TripID = *w.TripID
	}
	if w.CityID != nil {
		next.CityID = *w.CityID
	}
	if w.TakenAt != nil {
		next.TakenAt = *w.TakenAt
	}
	if w.Asset != nil {
		next.Asset = *w.Asset
	}
	if logbook.Sent(w.Caption) {
		next.Caption = logbook.StoredCaption(w.Caption)
	}
	if logbook.Sent(w.Coordinates) {
		next.Coordinates = logbook.Value(w.Coordinates)
	}
	if logbook.Sent(w.AccuracyMetres) {
		next.AccuracyMetres = logbook.Value(w.AccuracyMetres)
	}
	if logbook.Sent(w.FiledLater) {
		next.FiledLater = logbook.Value(w.FiledLater)
	}
	// THE PAIR IS NOT WRITABLE FROM HERE AT ALL, which mirrors the statement:
	// `upsertPhotoSQL` names neither column, and `PhotoWrite` has no slot.

	if found < 0 {
		f.books.doc.Photos = append(f.books.doc.Photos, next)
	} else {
		f.books.doc.Photos[found] = next
	}
	f.books.version++
	return next, f.books.version, nil
}

func (f *fakeMoments) DeletePhoto(_ context.Context, _, photoID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return 0, f.failWith
	}
	kept := make([]logbook.Photo, 0, len(f.books.doc.Photos))
	for _, photo := range f.books.doc.Photos {
		if photo.ID != photoID {
			kept = append(kept, photo)
		}
	}
	if len(kept) == len(f.books.doc.Photos) {
		// AN UNKNOWN ID MOVES NO VERSION, which is the real store's contract
		// and is what stops a retried delete throwing away the phone's whole
		// cached document.
		return f.books.version, nil
	}
	f.books.doc.Photos = kept
	f.books.version++
	return f.books.version, nil
}

func (f *fakeMoments) SnoozePhotos(_ context.Context, _ string, w logbook.SnoozeWrite) ([]logbook.Photo, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snoozes++
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	wanted := map[string]bool{}
	for _, id := range *w.PhotoIDs {
		wanted[id] = true
	}
	moved := []logbook.Photo{}
	for i, photo := range f.books.doc.Photos {
		if !wanted[photo.ID] {
			continue
		}
		f.books.doc.Photos[i].FiledLater = w.Until
		moved = append(moved, f.books.doc.Photos[i])
	}
	if len(moved) == 0 {
		return []logbook.Photo{}, f.books.version, nil
	}
	sort.Slice(moved, func(a, b int) bool { return moved[a].ID < moved[b].ID })
	f.books.version++
	return moved, f.books.version, nil
}

func (f *fakeMoments) RefilePhoto(_ context.Context, _, photoID string, w logbook.RefileWrite) (logbook.PhotoRefiled, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.PhotoRefiled{}, f.failWith
	}
	found := -1
	for i, photo := range f.books.doc.Photos {
		if photo.ID == photoID {
			found = i
		}
	}
	if found < 0 {
		return logbook.PhotoRefiled{}, logbook.ErrNoPhoto
	}
	// BOTH COLUMNS OR NEITHER. The twin cannot express the half-filed state,
	// for the reason the statement writes them in one UPDATE.
	f.books.doc.Photos[found].PlaceID = w.PlaceID
	f.books.doc.Photos[found].VisitID = w.VisitID
	f.books.version++

	out := logbook.PhotoRefiled{Photo: f.books.doc.Photos[found], Version: f.books.version}
	if f.mintOnRefile {
		doc := f.books.doc
		out.Document = &doc
	}
	return out, nil
}

func (f *fakeMoments) PutWalk(_ context.Context, _ string, w logbook.WalkWrite) (logbook.Walk, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Walk{}, 0, f.failWith
	}
	f.lastWalkWrite = w

	id := ""
	if w.ID != nil {
		id = *w.ID
	}
	next := logbook.Walk{ID: id}
	found := -1
	for i, existing := range f.books.doc.Walks {
		if existing.ID == id {
			next, found = existing, i
		}
	}
	if found < 0 && (w.TripID == nil || w.CityID == nil || w.RecordedOn == nil ||
		w.DistanceKm == nil || w.Points == nil) {
		return logbook.Walk{}, 0, logbook.InvalidFieldError{Field: "points",
			Why: "a walk that is not in this log yet has no track to leave alone"}
	}
	if w.TripID != nil {
		next.TripID = *w.TripID
	}
	if w.CityID != nil {
		next.CityID = *w.CityID
	}
	if w.RecordedOn != nil {
		next.RecordedOn = *w.RecordedOn
	}
	if w.DistanceKm != nil {
		next.DistanceKm = *w.DistanceKm
	}
	// ABSENT MEANS LEAVE ALONE, AND ON THIS FIELD IT IS THE WHOLE STEP.
	if w.Points != nil {
		next.Points = *w.Points
	}
	if logbook.Sent(w.Name) {
		next.Name = logbook.Value(w.Name)
	}
	if w.Dismissed != nil {
		next.Dismissed = *w.Dismissed
	}

	if found < 0 {
		f.books.doc.Walks = append(f.books.doc.Walks, next)
	} else {
		f.books.doc.Walks[found] = next
	}
	f.books.version++
	return next, f.books.version, nil
}

// === the legs ===

func aFiledPhotograph() logbook.Photo {
	place, visit := "bukchon", "v-bukchon-0"
	caption := "Last morning, and the lanes were empty for once"
	return logbook.Photo{
		ID: "ph-0", TripID: "autumn-crossing", CityID: "seoul",
		TakenAt: logbook.At(time.Date(2027, time.September, 30, 12, 0, 0, 0, time.UTC)),
		Asset:   strings.Repeat("a", 64),
		PlaceID: &place, VisitID: &visit, Caption: &caption,
	}
}

func aWalk() logbook.Walk {
	return logbook.Walk{
		ID: "w-busan", TripID: "autumn-crossing", CityID: "busan",
		RecordedOn: logbook.At(time.Date(2027, time.September, 28, 0, 0, 0, 0, time.UTC)),
		DistanceKm: 6.4,
		Points: []logbook.LatLng{
			{Lat: 35.0975, Lng: 129.0104}, {Lat: 35.0981, Lng: 129.0117},
			{Lat: 35.0996, Lng: 129.0129},
		},
	}
}

func (h *harness) moments() *fakeMoments { return h.deps.Photos.(*fakeMoments) }

// withMoments gives the harness a photograph and a walk to write to.
func withMoments(t *testing.T) (*harness, *fakeMoments, string) {
	t.Helper()
	h := newHarness(t, options{})
	moments := h.moments()
	moments.books.doc.Photos = []logbook.Photo{aFiledPhotograph()}
	moments.books.doc.Walks = []logbook.Walk{aWalk()}
	return h, moments, bearer(t, h)
}

// M2's NOTE ARRIVES AT THE STORE AS A CAPTION AND NOTHING ELSE.
//
// THE ASSERTION IS ON WHAT THE HANDLER HANDED THE STORE, not on the response,
// because that is where the difference is visible. A body of `{caption}` that
// reached the store carrying a zero `tripId` or a sent-but-empty coordinate
// would answer exactly the same 200.
func TestACaptionOnlyPutReachesTheStoreCarryingOnlyACaption(t *testing.T) {
	h, moments, token := withMoments(t)

	got := h.do(t, http.MethodPut, "/v1/photos/ph-0", `{"caption":"a new note"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT /v1/photos/ph-0 = %d %s", got.status, got.body)
	}

	write := moments.lastPhotoWrite
	for _, absent := range []struct {
		name string
		sent bool
	}{
		{"tripId", write.TripID != nil},
		{"cityId", write.CityID != nil},
		{"takenAt", write.TakenAt != nil},
		{"asset", write.Asset != nil},
		{"coordinates", logbook.Sent(write.Coordinates)},
		{"accuracyMetres", logbook.Sent(write.AccuracyMetres)},
		{"filedLater", logbook.Sent(write.FiledLater)},
	} {
		if absent.sent {
			t.Errorf("the store was told to write %s on a body that never carried it. "+
				"Absent means LEAVE ALONE (DEC-89)", absent.name)
		}
	}
	if !logbook.Sent(write.Caption) {
		t.Error("the caption did not reach the store at all")
	}

	// AND THE ANSWER STILL CARRIES THE FILING, which is what the phone
	// splices. A response assembled from the request could not: `PhotoWrite`
	// has no slot for a place.
	var answered logbook.Photo
	if err := json.Unmarshal(got.body, &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.PlaceID == nil || *answered.PlaceID != "bukchon" {
		t.Errorf("the answer carries placeId %v, want bukchon", answered.PlaceID)
	}
	if answered.VisitID == nil || *answered.VisitID != "v-bukchon-0" {
		t.Errorf("the answer carries visitId %v, want v-bukchon-0", answered.VisitID)
	}
	if got.header.Get("ETag") == "" {
		t.Error("the write carries no ETag, so the phone cannot stamp its cache")
	}
}

// AND THE ANSWER TO A PHOTO WRITE CARRIES NO LIST KEY AT ALL, WHICH IS WHY
// THERE IS NO EmitPhoto.
//
// MEASURED ON THE WIRE rather than on a Go value: the assertion walks the
// emitted keys and reddens the day `Photo` grows a list. `internal/logbook`
// holds the same claim about the struct; this one holds it about the bytes
// this route actually writes.
func TestAPhotoAnswerCarriesNoListKeyAndThereforeNeedsNoEmitter(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPut, "/v1/photos/ph-0", `{"caption":"a new note"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &keys); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("the answer has no keys at all")
	}
	for key, value := range keys {
		if len(value) > 0 && value[0] == '[' {
			t.Errorf("the answer's %q is a list, so this route now needs an EmitPhoto: "+
				"a nil slice marshals to `null` and the client reads every list key "+
				"in this document as a non-nullable List", key)
		}
	}
}

// D1 ANSWERS 204 WITH AN ETag AND NO BODY, AND AN UNKNOWN ID ANSWERS 204 TOO.
//
// THE ETag IS WHAT THE 204 IS FOR rather than a bare success: the phone has
// just spliced a deletion into a document it caches under a version, and
// without the new tag its next conditional GET either refetches the whole log
// or keeps serving a body that still holds the photograph.
//
// THE UNKNOWN-ID HALF IS THE CLIENT'S OWN ASYMMETRY. `deletePhoto` answers
// true for an id the log does not hold — the caller asked for it to be absent
// and it is — while `setPhotoCaption` and `refilePhoto` answer false.
func TestDeletingAPhotographAnswers204WithAnETagAndAnUnknownIdDoesToo(t *testing.T) {
	h, moments, token := withMoments(t)

	got := h.do(t, http.MethodDelete, "/v1/photos/ph-0", "", token)
	if got.status != http.StatusNoContent {
		t.Fatalf("DELETE /v1/photos/ph-0 = %d %s, want 204", got.status, got.body)
	}
	if len(got.body) != 0 {
		t.Errorf("a 204 carried a body: %q", got.body)
	}
	firstTag := got.header.Get("ETag")
	if firstTag == "" {
		t.Error("the delete carries no ETag, so the phone cannot stamp its cache")
	}
	if len(moments.books.doc.Photos) != 0 {
		t.Errorf("%d photographs survive", len(moments.books.doc.Photos))
	}

	// THE REPEAT IS A SUCCESS AND MOVES NOTHING, which is what stops a retried
	// delete taking the client's failure branch (DEC-103) — and what stops it
	// throwing away the phone's whole cached document.
	again := h.do(t, http.MethodDelete, "/v1/photos/ph-0", "", token)
	if again.status != http.StatusNoContent {
		t.Errorf("the SECOND DELETE = %d, want 204", again.status)
	}
	if again.header.Get("ETag") != firstTag {
		t.Errorf("the ETag moved %q -> %q on a delete that deleted nothing",
			firstTag, again.header.Get("ETag"))
	}
}

// N1's 'LATER' ANSWERS THE ROWS IT WROTE, AND AN EMPTY MATCH ANSWERS `[]`
// RATHER THAN `null`.
//
// `null` IS THE ONE SHAPE THE CLIENT THROWS ON, and a bulk route is where a
// nil slice is most likely: the ordinary case for an empty match is a group
// whose photographs have all been filed since the row was drawn.
func TestASnoozeAnswersTheRowsItWroteAndAnEmptyMatchAnswersAnEmptyList(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPost, "/v1/photos/snooze",
		`{"photoIds":["ph-0","gone-1"],"until":"2027-10-19T00:00:00.000Z"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("POST /v1/photos/snooze = %d %s", got.status, got.body)
	}
	var body struct {
		Photos []logbook.Photo `json:"photos"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if len(body.Photos) != 1 || body.Photos[0].ID != "ph-0" {
		t.Errorf("the answer carries %d photographs, want the one that was there — an "+
			"id the log does not hold is SKIPPED and not fatal", len(body.Photos))
	}

	empty := h.do(t, http.MethodPost, "/v1/photos/snooze",
		`{"photoIds":["gone-1","gone-2"],"until":"2027-10-19T00:00:00.000Z"}`, token)
	if empty.status != http.StatusOK {
		t.Fatalf("a snooze matching nothing = %d %s, want 200 — the client's own method "+
			"returns false without writing, which is a request that was MET by doing "+
			"nothing", empty.status, empty.body)
	}
	if !strings.Contains(string(empty.body), `"photos":[]`) {
		t.Errorf("an empty match answered %s, want `\"photos\":[]`. `null` is the one "+
			"shape the client's List cast throws on", empty.body)
	}
}

// AND A SNOOZE WITH NO `photoIds` IS A 422 NAMING THE FIELD, WITHOUT REACHING
// THE STORE.
//
// An absent group and an empty one are different requests: the first never
// named a group, and the second is a group that turned out to be empty.
// Collapsing them would make a malformed request look like an ordinary one.
func TestASnoozeWithNoGroupIsRefusedByNameAndNeverReachesTheStore(t *testing.T) {
	h, moments, token := withMoments(t)

	got := h.do(t, http.MethodPost, "/v1/photos/snooze",
		`{"until":"2027-10-19T00:00:00.000Z"}`, token)
	if got.status != 422 {
		t.Fatalf("a snooze with no photoIds = %d %s, want 422", got.status, got.body)
	}
	if field := fieldOfBody(t, got.body); field != "photoIds" {
		t.Errorf("the refusal names %q, want \"photoIds\"", field)
	}
	if moments.snoozes != 0 {
		t.Errorf("the store was reached %d times by a body that named no group", moments.snoozes)
	}

	noDate := h.do(t, http.MethodPost, "/v1/photos/snooze", `{"photoIds":["ph-0"]}`, token)
	if field := fieldOfBody(t, noDate.body); noDate.status != 422 || field != "until" {
		t.Errorf("a snooze with no date = %d naming %q, want 422 naming \"until\"",
			noDate.status, field)
	}
}

// M2.2's RE-FILE ANSWERS TWO SHAPES AND THE SHAPE IS READ OFF WHAT MOVED.
//
// An occasion that ALREADY EXISTED moved one entity, so DEC-32's bare
// photograph is the splice. A MINTED one moved the place as well — and
// renumbered every one of that place's ordinals — so the phone cannot splice
// what it was not sent, and the answer is the whole envelope. That is `PUT
// /v1/cities/{id}`'s own device.
func TestARefileAnswersAPhotographOrTheWholeEnvelopeDependingOnWhatMoved(t *testing.T) {
	h, moments, token := withMoments(t)

	got := h.do(t, http.MethodPost, "/v1/photos/ph-0/refile",
		`{"placeId":"gamcheon","visitId":"v-gamcheon-0"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("POST /v1/photos/ph-0/refile = %d %s", got.status, got.body)
	}
	if strings.Contains(string(got.body), `"logbook"`) {
		t.Errorf("a re-file to an EXISTING occasion answered the whole envelope: %s. "+
			"One entity moved and the phone splices it", got.body)
	}
	var answered logbook.Photo
	if err := json.Unmarshal(got.body, &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.PlaceID == nil || *answered.PlaceID != "gamcheon" ||
		answered.VisitID == nil || *answered.VisitID != "v-gamcheon-0" {
		t.Errorf("the answer is filed at %v/%v, want gamcheon/v-gamcheon-0",
			answered.PlaceID, answered.VisitID)
	}

	moments.mintOnRefile = true
	minted := h.do(t, http.MethodPost, "/v1/photos/ph-0/refile",
		`{"placeId":"gamcheon","visitId":"v-fresh","visitAt":"2027-09-28T09:00:00.000Z"}`, token)
	if minted.status != http.StatusOK {
		t.Fatalf("a re-file that mints = %d %s", minted.status, minted.body)
	}
	if !strings.Contains(string(minted.body), `"version":2`) {
		t.Errorf("a re-file that MINTED an occasion answered %s — the place gained a "+
			"visit and every one of its ordinals was rewritten, so the answer is the "+
			"whole envelope", minted.body)
	}
}

// AND A RE-FILE THAT NAMES NO OCCASION IS A 422 NAMING `visitId`.
//
// The refusal is `logbook.Service`'s and the leg here is that it REACHES THE
// WIRE as the 422 that says which field — through the same mapping every other
// refusal in this API goes through. The proof that the store was not reached
// is in internal/logbook, where the counting twin lives.
func TestARefileThatNamesNoOccasionIsA422NamingVisitId(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPost, "/v1/photos/ph-0/refile", `{"placeId":"gamcheon"}`, token)
	if got.status != 422 {
		t.Fatalf("a re-file with no visitId = %d %s, want 422", got.status, got.body)
	}
	if field := fieldOfBody(t, got.body); field != "visitId" {
		t.Errorf("the refusal names %q, want \"visitId\" — a place can be visited more "+
			"than once on one trip, so a server picking for itself files the "+
			"photograph to whichever row the planner returned", field)
	}
}

// A PHOTOGRAPH THIS LOG DOES NOT HOLD IS A 404 ON A RE-FILE AND A 204 ON A
// DELETE.
//
// The client's own asymmetry, and it is asserted as a PAIR because that is
// what makes it a decision rather than two accidents.
func TestAnUnknownPhotographIs404OnARefileAnd204OnADelete(t *testing.T) {
	h, _, token := withMoments(t)

	refile := h.do(t, http.MethodPost, "/v1/photos/no-such/refile",
		`{"placeId":"gamcheon","visitId":"v-gamcheon-0"}`, token)
	if refile.status != http.StatusNotFound {
		t.Errorf("re-filing an absent photograph = %d, want 404 — a set asks for a "+
			"value the log then has to hold", refile.status)
	}
	if deleted := h.do(t, http.MethodDelete, "/v1/photos/no-such", "", token); deleted.status != http.StatusNoContent {
		t.Errorf("deleting an absent photograph = %d, want 204 — the caller asked for "+
			"it to be absent and it is", deleted.status)
	}
}

// === the walk route ===

// A WALK ANSWER CARRIES `"points": [...]` AND NEVER `null` (CF-BLO-3, PD-15).
//
// MEASURED AGAINST THE WIRE rather than against a Go value. `jq -c` renders a
// JSON null as the four characters `null`, and so does this: the substring
// check tells `[]` from `null`, which is the whole distinction and the one the
// client throws on.
//
// THE BODY IS N1's DISCARD, which carries no track at all — so a handler
// answering the request rather than the row would produce exactly the null
// this leg refuses.
func TestAWalkAnswerCarriesItsPointsAndNeverNull(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPut, "/v1/walks/w-busan", `{"dismissed":true}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT /v1/walks/w-busan = %d %s", got.status, got.body)
	}
	if strings.Contains(string(got.body), `"points":null`) {
		t.Fatalf("the answer carries `\"points\":null`: %s. `photo.g.dart:47-49` reads "+
			"it as `(json['points'] as List<dynamic>)` with no null branch, so the "+
			"app throws on the answer to its own write", got.body)
	}
	var answered logbook.Walk
	if err := json.Unmarshal(got.body, &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if len(answered.Points) != 3 {
		t.Errorf("the answer carries %d points, want the 3 that were recorded — N1's "+
			"Discard sets a flag and keeps the track", len(answered.Points))
	}
	if !answered.Dismissed {
		t.Error("dismissed = false after N1's Discard")
	}

	// AND THE STORE WAS TOLD NOTHING ABOUT THE TRACK, which is the half of it
	// the response cannot show.
	if h.moments().lastWalkWrite.Points != nil {
		t.Errorf("the store was handed a track of %d points on a `{dismissed:true}` "+
			"body. Absent means LEAVE ALONE, and a List<LatLng> recorded on a day "+
			"that has passed cannot be re-recorded", len(*h.moments().lastWalkWrite.Points))
	}
}

// AND AN EMPTY TRACK IS REFUSED BY NAME, WHERE `walks_points_array_ck` WOULD
// LET IT THROUGH.
//
// An empty array IS an array, so the 0001 constraint does not see it. 0003's
// `walks_points_present_ck` is the guarantee and this 422 is what names the
// field — DEC-58's precedent.
func TestAnEmptyTrackIsRefusedNamingPoints(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPut, "/v1/walks/w-busan", `{"points":[]}`, token)
	if got.status != 422 {
		t.Fatalf("points: [] = %d %s, want 422", got.status, got.body)
	}
	if field := fieldOfBody(t, got.body); field != "points" {
		t.Errorf("the refusal names %q, want \"points\"", field)
	}

	// THE OTHER HALF: a real track is not refused. Without it this leg passes
	// against a build that refuses every walk write.
	fine := h.do(t, http.MethodPut, "/v1/walks/w-busan",
		`{"points":[{"lat":35.0975,"lng":129.0104}]}`, token)
	if fine.status != http.StatusOK {
		t.Errorf("a one-point track = %d %s, want 200", fine.status, fine.body)
	}
}

// AND AN OVER-LONG TRACK IS REFUSED BY NAME AND NOT BY http.MaxBytesReader.
//
// THAT IS THE WHOLE OF DEC-93's SECOND SENTENCE. `ErrBodyTooLarge` carries no
// field at all, so a client whose walk is too long would be told "your request
// is too big" about a body it cannot see the shape of — and a user whose walk
// is refused has lost a recording of a day.
//
// THE BODY IS DELIBERATELY WELL INSIDE `httpx.MaxBodyBytes`, or the leg would
// be measuring the reader rather than the validator: 501 points at seven
// decimal places is about 21 KB against a 1 MiB ceiling.
func TestAnOverLongTrackIsRefusedNamingPointsAndNotAsABodyTooLarge(t *testing.T) {
	h, _, token := withMoments(t)

	var track strings.Builder
	track.WriteString(`{"points":[`)
	for i := range logbook.MaxWalkPoints + 1 {
		if i > 0 {
			track.WriteByte(',')
		}
		track.WriteString(`{"lat":35.0975123,"lng":129.0104567}`)
	}
	track.WriteString(`]}`)
	if int64(track.Len()) >= 1<<20 {
		t.Fatalf("the body is %d bytes and the reader's ceiling is 1 MiB, so this leg "+
			"would be measuring MaxBytesReader rather than the validator", track.Len())
	}

	got := h.do(t, http.MethodPut, "/v1/walks/w-busan", track.String(), token)
	if got.status != 422 {
		t.Fatalf("%d points = %d %s, want 422", logbook.MaxWalkPoints+1, got.status, got.body)
	}
	if field := fieldOfBody(t, got.body); field != "points" {
		t.Errorf("the refusal names %q, want \"points\" — `ErrBodyTooLarge` has no field "+
			"on it, which is why DEC-93 puts the cap in ValidateWalk", field)
	}
}

// fieldOfBody reads DEC-12's one additive key off an error body, and answers
// "" when there is none — so a leg comparing against a field name reddens on a
// 500 as well as on the wrong field.
func fieldOfBody(t *testing.T, body []byte) string {
	t.Helper()
	var decoded struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	return decoded.Field
}
