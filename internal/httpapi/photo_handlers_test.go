// R7's five routes over the real mux, the real middleware chain and the real
// auth, with fake stores.
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

// fakeMoments satisfies logbook.PhotoStore and logbook.WalkStore over the
// same little document the trip fake writes to.
type fakeMoments struct {
	mu       sync.Mutex
	books    *fakeLogbook
	failWith error

	lastPhotoWrite logbook.PhotoWrite
	lastWalkWrite  logbook.WalkWrite

	snoozes int

	mintOnRefile bool

	answerWithoutPoints bool
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
	if f.answerWithoutPoints {
		next.Points = nil
	}
	return next, f.books.version, nil
}

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

// M2's note arrives at the store as A caption and nothing else.
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

// The answer to A photo write carries no list key at all, which is why
// there is no EmitPhoto.
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

// D1 ANSWERS 204 with an ETag and no body, and an unknown id answers 204 TOO.
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

	again := h.do(t, http.MethodDelete, "/v1/photos/ph-0", "", token)
	if again.status != http.StatusNoContent {
		t.Errorf("the SECOND DELETE = %d, want 204", again.status)
	}
	if again.header.Get("ETag") != firstTag {
		t.Errorf("the ETag moved %q -> %q on a delete that deleted nothing",
			firstTag, again.header.Get("ETag"))
	}
}

// N1's 'LATER' answers the rows it wrote, and an empty match answers `[]`
// Than `null`.
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

// A snooze with no `photoIds` is a 422 naming the field, without reaching
// the store.
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

// M2.2's RE-file answers two shapes and the shape is read off what moved.
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

// a re-file that names no occasion is a 422 NAMING `visitId`.
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

// A photograph this log does not hold is a 404 ON A RE-FILE and A 204 ON A
// DELETE.
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

// A walk answer carries `"points": [...]` and never `null`.
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

	if h.moments().lastWalkWrite.Points != nil {
		t.Errorf("the store was handed a track of %d points on a `{dismissed:true}` "+
			"body. Absent means LEAVE ALONE, and a List<LatLng> recorded on a day "+
			"that has passed cannot be re-recorded", len(*h.moments().lastWalkWrite.Points))
	}

	h.moments().answerWithoutPoints = true
	unread := h.do(t, http.MethodPut, "/v1/walks/w-busan", `{"dismissed":true}`, token)
	if unread.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", unread.status, unread.body)
	}
	if !strings.Contains(string(unread.body), `"points":[]`) {
		t.Errorf("a Walk with a nil track reached the wire as %s. `EmitWalk` normalises "+
			"it to `[]`; without it the key is `null`, which is the one shape "+
			"`(json['points'] as List<dynamic>)` throws on", unread.body)
	}
}

// An empty track is refused by name, where `walks_points_array_ck` would
// let it through.
func TestAnEmptyTrackIsRefusedNamingPoints(t *testing.T) {
	h, _, token := withMoments(t)

	got := h.do(t, http.MethodPut, "/v1/walks/w-busan", `{"points":[]}`, token)
	if got.status != 422 {
		t.Fatalf("points: [] = %d %s, want 422", got.status, got.body)
	}
	if field := fieldOfBody(t, got.body); field != "points" {
		t.Errorf("the refusal names %q, want \"points\"", field)
	}

	fine := h.do(t, http.MethodPut, "/v1/walks/w-busan",
		`{"points":[{"lat":35.0975,"lng":129.0104}]}`, token)
	if fine.status != http.StatusOK {
		t.Errorf("a one-point track = %d %s, want 200", fine.status, fine.body)
	}
}

// An over-long track is refused by name and not by http.MaxBytesReader.
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

// fieldOfBody reads the one additive key off an error body.
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
