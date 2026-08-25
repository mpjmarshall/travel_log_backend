// D3's cascade, row for row, against the client's own log.
//
// THE SHEET IS THE SPEC AND THIS FILE IS THE SHEET IN COUNTS. D3 itemises four
// consequences beside the words 'deleted' and 'kept', and then makes the user
// type the trip's name out before the action arms. Every leg below is named
// for the line it defends, and EVERY ONE ASSERTS ON A SURVIVING ROW COUNT
// rather than on error/no-error (DBA F2) — because the failure that matters
// here is a cascade that succeeds and takes one thing too many.
//
// WHY IT IS AT FIXTURE SCALE AND NOT ON A THREE-ROW FIXTURE. The numbers are
// the ones the safety lens executed against the real 0001 and the real
// document, and a purpose-built fixture would be a fixture built to agree with
// the implementation. `autumn-crossing` is the trip the sheet was measured on:
// 96 photographs, 1 walk, 5 pins across three cities, one live link. All eight
// figures below were re-derived at this working tree, against postgres:17.11,
// by counting before and after — they are not copied from the report.
//
// THE ONE SHAPE THE FIXTURE DOES NOT HOLD IS THE ONE THAT ONCE WENT WRONG, and
// it is deliberately not synthesised here. "Deleting a trip left twenty-two of
// ANOTHER trip's photographs naming a visit that had gone" — in this document
// no photograph of another trip names an autumn-crossing visit (measured: 0),
// so the fixture cannot express it. That leg lives in
// internal/postgres/logbook_store_test.go on a log built to hold it, which is
// the same arrangement `to_file_test.dart` uses in the client for the window
// filter the sample log no longer exercises.
package seed_test

import (
	"context"
	"database/sql"
	"testing"

	"travellog/internal/postgres"
)

// deletedAutumnCrossing seeds the client's own log, deletes the trip D3's
// sheet was measured on THROUGH THE STORE, and answers the row counts before
// and after.
//
// IT GOES THROUGH LogbookStore.DeleteTrip AND NOT THROUGH `DELETE FROM trips`.
// The raw statement is what the safety lens ran and it proves the SCHEMA; what
// these legs are about is the route, which has a version bump, a whole-log
// answer and a Go-side order of statements the schema cannot see.
func deletedAutumnCrossing(t *testing.T) (before, after map[string]int, db *sql.DB) {
	t.Helper()
	db, _, _ = loaded(t)
	before = rowCounts(t, db)

	snap, err := postgres.LogbookStore{DB: db}.DeleteTrip(context.Background(), travellerUUID, "autumn-crossing")
	if err != nil {
		t.Fatalf("DeleteTrip: %v", err)
	}
	if snap.Document == nil {
		t.Fatalf("DeleteTrip answered no document. The cache cannot splice a cascade: " +
			"D3 removes rows from five tables, and a client handed a bare trip or a " +
			"204 has no way to know which photographs and walks went with it.")
	}
	after = rowCounts(t, db)
	return before, after, db
}

// SHEET LINE 1 — "N photos and their notes", DELETED.
//
// The trip's photographs go, and no other trip's do. Both halves: a cascade
// that deleted everything would pass the first assertion on its own.
func TestD3DeletesTheTripsPhotographsAndTheirNotes(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["photos"] != 284 || after["photos"] != 188 {
		t.Errorf("photos %d -> %d, want 284 -> 188. The sheet says '96 photos and their "+
			"notes' are deleted, and the count is what the sheet itself computes from "+
			"the log.", before["photos"], after["photos"])
	}
}

// SHEET LINE 2 — "N recorded walks", DELETED.
//
// One of the two walks in the log is autumn-crossing's, and the other is not.
// A cascade that took both would still leave the trip gone and nothing else
// would notice.
func TestD3DeletesTheTripsWalksAndLeavesTheOtherTripsAlone(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["walks"] != 2 || after["walks"] != 1 {
		t.Errorf("walks %d -> %d, want 2 -> 1. The sheet says '1 recorded walk' is "+
			"deleted; the other belongs to kyoto-in-may and D3 says nothing about it.",
			before["walks"], after["walks"])
	}
}

// SHEET LINE 3 — "N pins in Busan, Kyoto and Seoul", KEPT. THIS IS THE ROW
// THAT IS EASIEST TO GET WRONG AND THE ONLY ONE WHOSE FAILURE IS SILENT.
//
// The subtitle says it in words: "The pins stay in those cities — a trip owns
// its visits, not its places". So `places` must not move at all, and the CRUD
// reflex — `DELETE FROM places WHERE id IN (SELECT place_id FROM visits WHERE
// trip_id = $1)` — takes five of them, with no error anywhere.
func TestD3KeepsEveryPinAndTakesOnlyTheTripsOwnVisits(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["places"] != 17 || after["places"] != 17 {
		t.Errorf("places %d -> %d, want 17 -> 17. D3's own subtitle: 'The pins stay in "+
			"those cities — a trip owns its visits, not its places'. Five places carry "+
			"an autumn-crossing visit, and deleting them is the one-thing-too-many "+
			"defect.", before["places"], after["places"])
	}
	if before["visits"] != 49 || after["visits"] != 44 {
		t.Errorf("visits %d -> %d, want 49 -> 44 — the trip's own five, and nobody "+
			"else's", before["visits"], after["visits"])
	}
}

// SHEET LINE 3, THE HARD HALF — A PIN LEFT WITH NO VISITS AT ALL SURVIVES.
//
// `gamcheon` is the fixture's one place whose every visit is on
// autumn-crossing (measured: it is the only one). After the cascade it is a
// WISHLIST PLACE — a pin with no visits — which is exactly what the client's
// own model says: "A place whose only visits were on the deleted trip survives
// with none, which is a wishlist place". A count of `places` alone cannot see
// this go wrong if some other place were deleted in its stead, so the row is
// named.
func TestD3KeepsThePinWhoseOnlyVisitsWereOnTheDeletedTrip(t *testing.T) {
	_, _, db := deletedAutumnCrossing(t)
	ctx := context.Background()

	var name, cityID string
	err := db.QueryRowContext(ctx,
		`SELECT name, city_id FROM places WHERE traveller_id = $1::uuid AND id = 'gamcheon'`,
		travellerUUID).Scan(&name, &cityID)
	if err == sql.ErrNoRows {
		t.Fatalf("gamcheon is gone. Its every visit was on autumn-crossing, and 'kept' is " +
			"what the sheet promised — a pin with no visits is a WISHLIST PLACE and is " +
			"a state the client's model has a name for.")
	}
	if err != nil {
		t.Fatalf("reading gamcheon back: %v", err)
	}
	if cityID != "busan" {
		t.Errorf("gamcheon came back in %q, want busan — 'the pins stay in those cities'", cityID)
	}

	var visits int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM visits WHERE traveller_id = $1::uuid AND place_id = 'gamcheon'`,
		travellerUUID).Scan(&visits); err != nil {
		t.Fatalf("counting gamcheon's visits: %v", err)
	}
	if visits != 0 {
		t.Errorf("gamcheon has %d visits left, want 0 — every one of them was on the "+
			"deleted trip, and a visit that survives its trip is a reference to a trip "+
			"that is gone", visits)
	}
}

// SHEET LINE 4 — "The shared link stops working", DEAD.
//
// The link is on the trip, so it goes with the trip. THE ROW GOES ENTIRELY AND
// NOT ONLY ITS `revoked_at`, and that is SAF-MIN-9 accepted in writing: DEC-67
// chose its primary key to KEEP revocation history, and
// `share_links_trip_fk ON DELETE CASCADE` destroys that trip's history along
// with the live row. The argument is in 0004's own comment beside DEC-67's,
// and this leg is what makes the accepted behaviour a measured one rather than
// a silence.
func TestD3TakesTheSharedLinkAndItsWholeHistoryWithTheTrip(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["share_links"] != 1 || after["share_links"] != 0 {
		t.Errorf("share_links %d -> %d, want 1 -> 0. The link is on the trip and D3 says "+
			"'The shared link stops working'; the revocation history goes with it, "+
			"which is accepted in 0004's comment.", before["share_links"], after["share_links"])
	}
}

// THE ROWS THE SHEET DOES NOT ITEMISE, BECAUSE IT DOES NOT HAVE TO.
//
// The trip goes and its itinerary goes with it — `trip_cities` is the join
// table behind `cityIds`, so five rows leave with the trip and the OTHER
// trips' thirteen stay. And the CITIES ARE UNTOUCHED: nothing in this app
// authorises destroying a city (DEC-57), `trip_cities_city_fk` is RESTRICT,
// and a cascade that reached them would be the largest destructive act in the
// application behind a sheet that never mentions it.
func TestD3TakesTheItineraryAndNeverACity(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["trips"] != 7 || after["trips"] != 6 {
		t.Errorf("trips %d -> %d, want 7 -> 6", before["trips"], after["trips"])
	}
	if before["trip_cities"] != 18 || after["trip_cities"] != 13 {
		t.Errorf("trip_cities %d -> %d, want 18 -> 13 — autumn-crossing's five leave and "+
			"the other trips' thirteen stay", before["trip_cities"], after["trip_cities"])
	}
	if before["cities"] != 12 || after["cities"] != 12 {
		t.Errorf("cities %d -> %d, want 12 -> 12. Nothing in this app authorises "+
			"destroying a city (DEC-57) and no sheet says a word about it; five of "+
			"these twelve are named on D3's own KEPT line.", before["cities"], after["cities"])
	}
	if before["media_objects"] != after["media_objects"] {
		t.Errorf("media_objects %d -> %d. The covers are RESTRICT and a deleted trip's "+
			"asset is still another trip's asset.", before["media_objects"], after["media_objects"])
	}
}

// THE SERVER-SIDE `_repointed`, AND ITS SECOND ASSERTION IS THE WHOLE POINT.
//
// After the cascade no surviving photograph names a visit that has gone. The
// schema does that through `photos_visit_fk … ON DELETE SET NULL (visit_id)`,
// which exists only because the DBA found it, and this leg ASSERTS it rather
// than trusting it.
//
// A DANGLING-REFERENCE CHECK IS NOT A FILING CHECK, and zero has to be zero
// for the right reason. If the photographs had been DELETED instead of
// repointed, the dangling query would answer 0 as well — the reference is gone
// rather than dangling. So the count of photographs that are still FILED is
// asserted beside it: 95 photographs carried a place and an occasion before,
// 64 do afterwards, and the 31 that left are autumn-crossing's own, deleted
// with the trip. That is a count that must not fall further, which is the one
// assertion the three standing guards cannot make (SAF-MAJ-5).
func TestAfterTheCascadeNoPhotographNamesAVisitThatIsGone(t *testing.T) {
	before, after, db := deletedAutumnCrossing(t)
	ctx := context.Background()

	var dangling int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM photos p
		LEFT JOIN visits v ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
		WHERE p.visit_id IS NOT NULL AND v.id IS NULL`).Scan(&dangling); err != nil {
		t.Fatalf("the dangling-reference query: %v", err)
	}
	if dangling != 0 {
		t.Errorf("%d surviving photographs name a visit that is gone. No count moves when "+
			"this happens and the log is corrupt — it is the defect the client's own "+
			"`_repointed` exists for.", dangling)
	}

	filed := func(label, query string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		return n
	}
	// The positive control, and it is what makes the zero above mean
	// something. Both counts move by exactly the photographs the trip took
	// with it; neither falls further.
	if got := filed("filed", `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != 64 {
		t.Errorf("%d surviving photographs still name a place, want 64. Before the "+
			"cascade 95 did, and the 31 that left are autumn-crossing's own. A "+
			"dangling-reference count of zero is satisfied by unfiling every "+
			"photograph in the log.", got)
	}
	if got := filed("occasioned", `SELECT count(*) FROM photos WHERE visit_id IS NOT NULL`); got != 64 {
		t.Errorf("%d surviving photographs still name a visit, want 64", got)
	}
	// PD-06's standing assertion, in both directions. A photograph naming a
	// place but no occasion is half a record, and it is the half a count
	// cannot see.
	if got := filed("half-filed",
		`SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); got != 0 {
		t.Errorf("%d photographs name a place with no occasion, want 0", got)
	}
	if before["photos"] == after["photos"] {
		t.Errorf("the photograph count did not move at all (%d), so this leg's premise "+
			"— that a cascade ran — is false", before["photos"])
	}
}
