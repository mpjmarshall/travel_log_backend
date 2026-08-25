// T5's city against a real PostgreSQL. Test-first.
//
// This file needs a database and SKIPS, saying so, when there is none.
//
// WHAT ONLY THIS FILE CAN SAY:
//
//  1. THE ATTACHED CITY LANDS AT THE END OF AN ORDERED JOIN TABLE. `cityIds`
//     is an array on the wire and `trip_cities` underneath (DEC-64), so the
//     only thing between "Kyoto then Seoul" and "Seoul then Kyoto" is an
//     ordinal computed from the rows that are already there.
//  2. A RE-PUT DOES NOT ATTACH TWICE. `trip_cities_pkey` would refuse it, and
//     what a client sees is a 500 on a request that had already succeeded.
//  3. A WRITE THAT NAMES ONLY THE NAME LEAVES THE COUNTRY AND THE CENTRE
//     ALONE (DEC-89) — and the proposed INSERT tuple is checked against five
//     NOT NULL columns and four CHECKs before the conflict is resolved, so
//     "leave alone" here means "propose the stored value" rather than
//     "propose NULL".
//  4. AN UNKNOWN `attachTo` ROLLS THE WHOLE WRITE BACK, city included.
package postgres

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

func cityStore(t *testing.T) (CityStore, *sql.DB) {
	t.Helper()
	db := seeded(t)
	return CityStore{DB: db}, db
}

func itineraryOf(t *testing.T, db *sql.DB, tripID string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT city_id FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2 ORDER BY ordinal`,
		tid, tripID)
	if err != nil {
		t.Fatalf("reading the itinerary: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a city: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func aCityWrite(id, name string) logbook.CityWrite {
	return logbook.CityWrite{
		ID: ptr(id), Name: ptr(name),
		Country: &logbook.Country{Code: "JP", Name: "Japan"},
		Centre:  &logbook.LatLng{Lat: 34.69, Lng: 135.50},
	}
}

// === THE LEG WRITTEN FIRST ===

// A CITY CREATED WITH `attachTo` JOINS THAT TRIP'S ITINERARY AT THE END, AND
// THE ANSWER IS THE WHOLE LOG.
//
// The order is the client's own — `t.withCities([...t.cityIds, id])` — and it
// is travel order rather than a set: T1 and T4 draw the itinerary in the order
// it was walked, so prepending would silently reorder somebody's trip.
func TestACityAttachedToATripLandsAtTheEndAndAnswersTheWholeLog(t *testing.T) {
	store, db := cityStore(t)

	before := itineraryOf(t, db, "autumn-crossing")
	if strings.Join(before, ",") != "kyoto,seoul" {
		t.Fatalf("the fixture's itinerary is %v, want [kyoto seoul]", before)
	}

	write := aCityWrite("osaka", "Osaka")
	write.AttachTo = ptr("autumn-crossing")
	written, err := store.PutCity(context.Background(), tid, write)
	if err != nil {
		t.Fatalf("PutCity with attachTo: %v", err)
	}
	if written.Document == nil {
		t.Fatalf("a city attached to a trip answered no document. TWO entities moved — " +
			"the city was created AND the trip's cityIds grew — and a phone handed " +
			"only the city would have to re-derive the itinerary from its own copy " +
			"of the rule")
	}
	if written.City.ID != "osaka" || written.City.Country.Code != "JP" {
		t.Errorf("the city in the answer is %+v", written.City)
	}
	if got := itineraryOf(t, db, "autumn-crossing"); strings.Join(got, ",") != "kyoto,seoul,osaka" {
		t.Errorf("the itinerary is %v, want [kyoto seoul osaka]. The client appends at the "+
			"END, and the order is travel order rather than a set", got)
	}

	// And the answered DOCUMENT agrees with the rows, which is what the phone
	// splices. A store that wrote the row and answered a stale document would
	// pass the assertion above.
	for _, trip := range written.Document.Trips {
		if trip.ID != "autumn-crossing" {
			continue
		}
		if strings.Join(trip.CityIDs, ",") != "kyoto,seoul,osaka" {
			t.Errorf("the answered document has cityIds %v", trip.CityIDs)
		}
	}
}

// A CITY CREATED WITHOUT `attachTo` BELONGS TO NO TRIP AND THE ANSWER IS THE
// CITY. Both halves fail on their own.
func TestACityCreatedWithNoAttachToBelongsToNoTripAndAnswersTheCity(t *testing.T) {
	store, db := cityStore(t)

	written, err := store.PutCity(context.Background(), tid, aCityWrite("osaka", "Osaka"))
	if err != nil {
		t.Fatalf("PutCity: %v", err)
	}
	if written.Document != nil {
		t.Errorf("a city that joined no trip answered the whole envelope. Nothing but the " +
			"city moved, and sending the whole log back on every rename is what the " +
			"conditional read exists to avoid")
	}
	if written.City.Name != "Osaka" {
		t.Errorf("the answer is %+v", written.City)
	}
	if n := count(t, db, `SELECT count(*) FROM trip_cities WHERE city_id = 'osaka'`); n != 0 {
		t.Errorf("osaka is on %d itineraries, want none", n)
	}
	if written.Version < 1 {
		t.Errorf("version = %d, want a bump: cities are in the emitted document", written.Version)
	}
}

// A RE-PUT DOES NOT ATTACH TWICE, which is what makes the route retriable.
//
// `trip_cities_pkey` is (traveller_id, trip_id, city_id), so a second append
// violates it and reaches the client as a 500 on a request that had already
// succeeded — and the row it would have added is a second entry for a city
// already on the itinerary.
func TestReAttachingACityThatIsAlreadyOnTheItineraryChangesNothing(t *testing.T) {
	store, db := cityStore(t)
	ctx := context.Background()

	write := aCityWrite("osaka", "Osaka")
	write.AttachTo = ptr("autumn-crossing")
	if _, err := store.PutCity(ctx, tid, write); err != nil {
		t.Fatalf("the first attach: %v", err)
	}
	if _, err := store.PutCity(ctx, tid, write); err != nil {
		t.Fatalf("the second attach: %v — a PUT on a client-minted key is idempotent by "+
			"construction and a retry must not be a 500", err)
	}
	if got := itineraryOf(t, db, "autumn-crossing"); strings.Join(got, ",") != "kyoto,seoul,osaka" {
		t.Errorf("the itinerary is %v after two identical writes, want [kyoto seoul osaka]", got)
	}
}

// A WRITE THAT NAMES ONLY THE NAME LEAVES EVERYTHING ELSE ALONE (DEC-89).
//
// This is the body a rename sends, and it is the branch that breaks loudly if
// the upsert proposes NULLs: the tuple is checked against five NOT NULL
// columns and `cities_country_code_ck` BEFORE the conflict is resolved.
func TestRenamingACityLeavesItsCountryAndItsCentreAlone(t *testing.T) {
	store, db := cityStore(t)

	written, err := store.PutCity(context.Background(), tid, logbook.CityWrite{
		ID: ptr("kyoto"), Name: ptr("Kyōto"),
	})
	if err != nil {
		t.Fatalf("the rename: %v\n"+
			"    an unsent field cannot propose NULL: the INSERT tuple is validated\n"+
			"    before ON CONFLICT resolves it, so it has to propose the stored value", err)
	}
	if written.City.Name != "Kyōto" {
		t.Errorf("name = %q, want the new one — without this the leg below passes against "+
			"a write that did nothing at all", written.City.Name)
	}
	if written.City.Country.Code != "JP" || written.City.Country.Name != "Japan" {
		t.Errorf("country = %+v, want JP/Japan untouched", written.City.Country)
	}
	if written.City.Centre.Lat != 35.01 || written.City.Centre.Lng != 135.76 {
		t.Errorf("centre = %+v, want 35.01/135.76 untouched", written.City.Centre)
	}
	if n := count(t, db, `SELECT count(*) FROM cities WHERE id='kyoto' AND country_code='JP'`); n != 1 {
		t.Errorf("the stored row disagrees with the answer")
	}
}

// AN UNKNOWN `attachTo` IS A 422 NAMING THE FIELD, AND THE CITY IS NOT WRITTEN
// EITHER.
//
// It is a 422 rather than a 404 because of which thing the request is about:
// the path names the CITY and the trip is a field of the body, which is how
// the client's own `createCity` treats it — it answers null without writing
// when `log.trip(attachTo) == null`.
func TestACityAttachedToATripThatIsNotInTheLogIsRefusedAndWritesNothing(t *testing.T) {
	store, db := cityStore(t)

	write := aCityWrite("osaka", "Osaka")
	write.AttachTo = ptr("no-such-trip")
	_, err := store.PutCity(context.Background(), tid, write)

	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "attachTo" {
		t.Fatalf("attaching to an unknown trip = %v, want an invalid_field on attachTo", err)
	}
	if n := count(t, db, `SELECT count(*) FROM cities WHERE id = 'osaka'`); n != 0 {
		t.Errorf("osaka was written anyway (%d rows). The refusal is inside the "+
			"transaction, so the city rides the rollback with the attach — a client "+
			"that got a 422 and a half-written city would have no way to tell", n)
	}
}

func TestACityCreatedWithoutItsRequiredFieldsIsRefusedByName(t *testing.T) {
	store, _ := cityStore(t)
	for _, tc := range []struct {
		field string
		write logbook.CityWrite
	}{
		{"name", logbook.CityWrite{ID: ptr("osaka"),
			Country: &logbook.Country{Code: "JP", Name: "Japan"}, Centre: &logbook.LatLng{}}},
		{"country", logbook.CityWrite{ID: ptr("osaka"), Name: ptr("Osaka"), Centre: &logbook.LatLng{}}},
		{"centre", logbook.CityWrite{ID: ptr("osaka"), Name: ptr("Osaka"),
			Country: &logbook.Country{Code: "JP", Name: "Japan"}}},
	} {
		_, err := store.PutCity(context.Background(), tid, tc.write)
		var invalid logbook.InvalidFieldError
		if !asInvalidField(err, &invalid) || invalid.Field != tc.field {
			t.Errorf("the create missing %s = %v, want an invalid_field on %s", tc.field, err, tc.field)
		}
	}
}

// A COVER THAT HAS NOT BEEN UPLOADED IS REFUSED BY NAME, which is R3's
// `uploaded_at IS NOT NULL` rule reaching R6's entity: the four foreign keys
// guarantee the ROW exists and say nothing about whether the bytes landed.
func TestACityCoverMustNameACommittedObject(t *testing.T) {
	store, db := cityStore(t)

	write := aCityWrite("osaka", "Osaka")
	write.CoverAsset = ptr(ptr(assetA))
	_, err := store.PutCity(context.Background(), tid, write)

	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) || invalid.Field != "coverAsset" {
		t.Fatalf("a cover naming an uncommitted object = %v, want an invalid_field on coverAsset", err)
	}

	mustExec(t, db, `UPDATE media_objects SET uploaded_at = now() WHERE traveller_id=$1::uuid AND id=$2`, tid, assetA)
	written, err := store.PutCity(context.Background(), tid, write)
	if err != nil {
		t.Fatalf("the same cover once the object is committed: %v", err)
	}
	if written.City.CoverAsset == nil || *written.City.CoverAsset != assetA {
		t.Errorf("coverAsset = %v, want %s", written.City.CoverAsset, assetA)
	}
}

// DEC-57 IS ALREADY GUARDED AND THIS STEP DOES NOT GUARD IT TWICE.
//
// R6 adds no `DELETE /v1/cities/{id}` route: the client has no delete-a-city
// control, so no sheet copy authorises the cascade, and a cascade here would
// be the largest destructive act in the application — every place in the city,
// every photograph taken there across every trip, every walk.
//
// A LEG ASSERTING THAT THE DATABASE REFUSES IT WAS WRITTEN HERE AND DELETED,
// and the reason is worth more than the leg would have been.
// `TestDeletingACityIsRefusedByEveryChildThatPointsAtIt` in schema_test.go
// already does it, and does it BETTER: it removes the other three children one
// at a time so each of the four foreign keys is in turn the one that fires,
// where a single DELETE against the whole fixture only ever proves whichever
// key PostgreSQL happens to check first. Measured while writing the duplicate:
// against `seeded`, the refusal names `trip_cities_city_fk` and NOT
// `places_city_fk` — so a leg asserting the latter would have been red against
// correct work. R6's own acceptance check asserts exactly that string, and the
// discrepancy is reported there rather than papered over here.
