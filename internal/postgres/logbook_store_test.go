// The logbook store against a real PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func logbookStore(t *testing.T) (LogbookStore, *sql.DB, string) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("applying 0001: %v", err)
	}
	return LogbookStore{DB: db}, db, schema
}

// aTraveller registers one through the auth store, so the row is made the way
// the running server makes it — logbook_version and all.
func aTraveller(t *testing.T, db *sql.DB) string {
	t.Helper()
	tr, err := AuthStore{DB: db}.CreateTraveller(context.Background(),
		"matt@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("creating a traveller: %v", err)
	}
	return tr.ID
}

func aCity(t *testing.T, db *sql.DB, travellerID, id, name string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO cities (traveller_id, id, name, country_code, country_name, centre_lat, centre_lng)
		 VALUES ($1::uuid, $2, $3, 'JP', 'Japan', 35.0116, 135.7681)`,
		travellerID, id, name); err != nil {
		t.Fatalf("inserting the city %s: %v", id, err)
	}
}

// aMediaObject is a COMMITTED object, and `uploaded_at` is what makes it one.
func aMediaObject(t *testing.T, db *sql.DB, travellerID, id string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO media_objects (traveller_id, id, byte_size, content_type, uploaded_at)
		 VALUES ($1::uuid, $2, 1024, 'image/png', now())`, travellerID, id); err != nil {
		t.Fatalf("inserting the media object %s: %v", id, err)
	}
}

// aBegunMediaObject is the other half: a row that exists and whose bytes have
// not landed.
func aBegunMediaObject(t *testing.T, db *sql.DB, travellerID, id string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO media_objects (traveller_id, id, byte_size, content_type)
		 VALUES ($1::uuid, $2, 1024, 'image/png')`, travellerID, id); err != nil {
		t.Fatalf("beginning the media object %s: %v", id, err)
	}
}

const anAsset = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// day, text and ptr are the pointer contract at the call site.
func day(t *testing.T, s string) **logbook.Instant {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %s: %v", s, err)
	}
	i := logbook.At(parsed)
	p := &i
	return &p
}

func text(s string) **string {
	p := &s
	return &p
}

func ptr[T any](v T) *T { return &v }

func always(int64) bool { return true }
func never(int64) bool  { return false }

// the one leg in no mutation reddened, argued rather than left unsaid.
func TestLogbookStoreIsTheStoreTheDomainDeclared(t *testing.T) {
	var _ logbook.Store = LogbookStore{}
}

func TestAFreshTravellersLogIsEmptyAtVersionZero(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	snap, err := store.Read(context.Background(), id, always)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Version != 0 {
		t.Errorf("version = %d, want 0 — a traveller who has never written has "+
			"nothing to validate a cached body against", snap.Version)
	}
	if snap.Document == nil {
		t.Fatalf("Document is nil when assemble said yes")
	}
	if n := len(snap.Document.Trips) + len(snap.Document.Cities) + len(snap.Document.Places) +
		len(snap.Document.Photos) + len(snap.Document.Walks); n != 0 {
		t.Errorf("a fresh log holds %d entities, want 0", n)
	}
	if snap.Document.Traveller != nil {
		t.Errorf("traveller = %+v, want nil while the name is unset", snap.Document.Traveller)
	}
}

func TestAnUnknownTravellerAnswersTheDomainsOwnSentinel(t *testing.T) {
	store, _, _ := logbookStore(t)

	_, err := store.Read(context.Background(), "00000000-0000-0000-0000-000000000000", always)
	if !errors.Is(err, logbook.ErrNoTraveller) {
		t.Errorf("Read(an unknown traveller) = %v, want logbook.ErrNoTraveller — the "+
			"handler must not have to know postgres's sentinels", err)
	}
}

// the 304 leg, and it is decisive rather than counted.
func TestA304ReadTouchesNoTableButTravellers(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`DROP TABLE trip_cities, visits, photos, walks, places, trips, cities CASCADE`); err != nil {
		t.Fatalf("dropping the entity tables: %v", err)
	}

	snap, err := store.Read(ctx, id, never)
	if err != nil {
		t.Fatalf("a refused read failed against a schema holding only travellers: %v\n"+
			"    the 304 path reached a table it has no business reading", err)
	}
	if snap.Document != nil {
		t.Errorf("Document is non-nil after assemble said no; the 304 path assembled the log")
	}
	if snap.Version != 0 {
		t.Errorf("version = %d, want 0", snap.Version)
	}

	if _, err := store.Read(ctx, id, always); err == nil {
		t.Errorf("an ASSEMBLING read succeeded with no trips table, so the leg above " +
			"proves nothing about which queries the 304 skipped")
	}
}

func TestAnAssembledReadCarriesTheOrderedCityIDs(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	for _, city := range []string{"kyoto", "matsumoto", "seoul"} {
		aCity(t, db, id, city, strings.ToUpper(city))
	}

	want := []string{"seoul", "kyoto", "matsumoto"}
	if _, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr(want),
	}); err != nil {
		t.Fatalf("PutTrip: %v", err)
	}

	snap, err := store.Read(context.Background(), id, always)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Document == nil {
		t.Fatalf("Document is nil when assemble said yes")
	}
	if len(snap.Document.Trips) != 1 {
		t.Fatalf("read %d trips, want 1", len(snap.Document.Trips))
	}
	got := snap.Document.Trips[0].CityIDs
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("cityIds = %v, want %v — the list is in travel order and "+
			"`ORDER BY ordinal` is the only thing keeping it", got, want)
	}
}

// A `date` scanned as anything but midnight UTC is a day out on the wire, and
// no key-set assertion can see it.
func TestTheDateColumnsComeBackAsMidnightUTC(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	if _, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
		Start: day(t, "2027-09-17T00:00:00.000Z"),
		End:   day(t, "2027-10-02T00:00:00.000Z"),
	}); err != nil {
		t.Fatalf("PutTrip: %v", err)
	}

	snap, err := store.Read(context.Background(), id, always)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Document == nil || len(snap.Document.Trips) != 1 {
		t.Fatalf("read %+v, want one trip", snap.Document)
	}
	trip := snap.Document.Trips[0]

	for _, field := range []struct {
		name string
		got  *logbook.Instant
		want string
	}{
		{"start", trip.Start, "2027-09-17T00:00:00.000Z"},
		{"end", trip.End, "2027-10-02T00:00:00.000Z"},
	} {
		if field.got == nil {
			t.Errorf("%s came back nil", field.name)
			continue
		}
		raw, err := field.got.MarshalJSON()
		if err != nil {
			t.Fatalf("marshalling %s: %v", field.name, err)
		}
		if got := strings.Trim(string(raw), `"`); got != field.want {
			t.Errorf("%s = %s, want %s", field.name, got, field.want)
		}
	}
}

// this leg changed its mind at R1 and that is the interesting half.
func TestPutTripWritesWhatWasSentAndLeavesTheRestAlone(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	aCity(t, db, id, "kyoto", "Kyoto")
	aCity(t, db, id, "seoul", "Seoul")

	created, v1, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"kyoto"}),
		Summary: text("Down the length of Japan"),
	})
	if err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	if created.Name != "Autumn crossing" || len(created.CityIDs) != 1 {
		t.Errorf("created = %+v", created)
	}

	replaced, v2, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing, again"), CityIDs: ptr([]string{"seoul", "kyoto"}),
	})
	if err != nil {
		t.Fatalf("PutTrip (replace): %v", err)
	}
	if replaced.Name != "Autumn crossing, again" {
		t.Errorf("name = %q, want the replacement", replaced.Name)
	}
	if replaced.Summary == nil || *replaced.Summary != "Down the length of Japan" {
		t.Errorf("summary = %v, want the one that was never re-sent — DEC-89: a key "+
			"that is not in the body is a key the write has nothing to say about",
			replaced.Summary)
	}
	if strings.Join(replaced.CityIDs, ",") != "seoul,kyoto" {
		t.Errorf("cityIds = %v, want [seoul kyoto]", replaced.CityIDs)
	}
	if v2 != v1+1 {
		t.Errorf("logbook_version went %d -> %d, want one bump per write", v1, v2)
	}
}

// The other half of the pointer contract: sent-as-null still clears.
func TestASentNullClearsTheFieldItNames(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
		Summary: text("Down the length of Japan"),
		Start:   day(t, "2027-09-18T00:00:00.000Z"),
	}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}

	cleared, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID:      ptr("autumn-crossing"),
		Summary: ptr[*string](nil),
		Start:   ptr[*logbook.Instant](nil),
	})
	if err != nil {
		t.Fatalf("PutTrip (clear): %v", err)
	}
	if cleared.Summary != nil {
		t.Errorf("summary = %q after a sent null, want nil", *cleared.Summary)
	}
	if cleared.Start != nil {
		t.Errorf("start = %v after a sent null, want nil", cleared.Start)
	}
	if cleared.Name != "Autumn crossing" {
		t.Errorf("name = %q — the body named neither the name nor the trip's cities, "+
			"so neither may have moved", cleared.Name)
	}
}

// SF6, and it is the acceptance check: a PUT body carrying shareCoordinates
// leaves the stored flag unchanged.
func TestPutTripNeverTouchesTheSharingFields(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{ID: ptr("kyoto"), Name: ptr("Kyoto")}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE trips SET share_photos = true, share_notes = true, share_coordinates = true
		 WHERE traveller_id = $1::uuid AND id = 'kyoto'`, id); err != nil {
		t.Fatalf("setting the sharing flags: %v", err)
	}

	var body logbook.TripWrite
	if err := json.Unmarshal([]byte(
		`{"id":"kyoto","name":"Kyoto again","shareCoordinates":true}`), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	trip, _, err := store.PutTrip(ctx, id, body)
	if err != nil {
		t.Fatalf("PutTrip (replace): %v", err)
	}

	if !trip.SharePhotos || !trip.ShareNotes || !trip.ShareCoordinates {
		t.Errorf("the sharing flags came back %v/%v/%v, want all true — the write reset a "+
			"group it does not own", trip.SharePhotos, trip.ShareNotes, trip.ShareCoordinates)
	}
	var photos, notes, coordinates bool
	if err := db.QueryRowContext(ctx,
		`SELECT share_photos, share_notes, share_coordinates FROM trips
		 WHERE traveller_id = $1::uuid AND id = 'kyoto'`, id).
		Scan(&photos, &notes, &coordinates); err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if !photos || !notes || !coordinates {
		t.Errorf("the STORED flags are %v/%v/%v, want all true", photos, notes, coordinates)
	}
}

func TestPutTripRefusesACityTheTravellerDoesNotHold(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	_, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"atlantis"}),
	})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("PutTrip(an unknown city) = %v (%T), want a named field", err, err)
	}
	if invalid.Field != "cityIds" {
		t.Errorf("field = %q, want %q", invalid.Field, "cityIds")
	}
}

// two ways for A cover to be wrong, and the route answers both the same way.
func TestPutTripRefusesACoverThatWasNeverUploaded(t *testing.T) {
	for _, c := range []struct {
		name  string
		begin bool
	}{
		{"an object nothing holds", false},
		{"an object begun and never uploaded", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, db, _ := logbookStore(t)
			id := aTraveller(t, db)
			if c.begin {
				aBegunMediaObject(t, db, id, anAsset)
			}

			_, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
				ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CoverAsset: text(anAsset),
			})
			var invalid logbook.InvalidFieldError
			if !errors.As(err, &invalid) {
				t.Fatalf("PutTrip(%s) = %v (%T), want a named field", c.name, err, err)
			}
			if invalid.Field != "coverAsset" {
				t.Errorf("field = %q, want %q", invalid.Field, "coverAsset")
			}
		})
	}
}

func TestPutTripAcceptsACoverThatHasBeenUploaded(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	aMediaObject(t, db, id, anAsset)

	trip, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CoverAsset: text(anAsset),
	})
	if err != nil {
		t.Fatalf("PutTrip: %v", err)
	}
	if trip.CoverAsset == nil || *trip.CoverAsset != anAsset {
		t.Errorf("coverAsset = %v, want %s", trip.CoverAsset, anAsset)
	}
}

// A reorder is the case the mandated write strategy exists for.
func TestReorderingTheCitiesOfATripDoesNotCollide(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	for _, city := range []string{"kyoto", "seoul"} {
		aCity(t, db, id, city, city)
	}
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"kyoto", "seoul"}),
	}); err != nil {
		t.Fatalf("PutTrip (first order): %v", err)
	}
	trip, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"seoul", "kyoto"}),
	})
	if err != nil {
		t.Fatalf("PutTrip (reversed): %v", err)
	}
	if strings.Join(trip.CityIDs, ",") != "seoul,kyoto" {
		t.Errorf("cityIds = %v, want [seoul kyoto]", trip.CityIDs)
	}
}

func TestAFailedWriteLeavesNoConnectionCheckedOut(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	if _, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"atlantis"}),
	}); err == nil {
		t.Fatalf("PutTrip(an unknown city) succeeded")
	}
	if inUse := db.Stats().InUse; inUse != 0 {
		t.Errorf("%d connection(s) still checked out after a failed write, want 0 — "+
			"an assertion reading through a SECOND connection cannot see an open "+
			"transaction, which is how VS5 shipped one", inUse)
	}
}

func TestAFailedWriteMovesNoVersion(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	before := versionOf(t, db, id)
	if _, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CityIDs: ptr([]string{"atlantis"}),
	}); err == nil {
		t.Fatalf("PutTrip(an unknown city) succeeded")
	}
	if after := versionOf(t, db, id); after != before {
		t.Errorf("logbook_version went %d -> %d on a write that did not land; the bump "+
			"is taken FIRST and must ride the rollback", before, after)
	}
}

func TestPutTripForAnUnknownTravellerAnswersTheDomainsSentinel(t *testing.T) {
	store, _, _ := logbookStore(t)

	_, _, err := store.PutTrip(context.Background(), "00000000-0000-0000-0000-000000000000",
		logbook.TripWrite{ID: ptr("kyoto"), Name: ptr("Kyoto")})
	if !errors.Is(err, logbook.ErrNoTraveller) {
		t.Errorf("PutTrip(an unknown traveller) = %v, want logbook.ErrNoTraveller", err)
	}
}

// the shipped defect, reproduced.
func TestRenamingATripLeavesItsItineraryAndItsDatesAlone(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	for _, city := range []string{"kyoto", "osaka", "seoul"} {
		aCity(t, db, id, city, strings.ToUpper(city))
	}
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID:      ptr("autumn-crossing"),
		Name:    ptr("Autumn crossing"),
		CityIDs: ptr([]string{"kyoto", "osaka", "seoul"}),
		Start:   day(t, "2027-09-18T00:00:00.000Z"),
		End:     day(t, "2027-10-02T00:00:00.000Z"),
	}); err != nil {
		t.Fatalf("PutTrip: %v", err)
	}

	got, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID:   ptr("autumn-crossing"),
		Name: ptr("Autumn crossing, renamed"),
	})
	if err != nil {
		t.Fatalf("PutTrip rename: %v", err)
	}

	if len(got.CityIDs) != 3 {
		t.Errorf("cityIds = %v after a name-only PUT, want three — a rename that "+
			"empties the itinerary answers 200 and Trip.phaseAt then reads hasDates, "+
			"so the trip silently leaves its year section on T1 and T2", got.CityIDs)
	}
	if got.Start == nil || got.End == nil {
		t.Errorf("start/end = %v/%v after a name-only PUT, want both intact — "+
			"D3's own subtitle would then read 'No dates yet, 0 cities' about a "+
			"trip that had three", got.Start, got.End)
	}
	if got.Name != "Autumn crossing, renamed" {
		t.Errorf("name = %q, want the new one — the point is that the field that "+
			"WAS sent still gets written", got.Name)
	}
	if n := countTripCities(t, db, id, "autumn-crossing"); n != 3 {
		t.Errorf("trip_cities holds %d rows, want 3 — the response and the table "+
			"must agree, and asserting only on the response would pass against a "+
			"handler that echoes its input", n)
	}
}

func countTripCities(t *testing.T, db *sql.DB, travellerID, tripID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2`,
		travellerID, tripID).Scan(&n); err != nil {
		t.Fatalf("counting trip_cities: %v", err)
	}
	return n
}

// the assertion's three standing guards cannot make: a count that must not
// fall.
func filedPhotographs(t *testing.T, db *sql.DB, travellerID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM photos WHERE traveller_id = $1::uuid AND place_id IS NOT NULL`,
		travellerID).Scan(&n); err != nil {
		t.Fatalf("counting filed photographs: %v", err)
	}
	return n
}

func TestATripWriteNeverLowersTheCountOfFiledPhotographs(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()
	aCity(t, db, id, "kyoto", "Kyoto")
	aMediaObject(t, db, id, anAsset)

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
		CityIDs: ptr([]string{"kyoto"}),
	}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	aPlace(t, db, id, "fushimi", "kyoto")
	aVisit(t, db, id, "visit-1", "fushimi", "autumn-crossing")
	aFiledPhoto(t, db, id, "photo-1", "autumn-crossing", "kyoto", "fushimi", "visit-1")
	aFiledPhoto(t, db, id, "photo-2", "autumn-crossing", "kyoto", "fushimi", "visit-1")

	before := filedPhotographs(t, db, id)
	if before != 2 {
		t.Fatalf("the fixture filed %d photographs, want 2 — a count assertion whose "+
			"before-value is 0 cannot fall", before)
	}

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing, renamed"),
	}); err != nil {
		t.Fatalf("PutTrip (rename): %v", err)
	}

	if after := filedPhotographs(t, db, id); after != before {
		t.Errorf("filed photographs went %d -> %d across a rename — this is the "+
			"assertion the dangling-reference, place-without-occasion and "+
			"pair-agreement checks are all blind to, because an unfiled "+
			"photograph is a COHERENT log that is not the same log", before, after)
	}
}

func aPlace(t *testing.T, db *sql.DB, travellerID, id, cityID string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO places (traveller_id, id, city_id, name, lat, lng)
		 VALUES ($1::uuid, $2, $3, $2, 34.9671, 135.7727)`,
		travellerID, id, cityID); err != nil {
		t.Fatalf("inserting the place %s: %v", id, err)
	}
}

func aVisit(t *testing.T, db *sql.DB, travellerID, id, placeID, tripID string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		 VALUES ($1::uuid, $2, $3, $4, 0, '2027-09-19T04:12:00Z')`,
		travellerID, id, placeID, tripID); err != nil {
		t.Fatalf("inserting the visit %s: %v", id, err)
	}
}

func aFiledPhoto(t *testing.T, db *sql.DB, travellerID, id, tripID, cityID, placeID, visitID string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO photos (traveller_id, id, trip_id, city_id, place_id, visit_id, taken_at, asset)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, '2027-09-19T04:12:00Z', $7)`,
		travellerID, id, tripID, cityID, placeID, visitID, anAsset); err != nil {
		t.Fatalf("inserting the photograph %s: %v", id, err)
	}
}

func TestACreateWithNoNameNamesTheFieldRatherThanViolatingNotNull(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	_, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Summary: text("no name at all"),
	})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("PutTrip(a create with no name) = %v (%T), want a named field — "+
			"absent means leave alone, and on a create there is nothing to leave, "+
			"so without this the answer is SQLSTATE 23502 as a 500", err, err)
	}
	if invalid.Field != "name" {
		t.Errorf("field = %q, want %q", invalid.Field, "name")
	}
}

// A partial date write is only half visible to ValidateTrip, and this is the
// other half.
func TestAPartialDateWriteThatWouldInvertTheOrderNamesTheField(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
		Start: day(t, "2027-09-18T00:00:00.000Z"),
		End:   day(t, "2027-10-02T00:00:00.000Z"),
	}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}

	_, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID:    ptr("autumn-crossing"),
		Start: day(t, "2027-12-01T00:00:00.000Z"),
	})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("PutTrip(a start after the stored end) = %v (%T), want a named field", err, err)
	}
	if invalid.Field != "end" {
		t.Errorf("field = %q, want %q — the field the sheet can point at is the one "+
			"the pair disagrees about", invalid.Field, "end")
	}

	var stored sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT started_on FROM trips WHERE traveller_id = $1::uuid AND id = 'autumn-crossing'`,
		id).Scan(&stored); err != nil {
		t.Fatalf("reading the trip back: %v", err)
	}
	if !stored.Valid || stored.Time.Month() != time.September {
		t.Errorf("started_on = %v, want the September date — a refused write writes nothing",
			stored)
	}
}

// The write response is a whole Trip the phone SPLICES into its cached log.
func TestARenamedTripStillReportsThatItIsShared(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
	}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	aLiveShareLink(t, db, id, "autumn-crossing", "kyoto-9f2a")

	got, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing, renamed"),
	})
	if err != nil {
		t.Fatalf("PutTrip (rename): %v", err)
	}
	if !got.Shared {
		t.Errorf("shared = false on a trip whose share_links row is un-revoked — " +
			"the phone splices this answer over its cache, so H1 loses the URL AND " +
			"the only control that revokes it, from an action that has nothing to " +
			"do with sharing")
	}

	revokeShareLink(t, db, id, "autumn-crossing")
	after, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
	})
	if err != nil {
		t.Fatalf("PutTrip (after revocation): %v", err)
	}
	if after.Shared {
		t.Errorf("shared = true after revocation — the field must be DERIVED from " +
			"share_links and not stored, or it is a second place for the same fact")
	}
}

// The whole-log read has to agree with the write's answer, or the splice and
// the next fetch disagree about the same trip.
func TestTheWholeLogAgreesWithTheWriteAboutSharing(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	for _, trip := range []string{"shared-one", "private-one"} {
		if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
			ID: ptr(trip), Name: ptr(trip),
		}); err != nil {
			t.Fatalf("PutTrip(%s): %v", trip, err)
		}
	}
	aLiveShareLink(t, db, id, "shared-one", "token-live")
	aRevokedShareLink(t, db, id, "private-one", "token-dead")

	snap, err := store.Read(ctx, id, always)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := map[string]bool{"shared-one": true, "private-one": false}
	for _, trip := range snap.Document.Trips {
		if trip.Shared != want[trip.ID] {
			t.Errorf("%s: shared = %v, want %v — a revoked link is still a row, so "+
				"the subquery has to say `revoked_at IS NULL` and not just EXISTS",
				trip.ID, trip.Shared, want[trip.ID])
		}
	}
}

// It leaks no capability.
func TestTheSharedFlagCarriesNoToken(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
	}); err != nil {
		t.Fatalf("PutTrip: %v", err)
	}
	aLiveShareLink(t, db, id, "autumn-crossing", "kyoto-9f2a")

	snap, err := store.Read(ctx, id, always)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	envelope, err := logbook.Emit(logbook.FormatVersion, *snap.Document)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), "kyoto-9f2a") {
		t.Errorf("the emitted log carries the share token itself:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"shared":true`) {
		t.Errorf("the emitted log does not carry `\"shared\":true`:\n%s", raw)
	}
}

func aLiveShareLink(t *testing.T, db *sql.DB, travellerID, tripID, token string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1::uuid, $2, $3)`,
		travellerID, tripID, logbook.HashShareToken(token)); err != nil {
		t.Fatalf("inserting a share link for %s: %v", tripID, err)
	}
}

func aRevokedShareLink(t *testing.T, db *sql.DB, travellerID, tripID, token string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO share_links (traveller_id, trip_id, token_hash, revoked_at)
		 VALUES ($1::uuid, $2, $3, now())`,
		travellerID, tripID, logbook.HashShareToken(token)); err != nil {
		t.Fatalf("inserting a revoked share link for %s: %v", tripID, err)
	}
}

func revokeShareLink(t *testing.T, db *sql.DB, travellerID, tripID string) {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`UPDATE share_links SET revoked_at = now()
		 WHERE traveller_id = $1::uuid AND trip_id = $2 AND revoked_at IS NULL`,
		travellerID, tripID)
	if err != nil {
		t.Fatalf("revoking the link on %s: %v", tripID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("revoking the link on %s moved %d rows, want 1", tripID, n)
	}
}

// `v.At`, `p.TakenAt` and `w.RecordedOn` were read out of `sql.NullTime` with
// `.Valid` never CHECKED.
func TestANullDateColumnIsADriverErrorAndNotAYearOneDate(t *testing.T) {
	for _, tc := range []struct{ table, column, what string }{
		{"visits", "at", "a visit's date, which is the day its photographs were taken"},
		{"photos", "taken_at", "the day a photograph was taken"},
		{"walks", "recorded_on", "the day a track was recorded"},
	} {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			store, db, _ := logbookStore(t)
			id := aTraveller(t, db)
			ctx := context.Background()
			aCity(t, db, id, "kyoto", "Kyoto")
			aMediaObject(t, db, id, anAsset)
			if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
				ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
				CityIDs: ptr([]string{"kyoto"}),
			}); err != nil {
				t.Fatalf("PutTrip: %v", err)
			}
			aPlace(t, db, id, "fushimi", "kyoto")

			if _, err := db.ExecContext(ctx,
				`ALTER TABLE `+tc.table+` ALTER COLUMN `+tc.column+` DROP NOT NULL`); err != nil {
				t.Fatalf("dropping NOT NULL on %s.%s: %v", tc.table, tc.column, err)
			}
			insertWithNullDate(t, db, id, tc.table)

			_, err := store.Read(ctx, id, always)
			if err == nil {
				t.Fatalf("the read succeeded with a NULL %s.%s — the emitter then "+
					"writes 0001-01-01T00:00:00.000Z, which DateTime.parse accepts "+
					"happily and every screen renders as %s in year one",
					tc.table, tc.column, tc.what)
			}
		})
	}
}

// The control.
func TestTheSameFixtureReadsCleanlyWithTheConstraintIntact(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()
	aCity(t, db, id, "kyoto", "Kyoto")
	aMediaObject(t, db, id, anAsset)
	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
		CityIDs: ptr([]string{"kyoto"}),
	}); err != nil {
		t.Fatalf("PutTrip: %v", err)
	}
	aPlace(t, db, id, "fushimi", "kyoto")
	for _, table := range []string{"visits", "photos", "walks"} {
		insertDatedRow(t, db, id, table)
	}
	if _, err := store.Read(ctx, id, always); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

func insertWithNullDate(t *testing.T, db *sql.DB, travellerID, table string) {
	t.Helper()
	var statement string
	switch table {
	case "visits":
		statement = `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
			VALUES ($1::uuid, 'visit-null', 'fushimi', 'autumn-crossing', 0, NULL)`
	case "photos":
		statement = `INSERT INTO photos (traveller_id, id, trip_id, city_id, taken_at, asset)
			VALUES ($1::uuid, 'photo-null', 'autumn-crossing', 'kyoto', NULL, '` + anAsset + `')`
	case "walks":
		statement = `INSERT INTO walks (traveller_id, id, trip_id, city_id, recorded_on, distance_km, points)
			VALUES ($1::uuid, 'walk-null', 'autumn-crossing', 'kyoto', NULL, 1.2,
			        '[{"lat":35.0,"lng":135.0}]'::jsonb)`
	}
	if _, err := db.ExecContext(context.Background(), statement, travellerID); err != nil {
		t.Fatalf("inserting a NULL-dated row into %s: %v", table, err)
	}
}

func insertDatedRow(t *testing.T, db *sql.DB, travellerID, table string) {
	t.Helper()
	var statement string
	switch table {
	case "visits":
		statement = `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
			VALUES ($1::uuid, 'visit-ok', 'fushimi', 'autumn-crossing', 0, '2027-09-19T04:12:00Z')`
	case "photos":
		statement = `INSERT INTO photos (traveller_id, id, trip_id, city_id, taken_at, asset)
			VALUES ($1::uuid, 'photo-ok', 'autumn-crossing', 'kyoto', '2027-09-19T04:12:00Z', '` + anAsset + `')`
	case "walks":
		statement = `INSERT INTO walks (traveller_id, id, trip_id, city_id, recorded_on, distance_km, points)
			VALUES ($1::uuid, 'walk-ok', 'autumn-crossing', 'kyoto', '2027-09-19', 1.2,
			        '[{"lat":35.0,"lng":135.0}]'::jsonb)`
	}
	if _, err := db.ExecContext(context.Background(), statement, travellerID); err != nil {
		t.Fatalf("inserting a dated row into %s: %v", table, err)
	}
}

// the shape the client's own history says went wrong, and the fixture cannot
// express it.
func TestDeletingATripClearsAnotherTripsVisitReferenceAndNothingElse(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	var tripID, visitTrip string
	if err := db.QueryRowContext(ctx, `
		SELECT p.trip_id, v.trip_id FROM photos p JOIN visits v
		  ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
		WHERE p.traveller_id = $1 AND p.id = 'p-autumn'`, tid).Scan(&tripID, &visitTrip); err != nil {
		t.Fatalf("the premise failed: p-autumn does not name a visit: %v", err)
	}
	if tripID == visitTrip {
		t.Fatalf("the premise failed: p-autumn's visit belongs to its own trip (%s), so "+
			"deleting the OTHER trip cannot reach it", tripID)
	}

	before := photoRow(t, db, "p-autumn")
	if before.visitID != "v-fushimi-may" || before.placeID != "fushimi-inari" {
		t.Fatalf("the premise failed: p-autumn is filed at %q/%q", before.placeID, before.visitID)
	}

	if _, err := (LogbookStore{DB: db}).DeleteTrip(ctx, tid, "kyoto-in-may"); err != nil {
		t.Fatalf("DeleteTrip: %v", err)
	}

	after := photoRow(t, db, "p-autumn")
	if after.missing {
		t.Fatalf("p-autumn is gone. It belongs to autumn-crossing; deleting kyoto-in-may " +
			"must not take another trip's photograph, and a dangling-reference check " +
			"answers 0 either way.")
	}
	if after.visitID != "" {
		t.Errorf("p-autumn still names the visit %q, which went with kyoto-in-may.\n"+
			"    This is the cascade the client's own history says moved no count and\n"+
			"    left the log corrupt: `photos_visit_fk … ON DELETE SET NULL (visit_id)`\n"+
			"    is what clears it, and the COLUMN LIST is what makes it executable.",
			after.visitID)
	}
	if after.placeID != before.placeID {
		t.Errorf("p-autumn's place_id went from %q to %q. Only visit_id is in the column "+
			"list; clearing the pin as well is a photograph that has lost its place "+
			"because somebody else's trip was deleted.", before.placeID, after.placeID)
	}
	if after.tripID != before.tripID || after.cityID != before.cityID ||
		after.caption != before.caption || !after.takenAt.Equal(before.takenAt) {
		t.Errorf("p-autumn changed elsewhere: %+v -> %+v", before, after)
	}
}

// A delete that removes nothing moves no version, and that is the branch a
// retried delete takes.
func TestADeleteThatRemovesNothingIsSuccessAndMovesNoVersion(t *testing.T) {
	db := seeded(t)
	store := LogbookStore{DB: db}
	ctx := context.Background()

	first, err := store.DeleteTrip(ctx, tid, "kyoto-in-may")
	if err != nil {
		t.Fatalf("the first DeleteTrip: %v", err)
	}
	if first.Version < 1 {
		t.Fatalf("a delete that removed a trip left version %d", first.Version)
	}

	again, err := store.DeleteTrip(ctx, tid, "kyoto-in-may")
	if err != nil {
		t.Fatalf("deleting the same trip twice answered %v; a delete asks for something "+
			"to be absent, and an absent thing satisfies it", err)
	}
	if again.Version != first.Version {
		t.Errorf("a repeated delete moved logbook_version from %d to %d.\n"+
			"    Nothing changed, so nothing should have: a bump here invalidates the\n"+
			"    phone's whole cached document on every retry, and DEC-103 exists\n"+
			"    because deletes are exactly what gets retried.",
			first.Version, again.Version)
	}
	if again.Document == nil {
		t.Fatalf("a repeated delete answered no document")
	}
	if len(again.Document.Trips) != 1 || again.Document.Trips[0].ID != "autumn-crossing" {
		t.Errorf("the log came back holding %d trips, want just autumn-crossing",
			len(again.Document.Trips))
	}
}

// A trip of another traveller is an unknown trip, not A DELETE.
func TestOneTravellerCannotDeleteAnothersTrip(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'other@example.com','x')`, otherT)

	snap, err := (LogbookStore{DB: db}).DeleteTrip(ctx, otherT, "kyoto-in-may")
	if err != nil {
		t.Fatalf("DeleteTrip: %v", err)
	}
	if len(snap.Document.Trips) != 0 {
		t.Errorf("the stranger's log came back with %d trips", len(snap.Document.Trips))
	}
	if n := count(t, db, `SELECT count(*) FROM trips WHERE traveller_id=$1`, tid); n != 2 {
		t.Errorf("the owner has %d trips left, want 2 — a delete keyed on the id alone "+
			"reaches every traveller's row of that name", n)
	}
}

type storedPhoto struct {
	missing                          bool
	tripID, cityID, placeID, visitID string
	caption                          string
	takenAt                          time.Time
}

func photoRow(t *testing.T, db *sql.DB, id string) storedPhoto {
	t.Helper()
	var out storedPhoto
	var placeID, visitID, caption sql.NullString
	err := db.QueryRowContext(context.Background(), `
		SELECT trip_id, city_id, place_id, visit_id, caption, taken_at
		FROM photos WHERE traveller_id = $1 AND id = $2`, tid, id).
		Scan(&out.tripID, &out.cityID, &placeID, &visitID, &caption, &out.takenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedPhoto{missing: true}
	}
	if err != nil {
		t.Fatalf("reading the photograph %s: %v", id, err)
	}
	out.placeID, out.visitID, out.caption = placeID.String, visitID.String, caption.String
	return out
}

// the name lands, it is trimmed, and the version moves.
func TestSetTravellerNameWritesATrimmedNameAndMovesTheVersion(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()
	before := versionOf(t, db, id)

	named, version, err := store.SetTravellerName(ctx, id, "  Matt  ")
	if err != nil {
		t.Fatalf("SetTravellerName: %v", err)
	}
	if named.Name != "Matt" {
		t.Errorf("the answer carries %q, want %q", named.Name, "Matt")
	}
	if version <= before {
		t.Errorf("logbook_version went %d -> %d; the traveller's name is IN the emitted "+
			"document", before, version)
	}

	var stored string
	if err := db.QueryRowContext(ctx, `SELECT name FROM travellers WHERE id = $1::uuid`, id).
		Scan(&stored); err != nil {
		t.Fatalf("reading the name back: %v", err)
	}
	if stored != "Matt" {
		t.Errorf("the column holds %q, want %q — the answer and the row have to agree, "+
			"because the phone splices one and re-reads the other", stored, "Matt")
	}
}

// an empty name is a named field and not A constraint violation.
func TestSetTravellerNameRefusesAnEmptyNameAndKeepsTheOldOne(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.SetTravellerName(ctx, id, "Matt"); err != nil {
		t.Fatalf("the first SetTravellerName: %v", err)
	}
	before := versionOf(t, db, id)

	for _, name := range []string{"", "   ", "\t\n"} {
		_, _, err := store.SetTravellerName(ctx, id, name)
		var invalid logbook.InvalidFieldError
		if !errors.As(err, &invalid) {
			t.Errorf("SetTravellerName(%q) answered %v, want an InvalidFieldError", name, err)
			continue
		}
		if invalid.Field != "name" {
			t.Errorf("the refusal names %q, want \"name\"", invalid.Field)
		}
	}

	var stored string
	if err := db.QueryRowContext(ctx, `SELECT name FROM travellers WHERE id = $1::uuid`, id).
		Scan(&stored); err != nil {
		t.Fatalf("reading the name back: %v", err)
	}
	if stored != "Matt" {
		t.Errorf("the column holds %q after three refused writes, want %q — an empty "+
			"name is refused and is not a way to clear it", stored, "Matt")
	}
	if got := versionOf(t, db, id); got != before {
		t.Errorf("a refused rename moved logbook_version from %d to %d — the refusal is "+
			"taken before the transaction opens, so nothing should have committed",
			before, got)
	}
}

// A name longer than this build takes is the same kind of refusal, and it is
// the same ceiling a trip's name wears.
func TestSetTravellerNameRefusesANameLongerThanTheBuildTakes(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	_, _, err := store.SetTravellerName(context.Background(), id,
		strings.Repeat("n", logbook.MaxNameBytes+1))
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) || invalid.Field != "name" {
		t.Errorf("a %d-byte name answered %v, want an InvalidFieldError naming name",
			logbook.MaxNameBytes+1, err)
	}
}
