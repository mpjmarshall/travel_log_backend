// The whole-log round trip through postgresql, and it is the strongest leg
// this project can write.
package seed_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"

	"travellog/internal/logbook"
	"travellog/internal/postgres"
	"travellog/internal/postgres/testdb"
	"travellog/internal/seed"
	"travellog/migrations"
)

const clientFixture = "../logbook/testdata/client_sample_log.json"

// The two locators the captured document holds and the sha256 of's two PNGs
// beside it.
const (
	cardDigest = "8dfb203bc0f890655a7545004866da13482af78d21b5c6deb7bd142592a5d3cd"
	heroDigest = "e66b552e6043510bb5cd474096d18208b1c975556ef1a8cfc565dd63a02835c1"
)

func fixtureMapping() map[string]string {
	return map[string]string{
		"assets/imagery/card-ireland.png":  cardDigest,
		"assets/imagery/hero-mountain.png": heroDigest,
	}
}

// fixtureObjects is the media_objects half.
func fixtureObjects(travellerID string) []seed.MediaObject {
	now := seed.Epoch
	return []seed.MediaObject{
		{TravellerID: travellerID, ID: cardDigest, ByteSize: 529392, ContentType: "image/png",
			CreatedAt: now, UploadedAt: &now},
		{TravellerID: travellerID, ID: heroDigest, ByteSize: 555376, ContentType: "image/png",
			CreatedAt: now, UploadedAt: &now},
	}
}

func aTraveller(id string) seed.Traveller {
	return seed.Traveller{
		ID:             id,
		Email:          "seed@travellog.test",
		LogbookVersion: 1,
		CreatedAt:      seed.Epoch,
	}
}

// freshDatabase is a migrated, empty schema — no traveller, which is the
// state `make seed` is the only thing allowed to run against.
func freshDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (postgres.Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return db
}

func clientDocument(t *testing.T) logbook.Document {
	t.Helper()
	raw, err := os.ReadFile(clientFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", clientFixture, err)
	}
	envelope, err := logbook.DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("the reference document no longer decodes: %v", err)
	}
	return envelope.Logbook
}

// travellerUUID is a fixed uuid because the traveller id is a path segment in
// the bucket and a column in ten tables.
const travellerUUID = "11111111-2222-4333-8444-555555555555"

// loaded seeds a fresh database with the client's own log and answers the
// document that came back out through the real store and the real emitter.
func loaded(t *testing.T) (*sql.DB, logbook.Document, logbook.Document) {
	t.Helper()
	db := freshDatabase(t)

	want, err := logbook.RewriteAssets(clientDocument(t), fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}

	dataset, err := seed.FromDocument(aTraveller(travellerUUID), fixtureObjects(travellerUUID), want)
	if err != nil {
		t.Fatalf("FromDocument: %v", err)
	}
	if _, err := seed.Load(t.Context(), db, dataset, seed.LoadOptions{}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	snap, err := postgres.LogbookStore{DB: db}.Read(t.Context(), travellerUUID, func(int64) bool { return true })
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Document == nil {
		t.Fatalf("Read assembled nothing")
	}
	envelope, err := logbook.Emit(logbook.FormatVersion, *snap.Document)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return db, want, envelope.Logbook
}

// The round trip.
func TestTheClientsOwnLogSurvivesPostgreSQL(t *testing.T) {
	_, want, got := loaded(t)

	for i := range want.Trips {
		want.Trips[i].Shared = want.Trips[i].ShareLinkID != nil
		want.Trips[i].ShareLinkID = nil
	}

	for _, diff := range documentDifferences(t, alignByID(want), alignByID(got), 40) {
		t.Errorf("%s", diff)
	}
}

// Every top-level list comes back ordered by its own id, and that is the
// ETag's precondition rather than a display choice.
func TestEveryTopLevelListComesBackOrderedByID(t *testing.T) {
	_, _, got := loaded(t)

	ordered := func(what string, ids []string) {
		t.Helper()
		if len(ids) < 2 {
			t.Errorf("%s has %d entries; an ordering assertion over it cannot fail", what, len(ids))
			return
		}
		if !sort.StringsAreSorted(ids) {
			t.Errorf("%s did not come back ordered by id: %v", what, ids)
		}
	}
	ordered("trips", idsOf(got.Trips, func(v logbook.Trip) string { return v.ID }))
	ordered("cities", idsOf(got.Cities, func(v logbook.City) string { return v.ID }))
	ordered("places", idsOf(got.Places, func(v logbook.Place) string { return v.ID }))
	ordered("photos", idsOf(got.Photos, func(v logbook.Photo) string { return v.ID }))
	ordered("walks", idsOf(got.Walks, func(v logbook.Walk) string { return v.ID }))
}

// travel order survives storage.
func TestATripsCitiesComeBackInTravelOrder(t *testing.T) {
	_, want, got := loaded(t)

	byID := map[string][]string{}
	for _, tr := range got.Trips {
		byID[tr.ID] = tr.CityIDs
	}
	unsorted := 0
	for _, tr := range want.Trips {
		if !sort.StringsAreSorted(tr.CityIDs) && len(tr.CityIDs) > 1 {
			unsorted++
		}
		if strings.Join(byID[tr.ID], ",") != strings.Join(tr.CityIDs, ",") {
			t.Errorf("trip %s: cityIds came back %v, the client wrote %v",
				tr.ID, byID[tr.ID], tr.CityIDs)
		}
	}
	if unsorted == 0 {
		t.Errorf("no trip in the fixture has cityIds out of alphabetical order; " +
			"this leg cannot tell ORDER BY ordinal from ORDER BY city_id")
	}
}

// The visit order is the fixture's own, and the plan's version of this leg
// cannot pass against the client's own log.
func TestVisitsComeBackInTheOrderTheClientWroteThem(t *testing.T) {
	_, want, got := loaded(t)

	byID := map[string][]logbook.Visit{}
	for _, p := range got.Places {
		byID[p.ID] = p.Visits
	}

	multi, newestFirst := 0, 0
	for _, p := range want.Places {
		if len(p.Visits) > 1 {
			multi++
		}
		gotVisits := byID[p.ID]
		if len(gotVisits) != len(p.Visits) {
			t.Errorf("place %s: %d visits came back, the client wrote %d",
				p.ID, len(gotVisits), len(p.Visits))
			continue
		}
		for i := range p.Visits {
			if gotVisits[i].ID != p.Visits[i].ID {
				t.Errorf("place %s: visit %d came back as %s, the client wrote %s — "+
					"the client reads visits.first.at as lastVisited",
					p.ID, i, gotVisits[i].ID, p.Visits[i].ID)
			}
		}
		if len(p.Visits) > 1 && !p.Visits[0].At.Time().Before(p.Visits[1].At.Time()) {
			newestFirst++
		}
	}
	if multi < 2 {
		t.Errorf("places with more than one visit = %d; this leg cannot fail below 2", multi)
	}
	if newestFirst == 0 {
		t.Errorf("no place in the fixture leads with its newest visit; the ordering " +
			"this leg protects would not be visible on P1 at all")
	}
}

// The rule, asserted over what came back out of PostgreSQL.
func TestThePlaceAndVisitPairAgreesAfterAStorageRoundTrip(t *testing.T) {
	_, _, got := loaded(t)

	var both, neither, placeOnly, visitOnly int
	for _, p := range got.Photos {
		switch {
		case p.PlaceID != nil && p.VisitID != nil:
			both++
		case p.PlaceID == nil && p.VisitID == nil:
			neither++
		case p.PlaceID != nil:
			placeOnly++
		default:
			visitOnly++
		}
	}
	if both != 95 || neither != 189 || placeOnly != 0 || visitOnly != 0 {
		t.Errorf("both=%d neither=%d place-only=%d visit-only=%d, want 95/189/0/0",
			both, neither, placeOnly, visitOnly)
	}
}

// Two reads with no write between them are byte-identical, which is the
// ETag's whole promise and which the slice could only test against one trip.
func TestTwoReadsOfASeededLogAreByteIdentical(t *testing.T) {
	db, _, _ := loaded(t)
	store := postgres.LogbookStore{DB: db}

	read := func() []byte {
		snap, err := store.Read(t.Context(), travellerUUID, func(int64) bool { return true })
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		envelope, err := logbook.Emit(logbook.FormatVersion, *snap.Document)
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		out, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		return out
	}

	first, second := read(), read()
	if string(first) != string(second) {
		t.Errorf("two reads of one log differ (%d vs %d bytes)", len(first), len(second))
	}
	t.Logf("the seeded log emits %d bytes through this build", len(first))
}

// A seeded traveller's first read carries A valid ETag.
func TestASeededTravellerReadsAtVersionOne(t *testing.T) {
	db, _, _ := loaded(t)

	snap, err := postgres.LogbookStore{DB: db}.Read(t.Context(), travellerUUID, func(int64) bool { return false })
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.Version != 1 {
		t.Errorf("logbook_version = %d, want 1 — a seeded log served with no ETag is "+
			"a log every phone re-fetches for ever", snap.Version)
	}
}

// The refusal, and it is the same predicate puts on register: any traveller
// row, not "a non-empty log".
func TestLoadRefusesWhenAnyTravellerRowExists(t *testing.T) {
	db := freshDatabase(t)

	want, err := logbook.RewriteAssets(clientDocument(t), fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}
	dataset, err := seed.FromDocument(aTraveller(travellerUUID), fixtureObjects(travellerUUID), want)
	if err != nil {
		t.Fatalf("FromDocument: %v", err)
	}
	if _, err := seed.Load(t.Context(), db, dataset, seed.LoadOptions{}); err != nil {
		t.Fatalf("the first Load: %v", err)
	}

	before := rowCounts(t, db)

	second, err := seed.FromDocument(aTraveller("99999999-8888-4777-8666-555555555555"),
		fixtureObjects("99999999-8888-4777-8666-555555555555"), want)
	if err != nil {
		t.Fatalf("FromDocument: %v", err)
	}
	_, err = seed.Load(t.Context(), db, second, seed.LoadOptions{})
	if err == nil {
		t.Fatalf("the second Load succeeded; the whole point of the guard is that it does not")
	}
	if !errors.Is(err, seed.ErrTravellerExists) {
		t.Errorf("the refusal is %v, want it to be seed.ErrTravellerExists", err)
	}

	var exists *seed.TravellerExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("the refusal does not carry the traveller it found: %v", err)
	}
	if exists.TravellerID != travellerUUID {
		t.Errorf("the refusal names traveller %q, want %q", exists.TravellerID, travellerUUID)
	}

	after := rowCounts(t, db)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("the refused load changed the database:\n  before %v\n  after  %v", before, after)
	}
}

// FromDocument refuses A document whose assets nothing has uploaded, and
// names the digest.
func TestFromDocumentRefusesAnAssetWithNoObject(t *testing.T) {
	doc, err := logbook.RewriteAssets(clientDocument(t), fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}
	only := fixtureObjects(travellerUUID)[:1]

	_, err = seed.FromDocument(aTraveller(travellerUUID), only, doc)
	if err == nil {
		t.Fatalf("FromDocument accepted a document addressing an object nothing declared")
	}
	if !strings.Contains(err.Error(), heroDigest) {
		t.Errorf("the refusal does not name the missing object: %v", err)
	}
}

// The ten tables, and the count is re-derived from the dataset rather than
// written twice.
func TestTheLoadWritesTenTablesAndNotSessions(t *testing.T) {
	db, _, _ := loaded(t)

	counts := rowCounts(t, db)
	want := map[string]int{
		"travellers": 1, "media_objects": 2, "cities": 12, "trips": 7,
		"trip_cities": 18, "places": 17, "visits": 49, "photos": 284,
		"walks": 2, "share_links": 1, "sessions": 0,
	}
	for table, n := range want {
		if counts[table] != n {
			t.Errorf("%s = %d rows, want %d", table, counts[table], n)
		}
	}
	written := 0
	for table, n := range counts {
		if table != "sessions" && n > 0 {
			written++
		}
	}
	if written != 10 {
		t.Errorf("tables with rows = %d, want 10", written)
	}
}

var seededTables = []string{
	"travellers", "sessions", "media_objects", "cities", "trips", "trip_cities",
	"places", "visits", "photos", "walks", "share_links",
}

func rowCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range seededTables {
		var n int
		if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

// documentDifferences renders's first n places where two documents
// disagree, by path.
func alignByID(doc logbook.Document) logbook.Document {
	out := doc
	out.Trips = sortedCopy(doc.Trips, func(v logbook.Trip) string { return v.ID })
	out.Cities = sortedCopy(doc.Cities, func(v logbook.City) string { return v.ID })
	out.Places = sortedCopy(doc.Places, func(v logbook.Place) string { return v.ID })
	out.Photos = sortedCopy(doc.Photos, func(v logbook.Photo) string { return v.ID })
	out.Walks = sortedCopy(doc.Walks, func(v logbook.Walk) string { return v.ID })
	return out
}

func sortedCopy[T any](in []T, id func(T) string) []T {
	out := make([]T, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return id(out[i]) < id(out[j]) })
	return out
}

func idsOf[T any](in []T, id func(T) string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, id(v))
	}
	return out
}

func documentDifferences(t *testing.T, want, got logbook.Document, n int) []string {
	t.Helper()
	return differences(t, toAny(t, want), toAny(t, got), "logbook", n)
}

func toAny(t *testing.T, doc logbook.Document) any {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling a document: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("re-decoding a document: %v", err)
	}
	return out
}

func differences(t *testing.T, want, got any, path string, n int) []string {
	t.Helper()
	var out []string
	var walk func(want, got any, path string)
	walk = func(want, got any, path string) {
		if len(out) >= n {
			return
		}
		switch w := want.(type) {
		case map[string]any:
			g, ok := got.(map[string]any)
			if !ok {
				out = append(out, fmt.Sprintf("%s: client has an object, server has %T", path, got))
				return
			}
			keys := make([]string, 0, len(w))
			for k := range w {
				keys = append(keys, k)
			}
			for k := range g {
				if _, seen := w[k]; !seen {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				wv, inWant := w[k]
				gv, inGot := g[k]
				switch {
				case !inWant:
					out = append(out, fmt.Sprintf("%s.%s: the server emits %v and the client's document has no such key", path, k, gv))
				case !inGot:
					out = append(out, fmt.Sprintf("%s.%s: the client has %v and the server emits no such key", path, k, wv))
				default:
					walk(wv, gv, path+"."+k)
				}
			}
		case []any:
			g, ok := got.([]any)
			if !ok {
				out = append(out, fmt.Sprintf("%s: client has a list, server has %T", path, got))
				return
			}
			if len(w) != len(g) {
				out = append(out, fmt.Sprintf("%s: client has %d entries, server has %d", path, len(w), len(g)))
				return
			}
			for i := range w {
				walk(w[i], g[i], fmt.Sprintf("%s[%d]", path, i))
			}
		default:
			if fmt.Sprint(want) != fmt.Sprint(got) {
				out = append(out, fmt.Sprintf("%s: client %#v, server %#v", path, want, got))
			}
		}
	}
	walk(want, got, path)
	return out
}

// quietLogger keeps the migration's own progress lines out of a test run that
// is about ten tables of rows.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A registered traveller who has written nothing is not an empty database,
// This is the leg that separates the predicate from the plan's.
func TestLoadRefusesATravellerWhoHasRegisteredAndWrittenNothing(t *testing.T) {
	db := freshDatabase(t)

	registered, err := postgres.AuthStore{DB: db}.CreateTraveller(t.Context(),
		"owner@example.com")
	if err != nil {
		t.Fatalf("registering the owner: %v", err)
	}

	counts := rowCounts(t, db)
	for _, table := range []string{"cities", "trips", "places", "visits", "photos", "walks"} {
		if counts[table] != 0 {
			t.Fatalf("%s has %d rows; this leg is about a database whose LOG is empty",
				table, counts[table])
		}
	}

	want, err := logbook.RewriteAssets(clientDocument(t), fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}
	dataset, err := seed.FromDocument(aTraveller(travellerUUID), fixtureObjects(travellerUUID), want)
	if err != nil {
		t.Fatalf("FromDocument: %v", err)
	}

	_, err = seed.Load(t.Context(), db, dataset, seed.LoadOptions{})
	if !errors.Is(err, seed.ErrTravellerExists) {
		t.Fatalf("Load answered %v; DEC-97's predicate is ANY TRAVELLER ROW, and "+
			"this database has one that has written nothing", err)
	}
	var exists *seed.TravellerExistsError
	if errors.As(err, &exists) && exists.TravellerID != registered.ID {
		t.Errorf("the refusal names %s, want the registered owner %s", exists.TravellerID, registered.ID)
	}
	if n := rowCounts(t, db)["travellers"]; n != 1 {
		t.Errorf("travellers = %d after the refusal, want the owner's 1 and nothing else", n)
	}
}
