// D2's two branches and the visits contract, at FIXTURE SCALE against the
// client's own log.
//
// THE SHEET IS THE SPEC AND THIS FILE IS THE SHEET IN COUNTS, which is
// cascade_test.go's arrangement for D3 and is here for its reason. Every leg
// asserts on a SURVIVING ROW COUNT rather than on error/no-error (DBA F2),
// because the failure that matters is a removal that succeeds and takes one
// thing too many — or one thing too few.
//
// WHY IT IS AT FIXTURE SCALE AND NOT ON A THREE-ROW FIXTURE. The numbers are
// the ones the safety lens and the database lens executed against the real
// 0001 and the real document, and a purpose-built fixture would be a fixture
// built to agree with the implementation. `fushimi-inari` is the place both
// lenses measured on: 28 occasions and 30 photographs, spanning THREE trips.
// Every figure below was re-derived at this working tree by counting before
// and after; none is copied from a report.
//
// AND THE ONE THE MECHANISM LEGS CANNOT REACH IS HERE: a place with 28
// occasions. internal/postgres/place_store_test.go works on a two-row fixture
// where an off-by-one in the ordinal offset still happens to fit.
package seed_test

import (
	"context"
	"database/sql"
	"testing"

	"travellog/internal/logbook"
	"travellog/internal/postgres"
)

// theFushimiNumbers is the shape both lenses measured, asserted before every
// leg that depends on it — because a leg run against a fixture that has moved
// is a leg asserting about a world nobody described.
const (
	fushimiVisits = 28
	fushimiFiled  = 30
	fushimiTrips  = 3
)

func rows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func loadedWithFushimiChecked(t *testing.T) (*sql.DB, postgres.PlaceStore) {
	t.Helper()
	db, _, _ := loaded(t)
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); got != fushimiVisits {
		t.Fatalf("fushimi-inari holds %d occasions, want %d — the fixture has moved and "+
			"every number in this file is about the one that was measured", got, fushimiVisits)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari'`); got != fushimiFiled {
		t.Fatalf("%d photographs are filed at fushimi-inari, want %d", got, fushimiFiled)
	}
	if got := rows(t, db, `SELECT count(DISTINCT trip_id) FROM photos WHERE place_id = 'fushimi-inari'`); got != fushimiTrips {
		t.Fatalf("those photographs span %d trips, want %d — the blast radius of an "+
			"accidental clear is what makes this the place both lenses measured", got, fushimiTrips)
	}
	return db, postgres.PlaceStore{DB: db}
}

// filings answers photograph id -> visit id for everything filed anywhere, so
// a leg can compare the WHOLE map rather than a count. A count is satisfied by
// two photographs swapping occasions.
func filings(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	got, err := db.QueryContext(context.Background(),
		`SELECT id, coalesce(place_id,'') || '/' || coalesce(visit_id,'') FROM photos ORDER BY id`)
	if err != nil {
		t.Fatalf("reading the filings: %v", err)
	}
	defer got.Close()
	out := map[string]string{}
	for got.Next() {
		var id, filed string
		if err := got.Scan(&id, &filed); err != nil {
			t.Fatalf("scanning a filing: %v", err)
		}
		out[id] = filed
	}
	return out
}

// === THE VISITS CONTRACT, ON THE PLACE THE LENSES MEASURED ===

// AN OMITTED `visits` KEY LEAVES ALL 28 AND ALL 30 (DEC-89, SAF-MAJ-4).
//
// This is the leg PD-06's fix does not cover and SAF-MAJ-4 asked for. The
// mandated shape ends "DELETE only the ids absent from the incoming array",
// and when the key is ABSENT every id is absent — so without the contract this
// body does exactly what delete-then-insert did, with the fix in place.
//
// THE BODY IS THE ONE A CLIENT ACTUALLY SENDS. C1's pin creates a wishlist
// place with no visits at all, and every later write to a place — a rename, a
// plan, a cover — names its own field and nothing else.
func TestAPlaceWriteWithTheVisitsKeyOmittedLeavesAll28AndAll30(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	before := filings(t, db)

	if _, _, err := store.PutPlace(context.Background(), travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("fushimi-inari"), Plan: ptrTo(ptrTo("go at first light")),
	}); err != nil {
		t.Fatalf("a plan-only write: %v", err)
	}

	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); got != fushimiVisits {
		t.Errorf("occasions %d -> %d on a body that never mentioned them. Absent means "+
			"LEAVE ALONE; treating it as an empty array is DELETE 28 and unfiles 30 "+
			"photographs across 3 trips", fushimiVisits, got)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari' AND visit_id IS NOT NULL`); got != fushimiFiled {
		t.Errorf("%d photographs are still filed at fushimi-inari, want %d", got, fushimiFiled)
	}
	for id, want := range before {
		if got := filings(t, db)[id]; got != want {
			t.Errorf("photograph %s: %q -> %q", id, want, got)
		}
	}
	// The count that must not fall, whole-log (DEC-89, SAF-MAJ-5). The three
	// standing guards are all blind here: the reference would be gone rather
	// than dangling, there would still be a place, and both columns being NULL
	// is a pair that agrees.
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != 95 {
		t.Errorf("%d photographs still name a place whole-log, want 95", got)
	}

	var plan sql.NullString
	if err := db.QueryRow(`SELECT plan FROM places WHERE id = 'fushimi-inari'`).Scan(&plan); err != nil {
		t.Fatalf("reading the plan back: %v", err)
	}
	if plan.String != "go at first light" {
		t.Errorf("plan = %q — without this the leg above passes against a write that did "+
			"nothing at all", plan.String)
	}
}

// A NO-OP RE-SEND OF ALL 28, BYTE FOR BYTE, IS A NO-OP (PD-06, DB-BLO-1).
//
// Delete-then-insert of an IDENTICAL array leaves `visits` at 28 and every one
// of the 30 photographs with `visit_id` NULL — measured on this project's own
// postgres:17.11, and the only trace is `DELETE 28 / INSERT 0 28`.
func TestReSendingAllTwentyEightOccasionsUnchangedUnfilesNothing(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	ctx := context.Background()
	before := filings(t, db)

	current, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{ID: ptrTo("fushimi-inari")})
	if err != nil {
		t.Fatalf("reading the place: %v", err)
	}
	if len(current.Visits) != fushimiVisits {
		t.Fatalf("the read answered %d occasions, want %d", len(current.Visits), fushimiVisits)
	}
	if _, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("fushimi-inari"), Visits: &current.Visits,
	}); err != nil {
		t.Fatalf("re-sending all %d unchanged: %v", fushimiVisits, err)
	}

	moved := 0
	for id, want := range before {
		if got := filings(t, db)[id]; got != want {
			moved++
			if moved <= 3 {
				t.Errorf("photograph %s: %q -> %q after re-sending an UNCHANGED array", id, want, got)
			}
		}
	}
	if moved > 3 {
		t.Errorf("… and %d more", moved-3)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); got != 0 {
		t.Errorf("%d photographs now name a place with no occasion, want 0. No count "+
			"moved in `visits` and no dangling-reference check can see it — the "+
			"reference is GONE, not dangling", got)
	}
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari' AND note IS NOT NULL`); got != 2 {
		t.Errorf("%d of fushimi-inari's occasions still carry a note, want 2 — a note is "+
			"the traveller's own words and nothing anywhere records what it said", got)
	}
}

// AND `visits: []` IS REFUSED ONLY WHERE THERE IS SOMETHING TO DESTROY.
//
// BOTH HALVES ARE IN ONE LEG ON PURPOSE. The rule is a single sentence —
// refuse the destruction, not the shape — and a leg that asserted only the
// refusal is what shipped first: it passed against a build that refused every
// empty array, including the nine wishlist places in the client's own log for
// which `Emit` writes exactly this. A guard that cannot tell the two apart
// looks identical to a correct one from the refusing side.
//
// The refusal is asserted on the ROW COUNT and not on the status alone,
// because a route that answered 422 AFTER running the DELETE would satisfy a
// status assertion perfectly.
func TestAnEmptyVisitsArrayIsRefusedWhereItWouldDestroyAndIsANoOpWhereItWouldNot(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	ctx := context.Background()
	empty := []logbook.Visit{}

	// VALIDATION MUST NOT DECIDE THIS. It is handed an array and cannot see
	// the occasions, so it is the store's question — and this line is what
	// stops the refusal drifting back up to the shape check.
	if err := logbook.ValidatePlace(logbook.PlaceWrite{ID: ptrTo("fushimi-inari"), Visits: &empty}); err != nil {
		t.Errorf("the validator refused `visits: []` = %v. Whether clearing destroys "+
			"anything is a fact about the place, not about the body", err)
	}

	// The destructive half: 28 occasions, 30 filings, 3 trips.
	if _, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("fushimi-inari"), Visits: &empty,
	}); err == nil {
		t.Errorf("the store accepted an empty array at a place with %d occasions. "+
			"`id <> ALL('{}')` is true of every row, so the DELETE becomes "+
			"`WHERE place_id = $2` — 28 occasions and 30 filings, and 95 filings "+
			"whole-log if it is done to every place", fushimiVisits)
	}
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); got != fushimiVisits {
		t.Errorf("occasions %d -> %d after a refused write", fushimiVisits, got)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari' AND visit_id IS NOT NULL`); got != fushimiFiled {
		t.Errorf("%d photographs are still filed, want %d", got, fushimiFiled)
	}

	// The half the shape-level refusal got wrong. A wishlist place holds no
	// occasions, `Emit` writes `"visits": []` for it, and C1's pin — the only
	// control that creates a place — sends the same shape through the client's
	// generated toJson(). Refusing it made the server's own output something
	// the server would not accept back.
	var wishlist string
	if err := db.QueryRow(`SELECT p.id FROM places p
		WHERE NOT EXISTS (SELECT 1 FROM visits v WHERE v.place_id = p.id AND v.traveller_id = p.traveller_id)
		ORDER BY p.id LIMIT 1`).Scan(&wishlist); err != nil {
		t.Fatalf("finding a wishlist place in the fixture: %v", err)
	}
	if _, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo(wishlist), Visits: &empty,
	}); err != nil {
		t.Errorf("the store refused `visits: []` at %s, which holds no occasions: %v\n"+
			"    There is nothing to clear, so the array is already what it asks for. "+
			"Refusing it refuses every wishlist place in the client's own log, and "+
			"C1's pin with them", wishlist, err)
	}
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = '`+wishlist+`'`); got != 0 {
		t.Errorf("%s holds %d occasions after a no-op, want 0", wishlist, got)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari' AND visit_id IS NOT NULL`); got != fushimiFiled {
		t.Errorf("the no-op at %s moved fushimi's filings to %d, want %d", wishlist, got, fushimiFiled)
	}
}

// REORDERING ALL 28 KEEPS ALL 30 FILINGS, and it is the first time the
// non-deferrable UNIQUE on visit ordinals is exercised at this width.
func TestReversingAllTwentyEightOccasionsKeepsEveryFiling(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	ctx := context.Background()
	before := filings(t, db)

	current, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{ID: ptrTo("fushimi-inari")})
	if err != nil {
		t.Fatalf("reading the place: %v", err)
	}
	reversed := make([]logbook.Visit, len(current.Visits))
	for i := range current.Visits {
		reversed[i] = current.Visits[len(current.Visits)-1-i]
	}
	after, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("fushimi-inari"), Visits: &reversed,
	})
	if err != nil {
		t.Fatalf("reversing %d occasions: %v\n"+
			"    an in-place renumber collides on visits_place_ordinal_uq, which is\n"+
			"    checked per ROW and is not deferrable", len(reversed), err)
	}

	if after.Visits[0].ID != reversed[0].ID || after.Visits[len(after.Visits)-1].ID != reversed[len(reversed)-1].ID {
		t.Errorf("the answered order is %s … %s, want %s … %s. The client reads "+
			"`visits.first.at` as lastVisited, so the order IS the meaning",
			after.Visits[0].ID, after.Visits[len(after.Visits)-1].ID,
			reversed[0].ID, reversed[len(reversed)-1].ID)
	}
	for id, want := range before {
		if got := filings(t, db)[id]; got != want {
			t.Errorf("photograph %s: %q -> %q across a REORDER. Changing the order of the "+
				"array is not a statement about which occasion a photograph was taken "+
				"on", id, want, got)
		}
	}
}

// === D2's TWO BRANCHES ===

// THE DELETE BRANCH, ROW BY ROW. The sheet says "all N, and the notes you
// wrote on them", and the count is what the sheet itself computes from the log.
func TestD2DeletesTheThirtyPhotographsAndTheNotesWrittenOnThem(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)

	captionsBefore := rows(t, db, `SELECT count(*) FROM photos WHERE caption IS NOT NULL`)
	if captionsBefore != 2 {
		t.Fatalf("the fixture holds %d captions, want 2", captionsBefore)
	}

	snap, err := store.RemovePlace(context.Background(), travellerUUID, "fushimi-inari", true)
	if err != nil {
		t.Fatalf("RemovePlace: %v", err)
	}
	if snap.Document == nil {
		t.Fatalf("RemovePlace answered no document — the cache cannot splice a cascade")
	}

	for _, tc := range []struct {
		label, query string
		want         int
	}{
		{"photographs", `SELECT count(*) FROM photos`, 254},
		{"places", `SELECT count(*) FROM places`, 16},
		{"occasions", `SELECT count(*) FROM visits`, 21},
		{"photographs still at fushimi-inari", `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari'`, 0},
		{"captions", `SELECT count(*) FROM photos WHERE caption IS NOT NULL`, 1},
		{"visit notes", `SELECT count(*) FROM visits WHERE note IS NOT NULL`, 3},
	} {
		if got := rows(t, db, tc.query); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.label, got, tc.want)
		}
	}

	// THE COUNT THAT MUST NOT FALL, AND HERE IT FALLS BY A KNOWN AMOUNT
	// (DEC-89, SAF-MAJ-5). 95 photographs carried a place before and 65 do
	// after; the 30 that left are the ones the user asked to destroy. "Known,
	// not merely non-zero" is the whole difference between this and a guard
	// that passes while a route unfiles the log.
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != 65 {
		t.Errorf("%d photographs still name a place, want 65 — 95 before, and the 30 that "+
			"left are fushimi-inari's own", got)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); got != 0 {
		t.Errorf("%d photographs name a place with no occasion, want 0", got)
	}
}

// THE KEEP BRANCH, ROW BY ROW, AND THE CAPTION IS THE ROW THAT SEPARATES THE
// TWO BRANCHES.
//
// "They lose the pin but keep their date and city" is
// `Photo.copyWith(clearPlace: true)`, which clears BOTH columns. The caption is
// asserted with them because the sheet names "the notes you wrote on them" on
// the DESTRUCTIVE branch only — 2 captions before, 1 after a delete, 2 after a
// keep, and that single row is the whole of the difference in the fixture.
func TestD2KeepsTheThirtyPhotographsWithTheirDateCityAndCaption(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	ctx := context.Background()

	kept, err := db.QueryContext(ctx,
		`SELECT id, city_id, trip_id, taken_at, coalesce(caption,'') FROM photos
		 WHERE place_id = 'fushimi-inari' ORDER BY id`)
	if err != nil {
		t.Fatalf("reading the thirty: %v", err)
	}
	type shot struct {
		city, trip, caption string
		takenAt             string
	}
	before := map[string]shot{}
	for kept.Next() {
		var id string
		var s shot
		if err := kept.Scan(&id, &s.city, &s.trip, &s.takenAt, &s.caption); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		before[id] = s
	}
	kept.Close()
	if len(before) != fushimiFiled {
		t.Fatalf("read %d photographs, want %d", len(before), fushimiFiled)
	}

	if _, err := store.RemovePlace(ctx, travellerUUID, "fushimi-inari", false); err != nil {
		t.Fatalf("RemovePlace: %v", err)
	}

	for _, tc := range []struct {
		label, query string
		want         int
	}{
		{"photographs", `SELECT count(*) FROM photos`, 284},
		{"places", `SELECT count(*) FROM places`, 16},
		{"occasions", `SELECT count(*) FROM visits`, 21},
		{"captions", `SELECT count(*) FROM photos WHERE caption IS NOT NULL`, 2},
		{"photographs naming a place", `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`, 65},
		{"photographs naming an occasion", `SELECT count(*) FROM photos WHERE visit_id IS NOT NULL`, 65},
		{"half-filed", `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`, 0},
	} {
		if got := rows(t, db, tc.query); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.label, got, tc.want)
		}
	}

	for id, want := range before {
		var place, visit, caption sql.NullString
		var city, trip, takenAt string
		if err := db.QueryRow(
			`SELECT place_id, visit_id, city_id, trip_id, taken_at, caption FROM photos WHERE id = $1`, id).
			Scan(&place, &visit, &city, &trip, &takenAt, &caption); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if place.Valid {
			t.Errorf("%s: place_id = %q, want NULL — the pin is what goes", id, place.String)
		}
		if visit.Valid {
			t.Errorf("%s: visit_id = %q, want NULL. `clearPlace: true` clears BOTH, and a "+
				"mutation clearing only place_id would pass every other assertion here",
				id, visit.String)
		}
		if city != want.city || trip != want.trip || takenAt != want.takenAt {
			t.Errorf("%s: city/trip/takenAt moved from %s/%s/%s to %s/%s/%s",
				id, want.city, want.trip, want.takenAt, city, trip, takenAt)
		}
		if caption.String != want.caption {
			t.Errorf("%s: caption %q -> %q. The sheet names the notes on the DESTRUCTIVE "+
				"branch only", id, want.caption, caption.String)
		}
	}
}

// THE WALKS ARE NOT TOUCHED ON EITHER BRANCH, and that is `walks` having no
// `place_id` at all rather than anything Go does. D2 says "the track stays
// with the day it was recorded either way", and the absence IS the promise.
func TestD2LeavesTheWalksAloneOnBothBranches(t *testing.T) {
	for _, deletePhotos := range []bool{true, false} {
		db, store := loadedWithFushimiChecked(t)
		before := rows(t, db, `SELECT count(*) FROM walks`)
		if before != 2 {
			t.Fatalf("the fixture holds %d walks, want 2", before)
		}
		if _, err := store.RemovePlace(context.Background(), travellerUUID, "fushimi-inari", deletePhotos); err != nil {
			t.Fatalf("RemovePlace(deletePhotos=%v): %v", deletePhotos, err)
		}
		if got := rows(t, db, `SELECT count(*) FROM walks`); got != before {
			t.Errorf("deletePhotos=%v: walks %d -> %d", deletePhotos, before, got)
		}
	}
}

// AND THE TWO WRITES MOVE NO FILING AT ALL (DEC-89, SAF-MAJ-5).
//
// `SELECT count(*) FROM photos WHERE place_id IS NOT NULL` is UNCHANGED across
// `PUT /v1/cities/{id}` and `PUT /v1/places/{id}`, and falls by a known amount
// only on the delete. This is the assertion the three standing guards cannot
// make, run across this step's two non-destructive routes.
func TestNeitherOfThisStepsTwoWritesLowersTheFilingCount(t *testing.T) {
	db, store := loadedWithFushimiChecked(t)
	ctx := context.Background()
	cities := postgres.CityStore{DB: db}

	const filed = 95
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != filed {
		t.Fatalf("the fixture holds %d filed photographs, want %d", got, filed)
	}

	if _, err := cities.PutCity(ctx, travellerUUID, logbook.CityWrite{
		ID: ptrTo("kyoto"), Name: ptrTo("Kyōto"),
	}); err != nil {
		t.Fatalf("PUT a city: %v", err)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != filed {
		t.Errorf("PUT /v1/cities/{id} moved the filing count to %d, want %d", got, filed)
	}

	osaka := logbook.CityWrite{
		ID: ptrTo("osaka-new"), Name: ptrTo("Osaka"),
		Country:  &logbook.Country{Code: "JP", Name: "Japan"},
		Centre:   &logbook.LatLng{Lat: 34.69, Lng: 135.50},
		AttachTo: ptrTo("kyoto-in-may"),
	}
	if _, err := cities.PutCity(ctx, travellerUUID, osaka); err != nil {
		t.Fatalf("PUT a city with attachTo: %v", err)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != filed {
		t.Errorf("the cascading city write moved the filing count to %d, want %d", got, filed)
	}

	if _, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("fushimi-inari"), Name: ptrTo("Fushimi Inari Taisha"),
	}); err != nil {
		t.Fatalf("PUT a place: %v", err)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != filed {
		t.Errorf("PUT /v1/places/{id} moved the filing count to %d, want %d — this is the "+
			"one assertion the dangling check, the place-without-occasion query and a "+
			"pair-agreement check are all blind to", got, filed)
	}

	if _, _, err := store.PutPlace(ctx, travellerUUID, logbook.PlaceWrite{
		ID: ptrTo("a-new-pin"), CityID: ptrTo("kyoto"), Name: ptrTo("Somewhere new"),
		Coordinates: &logbook.LatLng{Lat: 35.0, Lng: 135.7},
	}); err != nil {
		t.Fatalf("C1's pin: %v", err)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != filed {
		t.Errorf("C1's pin moved the filing count to %d, want %d", got, filed)
	}
	if got := rows(t, db, `SELECT count(*) FROM places`); got != 18 {
		t.Errorf("places = %d, want 18", got)
	}
}

func ptrTo[T any](v T) *T { return &v }
