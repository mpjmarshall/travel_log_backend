// T5's city against a real PostgreSQL.
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

// A city created with `attachTo` joins that trip's itinerary at the end, and
// the answer is the whole log.
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

	for _, trip := range written.Document.Trips {
		if trip.ID != "autumn-crossing" {
			continue
		}
		if strings.Join(trip.CityIDs, ",") != "kyoto,seoul,osaka" {
			t.Errorf("the answered document has cityIds %v", trip.CityIDs)
		}
	}
}

// A city created without `attachTo` belongs to no trip and the answer is the
// city.
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

// A RE-put does not attach twice, which is what makes the route retriable.
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

// A write that names only the name leaves everything else alone.
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

// an unknown `attachTo` is a 422 naming the field, and the city is not
// written either.
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

// A cover that has not been uploaded is refused by name, which is R3's
// `uploaded_at is not null` rule reaching R6's entity.
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
