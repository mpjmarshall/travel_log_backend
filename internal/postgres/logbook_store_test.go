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

// day, text and ptr are DEC-89's pointer contract at the call site. `day` and
// `text` answer a POINTER TO A POINTER because their fields are nullable: the
// outer one says the key was in the body, the inner one carries the value. A
// leg that wants "sent, and sent as null" writes `ptr[*logbook.Instant](nil)`.
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

// THE ONE LEG IN VS7 NO MUTATION REDDENED, ARGUED RATHER THAN LEFT UNSAID.
// It is a compile-time assertion: the way it fails is `LogbookStore does not
// implement logbook.Store`, which is exit 1 out of the build rather than a
// wrong answer from a test. VS6 recorded four of these and settled that it is
// the right red for a claim about types — an interface and its only
// implementation drifting apart is not a behaviour anything can observe.
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

// === the write ===

// THIS LEG CHANGED ITS MIND AT R1 AND THAT IS THE INTERESTING HALF. It used
// to be named …ReplacesWholeState and to assert `summary == nil` after a body
// that omitted it, with the reason "a whole-state upsert clears what the body
// omits". DEC-89 reverses exactly that sentence: what the body omits is left
// alone. So the leg now asserts the two halves separately — a field that WAS
// sent is written, and a field that was NOT sent survives — because a leg that
// only checked the first would go green against a statement that writes every
// column, which is the defect.
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

// AND THE OTHER HALF OF THE POINTER CONTRACT: sent-as-null still clears. It is
// reachable from Go and not from the wire — encoding/json collapses an
// explicit `null` onto absent for a `**T` field, measured and written out at
// logbook.TripWrite — so this leg is what keeps the store's own branch honest
// while no client can reach it. Delete the CASE WHEN's `sent` flag and the
// leg above reddens; write every column unconditionally and this one stays
// green, which is why both exist.
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

// SF6, AND IT IS THE ACCEPTANCE CHECK: a PUT body carrying shareCoordinates
// leaves the stored flag unchanged. The body cannot even express it, so what
// this leg really proves is that the UPDATE does not name the three columns
// and reset them to their defaults.
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

func TestPutTripRefusesACoverThatWasNeverUploaded(t *testing.T) {
	store, db, _ := logbookStore(t)
	id := aTraveller(t, db)

	_, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CoverAsset: text(anAsset),
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
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"), CoverAsset: text(anAsset),
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

// === DEC-89: absent means leave alone ===

// THE SHIPPED DEFECT, REPRODUCED. Executed by the safety lens against a real
// build of HEAD 89fc93f, and re-executed here before anything was changed:
// PUT /v1/trips/autumn-crossing with three cities and both dates -> 200 and
// trip_cities reads kyoto:0 / osaka:1 / seoul:2; then a body of {id, name} —
// which is EXACTLY what T4's pencil sends, because renameTrip owns the name
// and nothing else — answers 200 with "cityIds": [], "start": null,
// "end": null and SELECT count(*) FROM trip_cities -> 0.
//
// THE BODY IN THIS LEG IS THE BODY THE CLIENT SENDS. A synthesised whole
// entity cannot fail this way, which is why every leg written against one has
// been green since the route shipped — TestPutTripCreatesAndThenReplacesWholeState
// names every field it means to replace, and so proves the replacement rather
// than the absence.
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

	// T4's pencil. Two keys. Nothing else is in the body at all.
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

// THE ASSERTION THE THREE STANDING GUARDS CANNOT MAKE: a count that must not
// fall (DEC-89).
//
// The safety lens measured that a caption-only PUT which unfiles a photograph
// passes ALL THREE of the plan's current guards green — the dangling check
// sees no dangling reference, the place-without-occasion check sees no place,
// and the pair-agreement check sees a pair that agrees. Every one of them
// asks whether the log is COHERENT; none asks whether it is still the same
// log. `count(*) WHERE place_id IS NOT NULL` is the one that does, and it is
// unchanged across every route R6 and R7 will write except `refile` (raises
// it) and the D1/D2-delete branches (lower it by an amount the sheet states).
//
// IT IS HERE, ON THE TRIP ROUTE, BECAUSE THE TRIP ROUTE IS THE ONE THAT
// SHIPPED. Deleting a trip cascades to its photographs, so a write that
// touched trips at all could move this number; a rename must not.
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

// The two refusals DEC-89 makes reachable, and both are 422s naming a field
// rather than the 500s a constraint would have produced.

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

// A PARTIAL DATE WRITE IS ONLY HALF VISIBLE TO ValidateTrip, and this is the
// other half. The body carries one date and the database holds the other, so
// nothing outside the transaction can compare them — and
// trips_dates_ordered_ck would answer with SQLSTATE 23514, which reaches the
// client as a 500 with no field on it. New under DEC-89: a whole-state upsert
// always carried both dates, so this shape did not exist.
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

// === DEC-91: the emitted trip says whether it is shared ===

// DEC-32's write response is a whole Trip the phone SPLICES into its cached
// log. With DEC-85 hashing tokens at rest that Trip carries `shareLinkId:
// null`, and THE CLIENT HOLDS THE ONLY COPY — it minted the token. So an
// ordinary rename overwrites it, and H1 then renders no URL and puts BOTH
// controls out of reach: verified on `wipe/mock-data`, 'Copy link' is
// `onPressed: url == null || _busy ? null : () => _copy(url)`
// (share_sheet_screen.dart:224) and 'Stop sharing' is
// `onPressed: !trip.isShared || _busy ? null : _stop` (line 228), with
// `Trip.isShared => shareLinkId != null` (trip.dart:102). Meanwhile the row in
// share_links is un-revoked and `GET /l/{token}` still serves it. The user
// loses the capability AND the only control that revokes it, from an action
// that has nothing to do with sharing.
//
// `Trip.withName`'s own doc states the invariant verbatim (trip.dart:185-187):
// "A live link belongs to the trip and not to its name — renaming a trip you
// have shared must not quietly kill the URL somebody is holding."
//
// THE PLAN'S EXISTING GUARD IS BLIND TO IT: "the write's answer spliced into
// the cached document decodes equal to the next whole read" — and the next
// whole read is null too. THIS LEG USES A TRIP THAT HAS A LIVE LINK. That is
// the difference, and it is why the leg it replaces passed.
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
// the next fetch disagree about the same trip — which is exactly the shape of
// bug the ETag makes permanent.
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
	// A REVOKED link on the private one, which is the case a naive EXISTS
	// without `revoked_at IS NULL` gets wrong — and it is the ordinary state,
	// because DEC-67 revokes and keeps rather than deleting.
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

// AND IT LEAKS NO CAPABILITY. `shared` is a boolean derived from the row's
// existence; the token itself never reaches the emitter. The leg is here
// because "derive a flag from share_links" and "emit share_links" are one
// keystroke apart.
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
		`INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1::uuid, $2, $3)`,
		travellerID, tripID, token); err != nil {
		t.Fatalf("inserting a share link for %s: %v", tripID, err)
	}
}

func aRevokedShareLink(t *testing.T, db *sql.DB, travellerID, tripID, token string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO share_links (traveller_id, trip_id, token, revoked_at)
		 VALUES ($1::uuid, $2, $3, now())`,
		travellerID, tripID, token); err != nil {
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
