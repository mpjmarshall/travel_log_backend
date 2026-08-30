// C1's pin and D2's removal against a real PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func placeStore(t *testing.T) (PlaceStore, *sql.DB) {
	t.Helper()
	db := seeded(t)
	return PlaceStore{DB: db}, db
}

// visitFilings answers photograph id -> visit_id, so a leg can compare the
// whole map before and after rather than a count.
func visitFilings(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT id, coalesce(visit_id, '') FROM photos WHERE traveller_id = $1::uuid ORDER BY id`, tid)
	if err != nil {
		t.Fatalf("reading the filings: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, visit string
		if err := rows.Scan(&id, &visit); err != nil {
			t.Fatalf("scanning a filing: %v", err)
		}
		out[id] = visit
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the filings: %v", err)
	}
	return out
}

func visitOrder(t *testing.T, db *sql.DB, placeID string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT id FROM visits WHERE traveller_id = $1::uuid AND place_id = $2 ORDER BY ordinal, id`,
		tid, placeID)
	if err != nil {
		t.Fatalf("reading the visit order: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a visit: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func at(text string) logbook.Instant {
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return logbook.At(parsed)
}

// a no-op re-send must be a no-op.
func TestReSendingAnUnchangedVisitsArrayDoesNotUnfileItsPhotographs(t *testing.T) {
	store, db := placeStore(t)
	ctx := context.Background()

	before := visitFilings(t, db)
	if before["p-may"] == "" || before["p-autumn"] == "" {
		t.Fatalf("no photographs are filed at fushimi-inari — this leg would pass vacuously: %v", before)
	}

	current, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari")})
	if err != nil {
		t.Fatalf("reading the place through a no-op write: %v", err)
	}
	if len(current.Visits) == 0 {
		t.Fatalf("fushimi-inari came back with no visits at all")
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{
		ID: ptr("fushimi-inari"), Visits: &current.Visits,
	}); err != nil {
		t.Fatalf("re-sending the unchanged array: %v", err)
	}

	after := visitFilings(t, db)
	for id, want := range before {
		if after[id] != want {
			t.Errorf("photograph %s: visitId %q -> %q after re-sending an UNCHANGED visits "+
				"array. Delete-then-insert NULLs it through photos_visit_fk, and no "+
				"dangling-reference check can see it — the reference is gone rather "+
				"than dangling", id, want, after[id])
		}
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); n != 0 {
		t.Errorf("%d photographs now name a place with no occasion, want 0 — a state the "+
			"client's log has never held", n)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); n != 2 {
		t.Errorf("%d photographs still name a place, want 2", n)
	}
}

// ABSENT `visits` leaves them alone, and that is what makes createPlace
// correct by construction.
func TestAPlaceWriteWithNoVisitsKeyLeavesEveryVisitAndEveryFilingAlone(t *testing.T) {
	store, db := placeStore(t)

	before := visitFilings(t, db)
	visitsBefore := count(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`)
	if visitsBefore == 0 {
		t.Fatalf("fushimi-inari holds no visits — this leg would pass vacuously")
	}

	if _, _, err := store.PutPlace(context.Background(), tid, logbook.PlaceWrite{
		ID: ptr("fushimi-inari"), Name: ptr("Fushimi Inari Taisha"),
	}); err != nil {
		t.Fatalf("the rename: %v", err)
	}

	if got := count(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); got != visitsBefore {
		t.Errorf("visits %d -> %d after a body carrying only `name`. Absent means LEAVE "+
			"ALONE; treating it as an empty array is what unfiles 30 photographs "+
			"across 3 trips at fushimi-inari in the client's own log", visitsBefore, got)
	}
	after := visitFilings(t, db)
	for id, want := range before {
		if after[id] != want {
			t.Errorf("photograph %s: visitId %q -> %q after a rename", id, want, after[id])
		}
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM places WHERE traveller_id=$1::uuid AND id='fushimi-inari'`, tid).
		Scan(&name); err != nil {
		t.Fatalf("reading the name back: %v", err)
	}
	if name != "Fushimi Inari Taisha" {
		t.Errorf("name = %q — the leg above would pass against a write that did nothing "+
			"at all, so this is what says the write happened", name)
	}
}

// C1's pin, through the store: a wishlist place is a place with no visits,
// Creating one must not need a `visits` key at all.
func TestCreatingAPlaceWithNoVisitsIsAWishlistPlace(t *testing.T) {
	store, db := placeStore(t)

	place, version, err := store.PutPlace(context.Background(), tid, logbook.PlaceWrite{
		ID: ptr("tofuku-ji"), CityID: ptr("kyoto"), Name: ptr("Tofuku-ji"),
		Coordinates: &logbook.LatLng{Lat: 34.976, Lng: 135.774},
	})
	if err != nil {
		t.Fatalf("C1's pin: %v", err)
	}
	if len(place.Visits) != 0 {
		t.Errorf("the new pin came back with %d visits, want none — 'a place with no "+
			"visits IS a wishlist place'", len(place.Visits))
	}
	if version < 1 {
		t.Errorf("version = %d, want a bump: places are in the emitted document", version)
	}
	if n := count(t, db, `SELECT count(*) FROM places WHERE traveller_id=$1::uuid`, tid); n != 3 {
		t.Errorf("places = %d, want 3", n)
	}
}

// the reorder, and it is's first time the non-deferrable unique on visit
// ordinals is exercised by A ROUTE.
func TestReorderingTheVisitsArrayRewritesTheOrdinalsAndKeepsTheFiling(t *testing.T) {
	store, db := placeStore(t)
	ctx := context.Background()

	two := []logbook.Visit{
		{ID: "v-fushimi-autumn", PlaceID: "fushimi-inari", TripID: "autumn-crossing", At: at("2027-09-20T07:05:00Z")},
		{ID: "v-fushimi-may", PlaceID: "fushimi-inari", TripID: "kyoto-in-may", At: at("2027-05-03T07:05:00Z")},
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &two}); err != nil {
		t.Fatalf("the first array: %v", err)
	}
	if got := strings.Join(visitOrder(t, db, "fushimi-inari"), ","); got != "v-fushimi-autumn,v-fushimi-may" {
		t.Fatalf("stored order = %s, want v-fushimi-autumn,v-fushimi-may", got)
	}
	before := visitFilings(t, db)

	swapped := []logbook.Visit{two[1], two[0]}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &swapped}); err != nil {
		t.Fatalf("the reorder: %v\n"+
			"    an in-place renumber collides on visits_place_ordinal_uq, which is NOT\n"+
			"    deferrable — the ordinals have to be parked above every value the new\n"+
			"    array will use before any of them is written", err)
	}
	if got := strings.Join(visitOrder(t, db, "fushimi-inari"), ","); got != "v-fushimi-may,v-fushimi-autumn" {
		t.Errorf("stored order = %s, want v-fushimi-may,v-fushimi-autumn", got)
	}
	for id, want := range before {
		if got := visitFilings(t, db)[id]; got != want {
			t.Errorf("photograph %s: visitId %q -> %q across a REORDER. The order of the "+
				"array is not a change to which occasion a photograph was taken on",
				id, want, got)
		}
	}
}

// the offset is derived and the plan's `+ 1000` is a constant, and this is
// the leg that separates them.
func TestAVisitsArrayLongerThanAThousandStillReorders(t *testing.T) {
	store, db := placeStore(t)
	ctx := context.Background()

	const many = 1100
	long := make([]logbook.Visit, many)
	for i := range long {
		long[i] = logbook.Visit{
			ID:     "v-" + strings.Repeat("0", 4-len(itoa(i))) + itoa(i),
			TripID: "kyoto-in-may",
			At:     at("2027-05-03T07:05:00Z"),
		}
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("wishlist-pin"), Visits: &long}); err != nil {
		t.Fatalf("writing %d visits: %v", many, err)
	}
	if got := count(t, db, `SELECT count(*) FROM visits WHERE place_id='wishlist-pin'`); got != many {
		t.Fatalf("visits stored = %d, want %d", got, many)
	}

	reversed := make([]logbook.Visit, many)
	for i := range long {
		reversed[i] = long[many-1-i]
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("wishlist-pin"), Visits: &reversed}); err != nil {
		t.Fatalf("reversing %d visits: %v\n"+
			"    a FIXED offset of 1000 collides here: the row moving to ordinal 1000\n"+
			"    meets a row still parked at 1000, and visits_place_ordinal_uq is\n"+
			"    checked per ROW", many, err)
	}
	order := visitOrder(t, db, "wishlist-pin")
	if len(order) != many || order[0] != reversed[0].ID || order[many-1] != reversed[many-1].ID {
		t.Errorf("the reversed order did not stick: first %q last %q, want %q and %q",
			order[0], order[len(order)-1], reversed[0].ID, reversed[many-1].ID)
	}
}

// A place write does not move an occasion between pins.
func TestAPlaceWriteCannotStealAnotherPlacesOccasion(t *testing.T) {
	store, db := placeStore(t)

	stolen := []logbook.Visit{
		{ID: "v-fushimi-may", TripID: "kyoto-in-may", At: at("2027-05-03T07:05:00Z")},
	}
	_, _, err := store.PutPlace(context.Background(), tid, logbook.PlaceWrite{
		ID: ptr("wishlist-pin"), Visits: &stolen,
	})
	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "visits" {
		t.Fatalf("claiming another place's occasion = %v, want an invalid_field on visits", err)
	}
	var held string
	if err := db.QueryRow(`SELECT place_id FROM visits WHERE traveller_id=$1::uuid AND id='v-fushimi-may'`, tid).
		Scan(&held); err != nil {
		t.Fatalf("reading the occasion back: %v", err)
	}
	if held != "fushimi-inari" {
		t.Errorf("the occasion moved to %q — a refusal that rolls back is the whole point "+
			"of it being inside the transaction", held)
	}
}

// dropping an occasion that still holds photographs is refused, and an empty
// one may go.
func TestDroppingAnOccupiedOccasionIsRefusedAndAnEmptyOneMayGo(t *testing.T) {
	store, db := placeStore(t)
	ctx := context.Background()

	both := []logbook.Visit{
		{ID: "v-fushimi-may", TripID: "kyoto-in-may", At: at("2027-05-03T07:05:00Z")},
		{ID: "v-fushimi-empty", TripID: "autumn-crossing", At: at("2027-09-20T07:05:00Z")},
	}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &both}); err != nil {
		t.Fatalf("the two-occasion array: %v", err)
	}

	occupiedDropped := []logbook.Visit{both[1]}
	_, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &occupiedDropped})
	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "visits" {
		t.Fatalf("dropping an occasion that holds two photographs = %v, want an "+
			"invalid_field on visits — dropping it unfiles them and leaves them naming "+
			"a place with no occasion", err)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id='fushimi-inari'`); n != 2 {
		t.Errorf("visits = %d, want 2 — the refusal rolls back", n)
	}

	emptyDropped := []logbook.Visit{both[0]}
	if _, _, err := store.PutPlace(ctx, tid, logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &emptyDropped}); err != nil {
		t.Fatalf("dropping an occasion nothing is filed to: %v — a bare occasion is not "+
			"a record of anything and refusing every departure would make a tidy-up "+
			"impossible", err)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id='fushimi-inari'`); n != 1 {
		t.Errorf("visits = %d, want 1", n)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); n != 0 {
		t.Errorf("%d photographs name a place with no occasion, want 0", n)
	}
}

func TestAVisitNamingATripThatIsNotInTheLogIsRefusedByName(t *testing.T) {
	store, _ := placeStore(t)
	visits := []logbook.Visit{{ID: "v-x", TripID: "no-such-trip", At: at("2027-05-03T07:05:00Z")}}
	_, _, err := store.PutPlace(context.Background(), tid, logbook.PlaceWrite{
		ID: ptr("fushimi-inari"), Visits: &visits,
	})
	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "visits" {
		t.Errorf("a visit naming an unknown trip = %v, want an invalid_field on visits — "+
			"visits_trip_fk would answer a 500 with nothing on it", err)
	}
}

func TestAPlaceCreatedWithoutItsRequiredFieldsIsRefusedByName(t *testing.T) {
	store, _ := placeStore(t)
	for _, tc := range []struct {
		field string
		write logbook.PlaceWrite
	}{
		{"cityId", logbook.PlaceWrite{ID: ptr("new-pin"), Name: ptr("New"), Coordinates: &logbook.LatLng{}}},
		{"name", logbook.PlaceWrite{ID: ptr("new-pin"), CityID: ptr("kyoto"), Coordinates: &logbook.LatLng{}}},
		{"coordinates", logbook.PlaceWrite{ID: ptr("new-pin"), CityID: ptr("kyoto"), Name: ptr("New")}},
		{"cityId", logbook.PlaceWrite{ID: ptr("new-pin"), CityID: ptr("nowhere"), Name: ptr("New"), Coordinates: &logbook.LatLng{}}},
	} {
		_, _, err := store.PutPlace(context.Background(), tid, tc.write)
		var invalid logbook.InvalidFieldError
		if !asInvalidField(err, &invalid) || invalid.Field != tc.field {
			t.Errorf("the create missing %s = %v, want an invalid_field on %s", tc.field, err, tc.field)
		}
	}
}

// D2's delete branch, and it is about the order of two statements.
func TestRemovingAPlaceAndItsPhotographsActuallyRemovesThem(t *testing.T) {
	store, db := placeStore(t)

	before := count(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari'`)
	if before == 0 {
		t.Fatalf("the fixture holds no photographs at fushimi-inari — this leg would pass vacuously")
	}
	walksBefore := count(t, db, `SELECT count(*) FROM walks`)

	snap, err := store.RemovePlace(context.Background(), tid, "fushimi-inari", true)
	if err != nil {
		t.Fatalf("RemovePlace: %v", err)
	}
	if snap.Document == nil {
		t.Fatalf("RemovePlace answered no document. The cache cannot splice a cascade: " +
			"a place's removal takes its visits and either clears two columns on the " +
			"photographs filed there or deletes them, which is rows in three tables")
	}

	if after := count(t, db, `SELECT count(*) FROM photos WHERE place_id = 'fushimi-inari'`); after != 0 {
		t.Errorf("%d of %d photographs filed at fushimi-inari survived a ?photos=delete — "+
			"the place was almost certainly deleted BEFORE them, so photos_place_fk "+
			"ON DELETE SET NULL (place_id) cleared their pin and the DELETE matched "+
			"nothing", after, before)
	}
	if after := count(t, db, `SELECT count(*) FROM photos`); after != 0 {
		t.Errorf("%d photographs survive in the whole log, want 0 — both of the "+
			"fixture's are filed at fushimi-inari", after)
	}
	if after := count(t, db, `SELECT count(*) FROM walks`); after != walksBefore {
		t.Errorf("walks moved from %d to %d — D2 says the track stays with the day it was "+
			"recorded EITHER WAY, and there is no place_id on walks at all",
			walksBefore, after)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); n != 0 {
		t.Errorf("%d visits survive the pin, want 0 — 'the visits go with the pin', and "+
			"nothing could have kept them: a visit lives inside the place and points "+
			"back at it", n)
	}
}

// D2's keep branch, and all four of its promises.
func TestKeepingThePhotographsLeavesTheirDateCityAndCaptionAndClearsBothColumns(t *testing.T) {
	store, db := placeStore(t)
	mustExec(t, db, `UPDATE photos SET caption = 'the torii at dawn' WHERE traveller_id=$1::uuid AND id='p-may'`, tid)

	walksBefore := count(t, db, `SELECT count(*) FROM walks`)
	if _, err := store.RemovePlace(context.Background(), tid, "fushimi-inari", false); err != nil {
		t.Fatalf("RemovePlace: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM photos`); n != 2 {
		t.Fatalf("photographs = %d, want both kept", n)
	}
	rows, err := db.Query(`SELECT id, place_id, visit_id, city_id, trip_id, taken_at, caption
		FROM photos WHERE traveller_id=$1::uuid ORDER BY id`, tid)
	if err != nil {
		t.Fatalf("reading the photographs back: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, city, trip string
		var place, visit, caption sql.NullString
		var takenAt time.Time
		if err := rows.Scan(&id, &place, &visit, &city, &trip, &takenAt, &caption); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if place.Valid {
			t.Errorf("%s: place_id = %q, want NULL — the pin is what goes", id, place.String)
		}
		if visit.Valid {
			t.Errorf("%s: visit_id = %q, want NULL. `clearPlace: true` clears BOTH, and a "+
				"mutation that cleared only place_id would pass every other assertion "+
				"in this leg", id, visit.String)
		}
		if city != "kyoto" {
			t.Errorf("%s: city_id = %q, want kyoto — 'they keep their date and city'", id, city)
		}
		if takenAt.IsZero() {
			t.Errorf("%s: taken_at is zero", id)
		}
		if id == "p-may" && caption.String != "the torii at dawn" {
			t.Errorf("%s: caption = %q, want it untouched. The sheet names the notes on "+
				"the DESTRUCTIVE branch only", id, caption.String)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id = 'fushimi-inari'`); n != 0 {
		t.Errorf("%d visits survive, want 0 — the visits go with the pin EITHER WAY, and "+
			"the sheet says so because nothing could have kept them", n)
	}
	if n := count(t, db, `SELECT count(*) FROM walks`); n != walksBefore {
		t.Errorf("walks moved from %d to %d on the KEEP branch too", walksBefore, n)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); n != 0 {
		t.Errorf("%d photographs name a place with no occasion, want 0", n)
	}
}

// A removal of something absent takes nothing with it — including on the
// destructive branch.
func TestRemovingAPlaceThatIsNotInTheLogTakesNoPhotographWithIt(t *testing.T) {
	store, db := placeStore(t)

	before := count(t, db, `SELECT count(*) FROM photos`)
	versionBefore := versionOf(t, db, tid)

	snap, err := store.RemovePlace(context.Background(), tid, "never-pinned", true)
	if err != nil {
		t.Fatalf("removing a place that is not there = %v, want a success — the client's "+
			"own removePlace answers true for an id the log does not hold", err)
	}
	if snap.Document == nil {
		t.Fatalf("a miss answered no document")
	}
	if after := count(t, db, `SELECT count(*) FROM photos`); after != before {
		t.Errorf("photographs %d -> %d on a removal that removed nothing", before, after)
	}
	if after := versionOf(t, db, tid); after != versionBefore {
		t.Errorf("logbook_version %d -> %d on a removal that removed nothing. A bump on a "+
			"retried delete throws away the phone's whole cached document",
			versionBefore, after)
	}
}

// itoa is strconv.Itoa under another name, so the long-array leg reads
// without a second import in a file that is otherwise about SQL.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func asInvalidField(err error, into *logbook.InvalidFieldError) bool {
	for err != nil {
		if got, ok := err.(logbook.InvalidFieldError); ok {
			*into = got
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
