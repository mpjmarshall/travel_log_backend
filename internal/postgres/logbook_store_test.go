// The logbook store against a real PostgreSQL. Test-first.
//
// This file needs a database and SKIPS, saying so, when there is none.
//
// WHAT ONLY THIS FILE CAN SAY, and it is why the handler tests run against a
// fake instead of duplicating it:
//
//  1. THE ORDERED LIST SURVIVES STORAGE. cityIds is an ordered array on the
//     wire and a join table underneath (DEC-64), so the only thing standing
//     between "Kyoto then Seoul" and "Seoul then Kyoto" is `ORDER BY ordinal`
//     — and natural order agrees with it until the day a row is rewritten.
//  2. THE SHARING FIELDS SURVIVE A WRITE THAT DOES NOT NAME THEM (SF6).
//  3. THE 304 PATH READS NOTHING BUT travellers. Proven by dropping the five
//     entity tables and watching the refused read still succeed.
//  4. THE DATE COLUMNS COME BACK AS MIDNIGHT UTC. A `date` scanned wrong is a
//     day out, which no shape assertion can see.
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

func aMediaObject(t *testing.T, db *sql.DB, travellerID, id string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO media_objects (traveller_id, id, byte_size, content_type)
		 VALUES ($1::uuid, $2, 1024, 'image/png')`, travellerID, id); err != nil {
		t.Fatalf("inserting the media object %s: %v", id, err)
	}
}

const anAsset = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func day(t *testing.T, s string) *logbook.Instant {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %s: %v", s, err)
	}
	i := logbook.At(parsed)
	return &i
}

func text(s string) *string { return &s }

func always(int64) bool { return true }
func never(int64) bool  { return false }

func TestLogbookStoreIsTheStoreTheDomainDeclared(t *testing.T) {
	var _ logbook.Store = LogbookStore{}
}

// === the read ===

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

// THE 304 LEG, AND IT IS DECISIVE RATHER THAN COUNTED. DEC-31 says a 304
// costs one indexed row read; a document built and then thrown away saves
// bandwidth and no server work at all, which is half the point.
//
// The five entity tables are DROPPED and the read is then asked to refuse.
// pg_stat_all_tables was the first attempt and is the wrong instrument — its
// counters are collected asynchronously and cached per transaction, so the leg
// would have been a flake pretending to be a measurement. A table that is not
// there cannot be read from by accident.
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

	// The control. Without it the leg above is satisfied by a Read that never
	// touches those tables at all, which would also break the 200.
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

	// Deliberately NOT alphabetical, and deliberately not the insertion order
	// of the cities either: natural order agrees with ordinal until it does
	// not, and this is the arrangement where the two disagree.
	want := []string{"seoul", "kyoto", "matsumoto"}
	if _, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: want,
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
		ID: "autumn-crossing", Name: "Autumn crossing",
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

// === the write ===

func TestPutTripCreatesAndThenReplacesWholeState(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	aCity(t, db, id, "kyoto", "Kyoto")
	aCity(t, db, id, "seoul", "Seoul")

	created, v1, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"kyoto"},
		Summary: text("Down the length of Japan"),
	})
	if err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	if created.Name != "Autumn crossing" || len(created.CityIDs) != 1 {
		t.Errorf("created = %+v", created)
	}

	replaced, v2, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing, again", CityIDs: []string{"seoul", "kyoto"},
	})
	if err != nil {
		t.Fatalf("PutTrip (replace): %v", err)
	}
	if replaced.Name != "Autumn crossing, again" {
		t.Errorf("name = %q, want the replacement", replaced.Name)
	}
	if replaced.Summary != nil {
		t.Errorf("summary = %q, want nil — a whole-state upsert clears what the body omits",
			*replaced.Summary)
	}
	if strings.Join(replaced.CityIDs, ",") != "seoul,kyoto" {
		t.Errorf("cityIds = %v, want [seoul kyoto]", replaced.CityIDs)
	}
	if v2 != v1+1 {
		t.Errorf("logbook_version went %d -> %d, want one bump per write", v1, v2)
	}
}

// SF6, AND IT IS THE ACCEPTANCE CHECK: a PUT body carrying shareCoordinates
// leaves the stored flag unchanged. The body cannot even express it, so what
// this leg really proves is that the UPDATE does not name the three columns
// and reset them to their defaults.
func TestPutTripNeverTouchesTheSharingFields(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{ID: "kyoto", Name: "Kyoto"}); err != nil {
		t.Fatalf("PutTrip (create): %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE trips SET share_photos = true, share_notes = true, share_coordinates = true
		 WHERE traveller_id = $1::uuid AND id = 'kyoto'`, id); err != nil {
		t.Fatalf("setting the sharing flags: %v", err)
	}

	// The body a client sends when it means to rename the trip. It carries
	// `shareCoordinates: true` as the acceptance check asks; DEC-13 keeps
	// unknown fields tolerated, so it decodes and is not heard.
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
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"atlantis"},
	})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("PutTrip(an unknown city) = %v (%T), want a named field", err, err)
	}
	if invalid.Field != "cityIds" {
		t.Errorf("field = %q, want %q", invalid.Field, "cityIds")
	}
}

func TestPutTripRefusesACoverThatWasNeverUploaded(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	_, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CoverAsset: text(anAsset),
	})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("PutTrip(an unknown cover) = %v (%T), want a named field", err, err)
	}
	if invalid.Field != "coverAsset" {
		t.Errorf("field = %q, want %q", invalid.Field, "coverAsset")
	}
}

func TestPutTripAcceptsACoverThatHasBeenUploaded(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	aMediaObject(t, db, id, anAsset)

	trip, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CoverAsset: text(anAsset),
	})
	if err != nil {
		t.Fatalf("PutTrip: %v", err)
	}
	if trip.CoverAsset == nil || *trip.CoverAsset != anAsset {
		t.Errorf("coverAsset = %v, want %s", trip.CoverAsset, anAsset)
	}
}

// A REORDER IS THE CASE THE MANDATED WRITE STRATEGY EXISTS FOR. The schema's
// own comment records the measurement: `trip_cities_ordinal_uq` is
// non-deferrable, so UPDATE-in-place collides row by row even when the final
// state is unique, and delete-then-insert does not.
func TestReorderingTheCitiesOfATripDoesNotCollide(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)
	for _, city := range []string{"kyoto", "seoul"} {
		aCity(t, db, id, city, city)
	}
	ctx := context.Background()

	if _, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"kyoto", "seoul"},
	}); err != nil {
		t.Fatalf("PutTrip (first order): %v", err)
	}
	trip, _, err := store.PutTrip(ctx, id, logbook.TripWrite{
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"seoul", "kyoto"},
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
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"atlantis"},
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
		ID: "autumn-crossing", Name: "Autumn crossing", CityIDs: []string{"atlantis"},
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
		logbook.TripWrite{ID: "kyoto", Name: "Kyoto"})
	if !errors.Is(err, logbook.ErrNoTraveller) {
		t.Errorf("PutTrip(an unknown traveller) = %v, want logbook.ErrNoTraveller", err)
	}
}
