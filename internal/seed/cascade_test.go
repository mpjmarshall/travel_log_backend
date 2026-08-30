// D3's cascade, row for row, against the client's own log.
package seed_test

import (
	"context"
	"database/sql"
	"testing"

	"travellog/internal/postgres"
)

// deletedAutumnCrossing seeds the client's own log, deletes the trip D3's
// sheet was measured on through the store.
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

// sheet line 1 — "N photos and their notes", DELETED.
func TestD3DeletesTheTripsPhotographsAndTheirNotes(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["photos"] != 284 || after["photos"] != 188 {
		t.Errorf("photos %d -> %d, want 284 -> 188. The sheet says '96 photos and their "+
			"notes' are deleted, and the count is what the sheet itself computes from "+
			"the log.", before["photos"], after["photos"])
	}
}

// sheet line 2 — "N recorded walks", DELETED.
func TestD3DeletesTheTripsWalksAndLeavesTheOtherTripsAlone(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["walks"] != 2 || after["walks"] != 1 {
		t.Errorf("walks %d -> %d, want 2 -> 1. The sheet says '1 recorded walk' is "+
			"deleted; the other belongs to kyoto-in-may and D3 says nothing about it.",
			before["walks"], after["walks"])
	}
}

// sheet line 3 — "N pins in Busan, Kyoto and Seoul", KEPT.
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

// sheet line 3, the hard half — A pin left with no visits at all survives.
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

// sheet line 4 — "The shared link stops working", DEAD.
func TestD3TakesTheSharedLinkAndItsWholeHistoryWithTheTrip(t *testing.T) {
	before, after, _ := deletedAutumnCrossing(t)

	if before["share_links"] != 1 || after["share_links"] != 0 {
		t.Errorf("share_links %d -> %d, want 1 -> 0. The link is on the trip and D3 says "+
			"'The shared link stops working'; the revocation history goes with it, "+
			"which is accepted in 0004's comment.", before["share_links"], after["share_links"])
	}
}

// the rows the sheet does not itemise, because it does not have to.
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

// the SERVER-SIDE `_repointed`, and its second assertion is the whole point.
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
	if got := filed("filed", `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`); got != 64 {
		t.Errorf("%d surviving photographs still name a place, want 64. Before the "+
			"cascade 95 did, and the 31 that left are autumn-crossing's own. A "+
			"dangling-reference count of zero is satisfied by unfiling every "+
			"photograph in the log.", got)
	}
	if got := filed("occasioned", `SELECT count(*) FROM photos WHERE visit_id IS NOT NULL`); got != 64 {
		t.Errorf("%d surviving photographs still name a visit, want 64", got)
	}
	if got := filed("half-filed",
		`SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND visit_id IS NULL`); got != 0 {
		t.Errorf("%d photographs name a place with no occasion, want 0", got)
	}
	if before["photos"] == after["photos"] {
		t.Errorf("the photograph count did not move at all (%d), so this leg's premise "+
			"— that a cascade ran — is false", before["photos"])
	}
}
