// THE THREE ROW RULES, against a real PostgreSQL where a row either exists or
// does not (PD-07, docs/PUBLIC-ENVELOPE.md §5).
//
// WHAT ONLY THIS CAN SAY. internal/logbook's legs are about KEYS and FLAGS
// over a source somebody handed it; internal/httpapi's are about statuses,
// headers and the shape that leaves the process. Neither can see which rows a
// SELECT returned, and the leak PD-07 found is entirely a question of rows:
// every key in the leaking document is on the allowlist.
//
// THE FIXTURE IS BUILT FOR THE THREE CASES THAT LEAK, and each is asserted to
// exist before anything relies on it:
//
//	a WISHLIST place            in a city the shared trip visits, never been to
//	ANOTHER TRIP'S place        in the same city, visited on a different trip
//	a place visited on BOTH     which is the nested case, and the one a
//	                            place-level filter passes while it leaks
package postgres

import (
	"context"
	"errors"
	"testing"

	"travellog/internal/logbook"
)

// theSharedTrip is the trip the link in this file is for. `kyoto-in-may` is
// deliberately the OTHER one: seeded() already gives it the live link, so a
// filter that ignored the trip id entirely would answer this file's questions
// about the wrong trip and be visible.
const theSharedTrip = "autumn-crossing"

const otherTripToken = "mnpqrstuvwxy"

// sharedFixture extends seeded() with the three leaking shapes and a link on
// the trip under test.
//
// EVERYTHING IT ADDS IS IN A CITY THE SHARED TRIP ACTUALLY VISITS. A place in
// some unrelated city is filtered out by any implementation at all, including
// a wrong one, so a fixture built that way proves nothing.
func sharedFixture(t *testing.T) *logbook.PublicSource {
	t.Helper()
	db := seeded(t)

	// The place visited on BOTH trips. seeded() gives fushimi-inari one visit
	// on kyoto-in-may; this is its visit on the shared trip.
	//
	// THE ORDINALS ARE NEWEST FIRST, which is the client's own order and what
	// `visits_place_ordinal_uq` makes a fact about the row rather than about
	// the query. The September visit is the newer of the two, so it takes
	// ordinal 0 and May's moves to 1 — a place accumulates visits across
	// trips, which is the whole shape this file is about.
	mustExec(t, db, `UPDATE visits SET ordinal = 1 WHERE traveller_id=$1 AND id='v-fushimi-may'`, tid)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at, note)
		VALUES ($1,'v-fushimi-autumn','fushimi-inari',$2,0,'2027-09-20T07:05:00Z','the torii went on for ever')`,
		tid, theSharedTrip)

	// ANOTHER TRIP'S PLACE, in kyoto, which the shared trip does visit.
	mustExec(t, db, `INSERT INTO places (traveller_id, id, city_id, name, lat, lng)
		VALUES ($1,'nishiki','kyoto','Nishiki',35.00,135.76)`, tid)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-nishiki-may','nishiki','kyoto-in-may',0,'2027-05-04T07:05:00Z')`, tid)

	// A walk on the shared trip, and a DISMISSED one beside it.
	mustExec(t, db, `INSERT INTO walks (traveller_id, id, trip_id, city_id, recorded_on, distance_km, points, dismissed)
		VALUES ($1,'w-autumn',$2,'kyoto','2027-09-20',5.5,
		        '[{"lat":34.96,"lng":135.77},{"lat":34.97,"lng":135.78}]'::jsonb,false),
		       ($1,'w-discarded',$2,'kyoto','2027-09-21',0.4,
		        '[{"lat":34.96,"lng":135.77}]'::jsonb,true)`, tid, theSharedTrip)

	mustExec(t, db, `INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,$2,$3)`,
		tid, theSharedTrip, logbook.HashShareToken(otherTripToken))

	// THE FIXTURE IS ASSERTED BEFORE IT IS RELIED ON. Every number below is a
	// premise of a leg in this file, and a fixture that quietly stopped
	// holding one would make the leg pass while proving nothing.
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id='wishlist-pin'`); n != 0 {
		t.Fatalf("wishlist-pin has %d visits; this file needs a place nobody has been to", n)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE place_id='fushimi-inari'`); n != 2 {
		t.Fatalf("fushimi-inari has %d visits, want 2 — one on the shared trip and one "+
			"on another, which is the nested case a place-level filter passes", n)
	}
	if n := count(t, db, `SELECT count(*) FROM places WHERE city_id='kyoto'`); n != 3 {
		t.Fatalf("kyoto holds %d places, want 3 — the published one, a wishlist pin and "+
			"another trip's", n)
	}

	store := ShareReadStore{DB: db}
	link, err := store.ShareLink(context.Background(), logbook.HashShareToken(otherTripToken))
	if err != nil {
		t.Fatalf("ShareLink: %v", err)
	}
	src, err := store.PublicLog(context.Background(), link.TravellerID, link.TripID)
	if err != nil {
		t.Fatalf("PublicLog: %v", err)
	}
	return &src
}

// THE FAILING TEST THIS STEP WAS WRITTEN AGAINST (PD-07).
//
// Every key in the document a key-set walk sees is on the allowlist; the
// defect is WHICH ROWS are there. A place the traveller has never been to, and
// a place visited on an entirely different trip, both leak the shape of a
// private log through a link that was shared for one trip.
func TestThePublicEnvelopeCarriesOnlyTheSharedTripsOwnPlaces(t *testing.T) {
	src := sharedFixture(t)

	ids := map[string]bool{}
	for _, place := range src.Places {
		ids[place.ID] = true
	}

	if ids["wishlist-pin"] {
		t.Error("the public envelope carries wishlist-pin, a WISHLIST place — somewhere " +
			"the traveller has never been, published by a link shared for a trip. " +
			"Every key on it is on the allowlist; the ROW is the leak.")
	}
	if ids["nishiki"] {
		t.Error("the public envelope carries nishiki, visited only on another trip — " +
			"one link exposes the places of trips it was not shared for")
	}
	if len(src.Places) == 0 {
		t.Fatal("no places at all — a filter that removes everything satisfies both " +
			"assertions above and is not what the sheet promises")
	}
	if !ids["fushimi-inari"] {
		t.Errorf("the place the trip DID visit is missing: %v", ids)
	}
}

// THE NESTED CASE, WHICH THE LEG ABOVE PASSES WHILE IT LEAKS.
//
// A place correctly published because it is on the shared trip carries every
// OTHER trip's visits with it — their dates, and their notes. A key-set walk
// passes. A "no other trip's id appears" leg passes too, once `tripId` is off
// the allowlist, which it is.
func TestAPublishedPlaceCarriesOnlyTheSharedTripsVisits(t *testing.T) {
	src := sharedFixture(t)

	var published *logbook.Place
	for i, place := range src.Places {
		if place.ID == "fushimi-inari" {
			published = &src.Places[i]
		}
	}
	if published == nil {
		t.Fatal("fushimi-inari is not published at all; this leg needs the place that IS")
	}

	if len(published.Visits) != 1 {
		t.Fatalf("fushimi-inari published with %d visits, want 1 — a link shared for one "+
			"trip is publishing visits belonging to another, with their dates and "+
			"their notes", len(published.Visits))
	}
	if published.Visits[0].TripID != theSharedTrip {
		t.Errorf("published visit %s belongs to %s", published.Visits[0].ID, published.Visits[0].TripID)
	}
	// The visible half, before the leak: the client reads the first day's `at`
	// as when the place was last visited, so an unfiltered array dates the
	// public page to a trip the link was never shared for.
	if got := published.Visits[0].At.Time().Year(); got != 2027 {
		t.Errorf("the first day is dated %d", got)
	}
}

// PHOTOGRAPHS AND WALKS ARE `trip_id`, AND A DISMISSED WALK IS NOT PUBLISHED.
//
// N1's 'Discard' is an action the owner took to be rid of a track; publishing
// it would publish the thing they got rid of.
func TestThePublicEnvelopeCarriesOneTripsPhotographsAndNoDiscardedTrack(t *testing.T) {
	src := sharedFixture(t)

	for _, photo := range src.Photos {
		if photo.TripID != theSharedTrip {
			t.Errorf("photo %s belongs to %s", photo.ID, photo.TripID)
		}
	}
	if len(src.Photos) != 1 {
		t.Errorf("%d photographs, want 1 — the fixture holds one on each trip", len(src.Photos))
	}

	for _, walk := range src.Walks {
		if walk.ID == "w-discarded" {
			t.Error("a DISMISSED walk is published — N1's 'Discard' is an action the " +
				"owner took to be rid of that recording")
		}
		if walk.TripID != theSharedTrip {
			t.Errorf("walk %s belongs to %s", walk.ID, walk.TripID)
		}
	}
	if len(src.Walks) != 1 {
		t.Fatalf("%d walks, want 1 — a filter that publishes nothing satisfies the "+
			"assertion above", len(src.Walks))
	}
	if len(src.Walks[0].Points) != 2 {
		t.Errorf("the published walk carries %d points, want 2 — the track is read "+
			"through the same LATERAL the private document uses", len(src.Walks[0].Points))
	}
}

// THE CITIES ARE THE TRIP'S OWN, IN `trip_cities.ordinal` ORDER.
//
// Travel order is load-bearing on the wire and `ordinal` is the only thing
// that carries it — the private read orders cities by id, and the trip's own
// `cityIds` is what puts them back in the order somebody travelled.
func TestThePublicEnvelopeCarriesTheTripsCitiesInTravelOrder(t *testing.T) {
	src := sharedFixture(t)

	var got []string
	for _, city := range src.Cities {
		got = append(got, city.ID)
	}
	// seeded(): autumn-crossing is kyoto at ordinal 0 and seoul at 1. Sorted
	// by id that would be kyoto, seoul as well — so the leg asserts the TRIP's
	// own cityIds agree with it, which is the field the client reads.
	if len(got) != 2 || got[0] != "kyoto" || got[1] != "seoul" {
		t.Errorf("cities = %v, want [kyoto seoul] in trip_cities.ordinal order", got)
	}
	if len(src.Trip.CityIDs) != 2 || src.Trip.CityIDs[0] != "kyoto" {
		t.Errorf("trip.cityIds = %v, want the same order", src.Trip.CityIDs)
	}
}

// REVOKED AND UNKNOWN, AT THE STORE (PD-12, DEC-10).
//
// THE LOOKUP IS THE ONLY BRANCH. This method selects the row REGARDLESS of
// `revoked_at` and answers what it found, so the caller decides once and
// nothing downstream can tell "this token was once real" from "this token
// never existed" by what work was done. A store that filtered
// `AND revoked_at IS NULL` would be correct and would make that impossible to
// arrange, because the caller would have no way to answer both cases the same
// way.
func TestTheLookupAnswersARevokedRowRatherThanNothing(t *testing.T) {
	db := seeded(t)
	store := ShareReadStore{DB: db}
	ctx := context.Background()

	link, err := store.ShareLink(ctx, logbook.HashShareToken(tokenMay))
	if err != nil {
		t.Fatalf("ShareLink on a live token: %v", err)
	}
	if link.Revoked {
		t.Errorf("a live link reports revoked")
	}
	if link.TripID != "kyoto-in-may" {
		t.Errorf("the live link resolves to %q", link.TripID)
	}

	mustExec(t, db, `UPDATE share_links SET revoked_at = now() WHERE traveller_id=$1`, tid)

	revoked, err := store.ShareLink(ctx, logbook.HashShareToken(tokenMay))
	if err != nil {
		t.Fatalf("ShareLink on a revoked token answered %v, want the row.\n"+
			"    The lookup is the only branch: a store that hides a revoked row "+
			"leaves the caller unable to do the same work for both cases.", err)
	}
	if !revoked.Revoked {
		t.Error("a revoked link reports live — 'Stop sharing' is the only revocation " +
			"surface this API has")
	}

	if _, err := store.ShareLink(ctx, logbook.HashShareToken("nobodyeverheldthis")); err == nil {
		t.Error("a token nobody ever held resolved to a link")
	} else if !errors.Is(err, logbook.ErrNoShare) {
		t.Errorf("an unknown token answered %v, want logbook.ErrNoShare", err)
	}
}
