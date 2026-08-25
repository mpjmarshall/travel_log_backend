// The three R6 routes over the real mux, the real middleware chain and the
// real auth, with fake stores. Test-first.
//
// WHAT IS HERE AND WHAT IS DELIBERATELY NOT. These legs are about what leaves
// the process: which shape a city write answers, that a bare Place never
// reaches the wire carrying `"visits":null`, that `visits: []` is refused by
// name, and that `?photos` has no default. What only a real PostgreSQL can say
// — that a no-op re-send does not unfile a photograph, that D2's two
// statements are in the order the sheet promises, that a count does not fall —
// is in internal/postgres and internal/seed and is NOT repeated here.
//
// R5 WROTE THAT RULE DOWN AFTER PAYING FOR IT: "a leg over a twin cannot guard
// a statement the twin does not execute". So the fakes below are honest little
// stores — they honour DEC-89's pointer contract and D2's two branches — and
// nothing in this file is offered as evidence about a statement.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"travellog/internal/logbook"
)

// === the fakes ===

// fakeGeography satisfies logbook.CityStore AND logbook.PlaceStore over the
// same little document the trip fake writes to, so a city created here is a
// city a later place write can name.
//
// IT HONOURS DEC-89 BECAUSE IT HAS TO. A fake that applied every field
// regardless would make every handler leg green against the contract this
// whole step is about — the same reason fakeLogbook.PutTrip carries its own
// `leave alone` branch.
type fakeGeography struct {
	mu       sync.Mutex
	books    *fakeLogbook
	failWith error

	// lastPlaceWrite is what the handler handed the store, so a leg can assert
	// that an OMITTED key arrived as nil rather than as an empty slice — which
	// is the difference the whole step turns on and is invisible in the
	// response.
	lastPlaceWrite logbook.PlaceWrite

	// removals counts D2's calls and records the branch, so a leg can prove
	// the store was not reached at all when the parameter was missing.
	removals []logbook.PhotoDisposition
}

func (f *fakeGeography) PutCity(_ context.Context, _ string, w logbook.CityWrite) (logbook.CityWritten, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.CityWritten{}, f.failWith
	}
	id := ""
	if w.ID != nil {
		id = *w.ID
	}

	next := logbook.City{ID: id}
	found := -1
	for i, existing := range f.books.doc.Cities {
		if existing.ID == id {
			next, found = existing, i
		}
	}
	if found < 0 {
		// The store's own create-time refusals, so a leg about them is a leg
		// about the same rule the real store enforces under the lock.
		switch {
		case w.Name == nil:
			return logbook.CityWritten{}, logbook.InvalidFieldError{Field: "name",
				Why: "a city that is not in this log yet has no name to leave alone"}
		case w.Country == nil:
			return logbook.CityWritten{}, logbook.InvalidFieldError{Field: "country",
				Why: "a city that is not in this log yet has no country to leave alone"}
		case w.Centre == nil:
			return logbook.CityWritten{}, logbook.InvalidFieldError{Field: "centre",
				Why: "a city that is not in this log yet has no centre to leave alone"}
		}
	}
	if w.Name != nil {
		next.Name = *w.Name
	}
	if w.Country != nil {
		next.Country = *w.Country
	}
	if w.Centre != nil {
		next.Centre = *w.Centre
	}
	if logbook.Sent(w.CoverAsset) {
		next.CoverAsset = logbook.Value(w.CoverAsset)
	}
	if found < 0 {
		f.books.doc.Cities = append(f.books.doc.Cities, next)
	} else {
		f.books.doc.Cities[found] = next
	}

	f.books.version++
	out := logbook.CityWritten{City: next, Version: f.books.version}

	if w.AttachTo != nil {
		attached := false
		for i, trip := range f.books.doc.Trips {
			if trip.ID != *w.AttachTo {
				continue
			}
			attached = true
			for _, held := range trip.CityIDs {
				if held == id {
					attached = true
					goto done
				}
			}
			f.books.doc.Trips[i].CityIDs = append(trip.CityIDs, id)
		done:
		}
		if !attached {
			return logbook.CityWritten{}, logbook.InvalidFieldError{Field: "attachTo",
				Why: "that is not a trip in this log"}
		}
		doc := f.books.doc
		out.Document = &doc
	}
	return out, nil
}

func (f *fakeGeography) PutPlace(_ context.Context, _ string, w logbook.PlaceWrite) (logbook.Place, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Place{}, 0, f.failWith
	}
	f.lastPlaceWrite = w

	id := ""
	if w.ID != nil {
		id = *w.ID
	}
	next := logbook.Place{ID: id}
	found := -1
	for i, existing := range f.books.doc.Places {
		if existing.ID == id {
			next, found = existing, i
		}
	}
	if found < 0 {
		switch {
		case w.CityID == nil:
			return logbook.Place{}, 0, logbook.InvalidFieldError{Field: "cityId",
				Why: "a place belongs to a city"}
		case w.Name == nil:
			return logbook.Place{}, 0, logbook.InvalidFieldError{Field: "name",
				Why: "a place that is not in this log yet has no name to leave alone"}
		case w.Coordinates == nil:
			return logbook.Place{}, 0, logbook.InvalidFieldError{Field: "coordinates",
				Why: "a place that is not in this log yet has no coordinates to leave alone"}
		}
	}
	if w.CityID != nil {
		next.CityID = *w.CityID
	}
	if w.Name != nil {
		next.Name = *w.Name
	}
	if w.Coordinates != nil {
		next.Coordinates = *w.Coordinates
	}
	if logbook.Sent(w.Plan) {
		next.Plan = logbook.Value(w.Plan)
	}
	if logbook.Sent(w.CoverAsset) {
		next.CoverAsset = logbook.Value(w.CoverAsset)
	}
	// DEC-89, IN THE FAKE. An absent Visits leaves the stored list exactly
	// where it is — including leaving it NIL on a create, which is what makes
	// the EmitPlace leg below able to fail.
	if w.Visits != nil {
		next.Visits = *w.Visits
	}

	if found < 0 {
		f.books.doc.Places = append(f.books.doc.Places, next)
	} else {
		f.books.doc.Places[found] = next
	}
	f.books.version++
	return next, f.books.version, nil
}

func (f *fakeGeography) RemovePlace(_ context.Context, _, placeID string, deletePhotos bool) (logbook.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return logbook.Snapshot{}, f.failWith
	}
	branch := logbook.KeepPhotos
	if deletePhotos {
		branch = logbook.DeletePhotos
	}
	f.removals = append(f.removals, branch)

	kept := f.books.doc.Places[:0:0]
	gone := false
	for _, place := range f.books.doc.Places {
		if place.ID == placeID {
			gone = true
			continue
		}
		kept = append(kept, place)
	}
	if !gone {
		// A miss is a success and moves no version — the store's contract, and
		// a fake that bumped anyway would make the ETag leg green against a
		// store that did.
		doc := f.books.doc
		return logbook.Snapshot{Version: f.books.version, Document: &doc}, nil
	}
	f.books.doc.Places = kept

	photos := f.books.doc.Photos[:0:0]
	for _, photo := range f.books.doc.Photos {
		if photo.PlaceID == nil || *photo.PlaceID != placeID {
			photos = append(photos, photo)
			continue
		}
		if deletePhotos {
			continue
		}
		// D2's keep branch clears BOTH columns, which is
		// `Photo.copyWith(clearPlace: true)`, and leaves everything else.
		photo.PlaceID, photo.VisitID = nil, nil
		photos = append(photos, photo)
	}
	f.books.doc.Photos = photos

	f.books.version++
	doc := f.books.doc
	return logbook.Snapshot{Version: f.books.version, Document: &doc}, nil
}

// === helpers ===

func geographyHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, options{})
	return h, bearer(t, h)
}

func (h *harness) geography() *fakeGeography {
	return h.deps.Cities.(*fakeGeography)
}

const aKyoto = `{"name":"Kyoto","country":{"code":"JP","name":"Japan"},` +
	`"centre":{"lat":35.0116,"lng":135.7681}}`

// === T5's CITY ===

// THE TWO SHAPES ARE THE ROUTE'S WHOLE ASYMMETRY, and each half of this leg
// fails on its own: a route that always answered the city would leave the
// phone unable to see the itinerary it just changed, and one that always
// answered the envelope would send the whole log back on a plain rename.
func TestCreatingACityAnswersTheCityAndAttachingOneAnswersTheWholeLog(t *testing.T) {
	h, token := geographyHarness(t)

	bare := h.put(t, "/v1/cities/kyoto", aKyoto, token)
	if bare.status != http.StatusOK {
		t.Fatalf("PUT /v1/cities/kyoto = %d %s", bare.status, bare.body)
	}
	body := bare.decode(t)
	if body["id"] != "kyoto" || body["name"] != "Kyoto" {
		t.Errorf("the answer is %v, want the bare city — a city that joins no trip has "+
			"moved one entity and DEC-32's splice is the right answer", body)
	}
	if _, isEnvelope := body["logbook"]; isEnvelope {
		t.Errorf("a city created with no attachTo answered an ENVELOPE. Nothing but the " +
			"city moved, and sending the whole log back on every rename is what the " +
			"conditional read exists to avoid")
	}
	if body["country"] == nil {
		t.Errorf("the answer has no country — DEC-59 flattens it into two columns and the " +
			"wire keeps the client's nested shape")
	}

	if got := h.put(t, "/v1/trips/autumn", `{"name":"Autumn crossing"}`, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/autumn = %d %s", got.status, got.body)
	}
	attached := h.put(t, "/v1/cities/busan",
		`{"name":"Busan","country":{"code":"KR","name":"South Korea"},`+
			`"centre":{"lat":35.1,"lng":129.0},"attachTo":"autumn"}`, token)
	if attached.status != http.StatusOK {
		t.Fatalf("PUT /v1/cities/busan with attachTo = %d %s", attached.status, attached.body)
	}
	envelope := attached.decode(t)
	log, isEnvelope := envelope["logbook"].(map[string]any)
	if !isEnvelope {
		t.Fatalf("a city attached to a trip answered %v, want the whole envelope — TWO "+
			"entities moved, and a phone handed only the city would have to re-derive "+
			"the itinerary from its own copy of the rule", envelope)
	}
	if envelope["version"] != float64(logbook.FormatVersion) {
		t.Errorf("version = %v, want %d", envelope["version"], logbook.FormatVersion)
	}
	if got := cityIDsOf(t, log, "autumn"); strings.Join(got, ",") != "busan" {
		t.Errorf("autumn's cityIds = %v, want [busan]", got)
	}
}

// THE NEW CITY GOES AT THE END OF THE ORDERED LIST, which is the client's own
// `t.withCities([...t.cityIds, id])` and is a fact about travel order rather
// than about sets: T1 and T4 draw the itinerary in the order it was walked.
func TestAnAttachedCityLandsAtTheEndOfTheItinerary(t *testing.T) {
	h, token := geographyHarness(t)
	if got := h.put(t, "/v1/trips/autumn", `{"name":"Autumn crossing"}`, token); got.status != http.StatusOK {
		t.Fatalf("PUT trip = %d %s", got.status, got.body)
	}
	for _, city := range []string{"kyoto", "osaka", "seoul"} {
		body := `{"name":"` + city + `","country":{"code":"JP","name":"Japan"},` +
			`"centre":{"lat":35,"lng":135},"attachTo":"autumn"}`
		if got := h.put(t, "/v1/cities/"+city, body, token); got.status != http.StatusOK {
			t.Fatalf("PUT /v1/cities/%s = %d %s", city, got.status, got.body)
		}
	}
	last := h.put(t, "/v1/cities/busan",
		`{"name":"Busan","country":{"code":"KR","name":"South Korea"},`+
			`"centre":{"lat":35.1,"lng":129},"attachTo":"autumn"}`, token)
	log := last.decode(t)["logbook"].(map[string]any)

	got := cityIDsOf(t, log, "autumn")
	if strings.Join(got, ",") != "kyoto,osaka,seoul,busan" {
		t.Errorf("autumn's cityIds = %v, want [kyoto osaka seoul busan]. The client appends "+
			"at the END — `t.withCities([...t.cityIds, id])` — and prepending would "+
			"reorder an itinerary nobody asked to reorder", got)
	}
}

func TestACityWriteWhoseBodyNamesAnotherCityIsRefused(t *testing.T) {
	h, token := geographyHarness(t)
	got := h.put(t, "/v1/cities/kyoto", `{"id":"osaka","name":"Osaka"}`, token)
	if got.status != http.StatusUnprocessableEntity || got.decode(t)["field"] != "id" {
		t.Errorf("PUT /v1/cities/kyoto with a body naming osaka = %d %s, want 422 on id — "+
			"a body naming a different city is a client that believes it is writing "+
			"somewhere else", got.status, got.body)
	}
}

func TestACityCreatedWithNoCountryOrCentreIsRefusedByName(t *testing.T) {
	h, token := geographyHarness(t)
	for _, tc := range []struct{ body, field string }{
		{`{"country":{"code":"JP","name":"Japan"},"centre":{"lat":35,"lng":135}}`, "name"},
		{`{"name":"Kyoto","centre":{"lat":35,"lng":135}}`, "country"},
		{`{"name":"Kyoto","country":{"code":"JP","name":"Japan"}}`, "centre"},
		{`{"name":"Kyoto","country":{"code":"JAPAN","name":"Japan"},"centre":{"lat":35,"lng":135}}`, "country"},
		{`{"name":"Kyoto","country":{"code":"JP","name":"Japan"},"centre":{"lat":95,"lng":135}}`, "centre"},
	} {
		got := h.put(t, "/v1/cities/kyoto", tc.body, token)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("PUT %s = %d %s, want 422", tc.body, got.status, got.body)
			continue
		}
		if field := got.decode(t)["field"]; field != tc.field {
			t.Errorf("PUT %s named %v, want %q", tc.body, field, tc.field)
		}
	}
}

// === C1's PIN ===

// THE EMIT LEG (CF-BLO-3, PD-15), AND IT OMITS THE KEY RATHER THAN SENDING AN
// EMPTY ARRAY.
//
// That distinction is the same one the request contract makes, arriving on the
// response: a body with `visits: []` is refused outright, so a leg that sent
// one could never reach the answer this is about. A wishlist place — C1's pin,
// the ONE control that drives this route — has a nil visits list all the way
// through, and a bare `Place` marshals it as `"visits":null`, which
// `place.g.dart:30-32` reads as `(json['visits'] as List<dynamic>)` with no
// null branch and throws on.
//
// IT ASSERTS ON THE BYTES AND NOT ON A DECODED MAP, because `null` and `[]`
// both decode to "the key is present" and only one of them is a list.
func TestAPlaceCreatedWithNoVisitsAnswersAnEmptyArrayAndNeverNull(t *testing.T) {
	h, token := geographyHarness(t)
	if got := h.put(t, "/v1/cities/kyoto", aKyoto, token); got.status != http.StatusOK {
		t.Fatalf("PUT city = %d %s", got.status, got.body)
	}

	got := h.put(t, "/v1/places/tofuku-ji",
		`{"cityId":"kyoto","name":"Tofuku-ji","coordinates":{"lat":34.97,"lng":135.77}}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT /v1/places/tofuku-ji = %d %s", got.status, got.body)
	}
	if strings.Contains(string(got.body), `"visits":null`) {
		t.Errorf("the answer carries \"visits\":null:\n    %s\n"+
			"    place.g.dart reads it as `(json['visits'] as List<dynamic>)` — "+
			"non-nullable, no null branch — so the app throws on the answer to its own "+
			"write. C1's pin is a wishlist place with no visits, so this is the "+
			"ORDINARY create rather than an edge case. EmitPlace is what normalises it.",
			got.body)
	}
	if !strings.Contains(string(got.body), `"visits":[]`) {
		t.Errorf("the answer carries no `\"visits\":[]`:\n    %s", got.body)
	}
}

// AND THE OMISSION REACHED THE STORE AS AN OMISSION (DEC-89, SAF-MAJ-4).
//
// The response cannot show this: a place with no visits and a place whose
// visits were just cleared both answer `"visits":[]`. What tells them apart is
// what the handler handed the store, and the difference is 30 photographs at
// fixture scale.
func TestAPlaceWriteWithNoVisitsKeyHandsTheStoreNilAndNotAnEmptySlice(t *testing.T) {
	h, token := geographyHarness(t)
	if got := h.put(t, "/v1/cities/kyoto", aKyoto, token); got.status != http.StatusOK {
		t.Fatalf("PUT city = %d %s", got.status, got.body)
	}
	if got := h.put(t, "/v1/places/tofuku-ji",
		`{"cityId":"kyoto","name":"Tofuku-ji","coordinates":{"lat":34.97,"lng":135.77}}`,
		token); got.status != http.StatusOK {
		t.Fatalf("PUT place = %d %s", got.status, got.body)
	}
	if visits := h.geography().lastPlaceWrite.Visits; visits != nil {
		t.Errorf("the store was handed a non-nil Visits (%v) for a body with no `visits` "+
			"key. Absent means LEAVE ALONE; an empty slice reaching the store is the "+
			"one input that deletes every occasion at the place", *visits)
	}
}

// `visits: []` IS REFUSED BY NAME, AND NOTHING IS TOUCHED (DEC-89, SAF-MAJ-4).
//
// The store must not be reached at all: this refusal is a fact about the
// request rather than about any row, so it happens before a transaction is
// opened and before the traveller's advisory lock is taken.
func TestAnEmptyVisitsArrayReachesTheStoreBecauseOnlyTheStoreCanJudgeIt(t *testing.T) {
	h, token := geographyHarness(t)
	if got := h.put(t, "/v1/cities/kyoto", aKyoto, token); got.status != http.StatusOK {
		t.Fatalf("PUT city = %d %s", got.status, got.body)
	}
	if got := h.put(t, "/v1/places/fushimi-inari",
		`{"cityId":"kyoto","name":"Fushimi Inari","coordinates":{"lat":34.96,"lng":135.77}}`,
		token); got.status != http.StatusOK {
		t.Fatalf("PUT place = %d %s", got.status, got.body)
	}
	h.geography().lastPlaceWrite = logbook.PlaceWrite{}

	// THIS LEG ASSERTED THE OPPOSITE AND THE ASSERTION WAS WRONG, not merely
	// obsolete. `visits: []` against a place with occasions is a destruction
	// and against a place with none it is a no-op, and the handler cannot tell
	// those apart — it has a body and no database. Stopping the request here
	// refused both, which refused every wishlist place in the client's log.
	got := h.put(t, "/v1/places/fushimi-inari", `{"visits":[]}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT with `visits: []` = %d %s, want 200 — the twin holds no occasions, "+
			"so there is nothing to clear and nothing to refuse", got.status, got.body)
	}
	w := h.geography().lastPlaceWrite
	if w.ID == nil {
		t.Fatal("the store was not reached. Only the store can count the occasions, so " +
			"a handler that answers this request by itself is answering it blind")
	}
	if w.Visits == nil {
		t.Error("the store was handed a nil Visits. `[]` and absent are DIFFERENT " +
			"requests — absent means leave alone, `[]` means clear — and collapsing " +
			"them here is exactly the confusion DEC-89 exists to remove")
	} else if len(*w.Visits) != 0 {
		t.Errorf("the store was handed %d visits, want 0", len(*w.Visits))
	}
}

// A REORDERED ARRAY COMES BACK IN THE NEW ORDER, and it is asserted on the
// RESPONSE because the response is what the phone splices.
func TestAVisitsArrayComesBackInTheOrderItWasSent(t *testing.T) {
	h, token := geographyHarness(t)
	if got := h.put(t, "/v1/cities/kyoto", aKyoto, token); got.status != http.StatusOK {
		t.Fatalf("PUT city = %d %s", got.status, got.body)
	}
	if got := h.put(t, "/v1/trips/kyoto-in-may", `{"name":"Kyoto in May"}`, token); got.status != http.StatusOK {
		t.Fatalf("PUT trip = %d %s", got.status, got.body)
	}

	const two = `{"cityId":"kyoto","name":"Fushimi Inari","coordinates":{"lat":34.96,"lng":135.77},
		"visits":[
			{"id":"v-new","placeId":"fushimi-inari","tripId":"kyoto-in-may","at":"2027-05-02T09:00:00.000Z","note":null},
			{"id":"v-old","placeId":"fushimi-inari","tripId":"kyoto-in-may","at":"2027-05-01T09:00:00.000Z","note":null}]}`
	if got := h.put(t, "/v1/places/fushimi-inari", two, token); got.status != http.StatusOK {
		t.Fatalf("PUT place = %d %s", got.status, got.body)
	}

	const swapped = `{"visits":[
			{"id":"v-old","placeId":"fushimi-inari","tripId":"kyoto-in-may","at":"2027-05-01T09:00:00.000Z","note":null},
			{"id":"v-new","placeId":"fushimi-inari","tripId":"kyoto-in-may","at":"2027-05-02T09:00:00.000Z","note":null}]}`
	got := h.put(t, "/v1/places/fushimi-inari", swapped, token)
	if got.status != http.StatusOK {
		t.Fatalf("the reorder = %d %s", got.status, got.body)
	}
	if ids := visitIDsOf(t, got.body); strings.Join(ids, ",") != "v-old,v-new" {
		t.Errorf("visits came back %v, want [v-old v-new]. The client reads "+
			"`visits.first.at` as lastVisited, so the order IS the meaning", ids)
	}
}

func TestAVisitsArrayRepeatingAnIdIsRefusedByName(t *testing.T) {
	h, token := geographyHarness(t)
	const twice = `{"cityId":"kyoto","name":"Fushimi Inari","coordinates":{"lat":34.96,"lng":135.77},
		"visits":[
			{"id":"v-1","placeId":"fushimi-inari","tripId":"t","at":"2027-05-02T09:00:00.000Z","note":null},
			{"id":"v-1","placeId":"fushimi-inari","tripId":"t","at":"2027-05-01T09:00:00.000Z","note":null}]}`
	got := h.put(t, "/v1/places/fushimi-inari", twice, token)
	if got.status != http.StatusUnprocessableEntity || got.decode(t)["field"] != "visits" {
		t.Errorf("a repeated visit id = %d %s, want 422 on visits — visits_pkey is "+
			"(traveller_id, id), so the multi-row upsert collides with itself and "+
			"PostgreSQL answers a sentence with no field on it", got.status, got.body)
	}
}

func TestAVisitNamingAnotherPlaceIsRefusedByName(t *testing.T) {
	h, token := geographyHarness(t)
	const elsewhere = `{"cityId":"kyoto","name":"Fushimi Inari","coordinates":{"lat":34.96,"lng":135.77},
		"visits":[{"id":"v-1","placeId":"tofuku-ji","tripId":"t","at":"2027-05-02T09:00:00.000Z","note":null}]}`
	got := h.put(t, "/v1/places/fushimi-inari", elsewhere, token)
	if got.status != http.StatusUnprocessableEntity || got.decode(t)["field"] != "visits" {
		t.Errorf("a visit naming another place = %d %s, want 422 on visits", got.status, got.body)
	}
}

// === D2's REMOVAL ===

// THE PARAMETER IS REQUIRED, AND THE STORE MUST NOT BE REACHED WITHOUT IT.
//
// "A default is a silent answer to the question D2 makes the user answer on
// screen." A 422 alone would not prove it: a handler that defaulted to keep
// and then also answered 422 is not a shape anybody writes, but a handler that
// reached the store and THEN refused is — so the count of removals is the
// assertion that matters.
func TestRemovingAPlaceWithNoPhotosParameterIsRefusedAndRemovesNothing(t *testing.T) {
	h, token := geographyHarness(t)
	seedAPlace(t, h, token)

	for _, query := range []string{"", "?photos=", "?photos=keepp", "?photos=KEEP", "?photos=true"} {
		got := h.do(t, http.MethodDelete, "/v1/places/tofuku-ji"+query, "", token)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("DELETE /v1/places/tofuku-ji%q = %d %s, want 422", query, got.status, got.body)
			continue
		}
		if field := got.decode(t)["field"]; field != "photos" {
			t.Errorf("DELETE %q named %v, want \"photos\"", query, field)
		}
	}
	if n := len(h.geography().removals); n != 0 {
		t.Errorf("the store was reached %d times by requests that never said how far the "+
			"deletion should reach. The refusal is a fact about the REQUEST and must "+
			"happen before a statement that destroys anything", n)
	}
}

// AND BOTH SPELLINGS REACH THE BRANCH THEY NAME. Without this the leg above is
// satisfied by a route that refuses everything.
func TestTheTwoPhotoBranchesEachReachTheStore(t *testing.T) {
	h, token := geographyHarness(t)
	seedAPlace(t, h, token)
	if got := h.put(t, "/v1/places/fushimi-inari",
		`{"cityId":"kyoto","name":"Fushimi","coordinates":{"lat":34.96,"lng":135.77}}`,
		token); got.status != http.StatusOK {
		t.Fatalf("PUT second place = %d %s", got.status, got.body)
	}

	if got := h.do(t, http.MethodDelete, "/v1/places/tofuku-ji?photos=keep", "", token); got.status != http.StatusOK {
		t.Fatalf("?photos=keep = %d %s", got.status, got.body)
	}
	if got := h.do(t, http.MethodDelete, "/v1/places/fushimi-inari?photos=delete", "", token); got.status != http.StatusOK {
		t.Fatalf("?photos=delete = %d %s", got.status, got.body)
	}

	got := h.geography().removals
	if len(got) != 2 || got[0] != logbook.KeepPhotos || got[1] != logbook.DeletePhotos {
		t.Errorf("the store saw %v, want [keep delete]. A route that mapped both "+
			"spellings to one branch would pass every status assertion in this file", got)
	}
}

// D2 ANSWERS THE WHOLE LOG AND NOT A 204, for D3's reason: the cache cannot
// splice a cascade.
func TestRemovingAPlaceAnswersTheWholeLogbook(t *testing.T) {
	h, token := geographyHarness(t)
	seedAPlace(t, h, token)

	got := h.do(t, http.MethodDelete, "/v1/places/tofuku-ji?photos=keep", "", token)
	if got.status != http.StatusOK {
		t.Fatalf("DELETE = %d %s, want 200 with the whole logbook", got.status, got.body)
	}
	body := got.decode(t)
	log, isEnvelope := body["logbook"].(map[string]any)
	if !isEnvelope {
		t.Fatalf("the answer is %v, want the whole envelope. Removing a place takes its "+
			"visits and either clears two columns on the photographs filed there or "+
			"deletes them — rows in three tables from one request, and the client's own "+
			"`removePlace` already computes all of it", body)
	}
	if places, _ := log["places"].([]any); len(places) != 0 {
		t.Errorf("places = %v, want none left", places)
	}
	if got.header.Get("ETag") == "" {
		t.Errorf("no ETag on a write that moved the log")
	}
}

// A REMOVAL OF SOMETHING ABSENT IS A SUCCESS AND MOVES NO TAG, which is
// DeleteTrip's contract and the client's own: `removePlace` answers true for an
// id the log does not hold.
func TestRemovingAPlaceThatIsNotInTheLogSucceedsAndMovesNothing(t *testing.T) {
	h, token := geographyHarness(t)
	seedAPlace(t, h, token)

	first := h.do(t, http.MethodDelete, "/v1/places/tofuku-ji?photos=keep", "", token)
	if first.status != http.StatusOK {
		t.Fatalf("the first DELETE = %d %s", first.status, first.body)
	}
	second := h.do(t, http.MethodDelete, "/v1/places/tofuku-ji?photos=keep", "", token)
	if second.status != http.StatusOK {
		t.Fatalf("the second DELETE = %d %s, want 200 — a delete of something absent has "+
			"succeeded", second.status, second.body)
	}
	if got, want := second.header.Get("ETag"), first.header.Get("ETag"); got != want {
		t.Errorf("the repeated DELETE answered ETag %q, want %q. A bump on a retried "+
			"delete throws away the phone's whole cached document", got, want)
	}
}

// === helpers ===

func seedAPlace(t *testing.T, h *harness, token string) {
	t.Helper()
	if got := h.put(t, "/v1/cities/kyoto", aKyoto, token); got.status != http.StatusOK {
		t.Fatalf("PUT city = %d %s", got.status, got.body)
	}
	if got := h.put(t, "/v1/places/tofuku-ji",
		`{"cityId":"kyoto","name":"Tofuku-ji","coordinates":{"lat":34.97,"lng":135.77}}`,
		token); got.status != http.StatusOK {
		t.Fatalf("PUT place = %d %s", got.status, got.body)
	}
}

func cityIDsOf(t *testing.T, log map[string]any, tripID string) []string {
	t.Helper()
	trips, _ := log["trips"].([]any)
	for _, raw := range trips {
		trip, _ := raw.(map[string]any)
		if trip["id"] != tripID {
			continue
		}
		ids, _ := trip["cityIds"].([]any)
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, id.(string))
		}
		return out
	}
	t.Fatalf("%s is not in the answered log", tripID)
	return nil
}

func visitIDsOf(t *testing.T, body []byte) []string {
	t.Helper()
	var place struct {
		Visits []struct {
			ID string `json:"id"`
		} `json:"visits"`
	}
	if err := json.Unmarshal(body, &place); err != nil {
		t.Fatalf("the body is not a place: %q: %v", body, err)
	}
	out := make([]string, len(place.Visits))
	for i, visit := range place.Visits {
		out[i] = visit.ID
	}
	return out
}
