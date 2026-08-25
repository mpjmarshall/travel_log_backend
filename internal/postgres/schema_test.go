// migrations/0001_init.up.sql, exercised rather than inspected.
//
// EVERY LEG HERE RUNS AGAINST A REAL POSTGRESQL and skips, saying so, when
// there is none. Two kinds of leg live in this file and they are labelled
// where they sit: CATALOG legs, which read pg_constraint and pg_index and are
// true at any size, and BEHAVIOUR legs, which delete something and count what
// survived. The catalog legs are cheap and they cannot see a wrong cascade;
// the behaviour legs are the ones named for the sheet line they defend.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"travellog/internal/logbook"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

// The letters pg_constraint.confdeltype uses.
const (
	deleteNoAction = "a"
	deleteRestrict = "r"
	deleteCascade  = "c"
	deleteSetNull  = "n"
)

const (
	tid    = "11111111-1111-1111-1111-111111111111"
	otherT = "22222222-2222-2222-2222-222222222222"
	assetA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	assetB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	noSuch = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// migrated opens a test schema, applies 0001, and answers the pool.
func migrated(t *testing.T) *sql.DB {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("applying 0001: %v", err)
	}
	return db
}

// seeded is `migrated` plus the shape every cascade leg is written against.
// It mirrors the client's own fixture where the fixture has an opinion:
// `fushimi-inari` is visited on one trip only, `wishlist-pin` has no visits at
// all, and one photograph on `autumn-crossing` points at a visit that belongs
// to `kyoto-in-may` — which is the `_repointed` shape.
func seeded(t *testing.T) *sql.DB {
	t.Helper()
	db := migrated(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'Matt@Example.COM','x')`, tid)
	mustExec(t, db, `INSERT INTO media_objects (traveller_id, id, byte_size, content_type) VALUES ($1,$2,10,'image/jpeg'),($1,$3,20,'image/jpeg')`, tid, assetA, assetB)
	mustExec(t, db, `INSERT INTO cities (traveller_id, id, name, country_code, country_name, centre_lat, centre_lng)
		VALUES ($1,'kyoto','Kyoto','JP','Japan',35.01,135.76),
		       ($1,'seoul','Seoul','KR','South Korea',37.56,126.97)`, tid)
	mustExec(t, db, `INSERT INTO trips (traveller_id, id, name, started_on, ended_on)
		VALUES ($1,'kyoto-in-may','Kyoto in May','2027-05-01','2027-05-10'),
		       ($1,'autumn-crossing','Autumn Crossing','2027-09-17','2027-10-02')`, tid)
	mustExec(t, db, `INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
		VALUES ($1,'kyoto-in-may','kyoto',0),
		       ($1,'autumn-crossing','kyoto',0),
		       ($1,'autumn-crossing','seoul',1)`, tid)
	mustExec(t, db, `INSERT INTO places (traveller_id, id, city_id, name, lat, lng)
		VALUES ($1,'fushimi-inari','kyoto','Fushimi Inari',34.96,135.77),
		       ($1,'wishlist-pin','kyoto','Somewhere To Go',35.00,135.75)`, tid)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-fushimi-may','fushimi-inari','kyoto-in-may',0,'2027-05-03T07:05:00Z')`, tid)
	mustExec(t, db, `INSERT INTO photos (traveller_id, id, trip_id, city_id, place_id, visit_id, taken_at, asset)
		VALUES ($1,'p-may','kyoto-in-may','kyoto','fushimi-inari','v-fushimi-may','2027-05-03T07:06:00Z',$2)`, tid, assetA)
	mustExec(t, db, `INSERT INTO photos (traveller_id, id, trip_id, city_id, place_id, visit_id, taken_at, asset)
		VALUES ($1,'p-autumn','autumn-crossing','kyoto','fushimi-inari','v-fushimi-may','2027-09-20T07:06:00Z',$2)`, tid, assetB)
	// THE TRACK IS NOT `[]` SINCE 0003. `walks_points_present_ck` refuses an
	// empty array, which is DEC-89's pointer contract making `points: []`
	// expressible and PD-21 closing it — a GPS track is a recording of a day
	// that has passed. Two points rather than one, so a leg that ever asks
	// about a polyline has one to draw.
	mustExec(t, db, `INSERT INTO walks (traveller_id, id, trip_id, city_id, recorded_on, distance_km, points)
		VALUES ($1,'w-may','kyoto-in-may','kyoto','2027-05-03',3.2,
		        '[{"lat":34.96,"lng":135.77},{"lat":34.97,"lng":135.78}]'::jsonb)`, tid)
	mustExec(t, db, `INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'kyoto-in-may','tok-may')`, tid)
	_ = ctx
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", firstLine(q), err)
	}
}

func firstLine(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.IndexByte(q, '\n'); i >= 0 {
		return q[:i] + " …"
	}
	return q
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", firstLine(q), err)
	}
	return n
}

// ---------------------------------------------------------------- THE BLOCKER

// THE LEG THE DEFECT HID FROM. Three review passes missed the composite
// SET NULL blocker because the only cascade leg anyone had written deleted a
// TRIP, and on that path the photograph is cascade-deleted through
// photos.trip_id before the broken FK ever fires. Deleting a PLACE is what
// reaches it.
func TestDeletingAPlaceClearsThePinAndLeavesThePhotographStanding(t *testing.T) {
	db := seeded(t)

	if _, err := db.Exec(`DELETE FROM places WHERE traveller_id=$1 AND id='fushimi-inari'`, tid); err != nil {
		t.Fatalf("deleting the place: %v\n"+
			"    this is the blocker: a composite ON DELETE SET NULL with no column list\n"+
			"    nulls traveller_id too, and traveller_id is NOT NULL", err)
	}

	if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1`, tid); n != 2 {
		t.Fatalf("photographs surviving the place deletion = %d, want 2 — D2's keep branch says they keep their date and city", n)
	}

	rows, err := db.Query(`SELECT id, traveller_id::text, place_id, visit_id, city_id, trip_id FROM photos WHERE traveller_id=$1 ORDER BY id`, tid)
	if err != nil {
		t.Fatalf("reading the photographs back: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, owner, city, trip string
		var place, visit sql.NullString
		if err := rows.Scan(&id, &owner, &place, &visit, &city, &trip); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if owner != tid {
			t.Errorf("%s: traveller_id = %q, want it untouched at %q", id, owner, tid)
		}
		if place.Valid {
			t.Errorf("%s: place_id = %q, want NULL — the pin is what goes", id, place.String)
		}
		if visit.Valid {
			t.Errorf("%s: visit_id = %q, want NULL — the visit went with the pin", id, visit.String)
		}
		if city != "kyoto" {
			t.Errorf("%s: city_id = %q, want it kept", id, city)
		}
		if trip == "" {
			t.Errorf("%s: trip_id was cleared", id)
		}
	}
	if seen != 2 {
		t.Fatalf("read back %d photographs, want 2", seen)
	}
}

// D2: "the track stays with the day it was recorded either way" — which is why
// walks carries no place_id at all.
func TestDeletingAPlaceLeavesEveryWalkUntouched(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `DELETE FROM places WHERE traveller_id=$1 AND id='fushimi-inari'`, tid)
	if n := count(t, db, `SELECT count(*) FROM walks WHERE traveller_id=$1`, tid); n != 1 {
		t.Errorf("walks after removing a place = %d, want 1", n)
	}
}

// D2's DELETE branch is an ORDER of statements in Go, not a foreign key, and
// the order silently inverts the promise. Asserted on the SURVIVING ROW COUNT
// rather than on error/no-error, so the leg cannot pass on an exception.
func TestTheOrderOfD2sDeleteBranchDecidesWhetherThePhotographsSurvive(t *testing.T) {
	t.Run("photographs first, which is what the sheet promises", func(t *testing.T) {
		db := seeded(t)
		mustExec(t, db, `DELETE FROM photos WHERE traveller_id=$1 AND place_id='fushimi-inari'`, tid)
		mustExec(t, db, `DELETE FROM places WHERE traveller_id=$1 AND id='fushimi-inari'`, tid)
		if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1`, tid); n != 0 {
			t.Errorf("photographs surviving = %d, want 0", n)
		}
	})
	t.Run("place first, and the delete then matches nothing", func(t *testing.T) {
		db := seeded(t)
		mustExec(t, db, `DELETE FROM places WHERE traveller_id=$1 AND id='fushimi-inari'`, tid)
		res, err := db.Exec(`DELETE FROM photos WHERE traveller_id=$1 AND place_id='fushimi-inari'`, tid)
		if err != nil {
			t.Fatalf("second delete: %v", err)
		}
		n, _ := res.RowsAffected()
		if n != 0 {
			t.Errorf("the second delete matched %d rows, want 0 — place_id is already NULL", n)
		}
		if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1`, tid); n != 2 {
			t.Errorf("photographs surviving the WRONG order = %d, want 2 — that is the defect this order produces", n)
		}
	})
}

// ------------------------------------------------------------------ D3 and _repointed

func TestDeletingATripClearsVisitIdOnAnotherTripsPhotographAndClearsNothingElse(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `DELETE FROM trips WHERE traveller_id=$1 AND id='kyoto-in-may'`, tid)

	var place, visit sql.NullString
	var trip, city string
	err := db.QueryRow(`SELECT trip_id, city_id, place_id, visit_id FROM photos WHERE traveller_id=$1 AND id='p-autumn'`, tid).
		Scan(&trip, &city, &place, &visit)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("the autumn photograph was deleted with the May trip")
	}
	if err != nil {
		t.Fatalf("reading the autumn photograph: %v", err)
	}
	if visit.Valid {
		t.Errorf("visit_id = %q, want NULL — _repointed clears the reference", visit.String)
	}
	if !place.Valid || place.String != "fushimi-inari" {
		t.Errorf("place_id = %v, want fushimi-inari kept — _repointed clears the reference and NOTHING ELSE", place)
	}
	if trip != "autumn-crossing" || city != "kyoto" {
		t.Errorf("trip_id/city_id = %q/%q, want autumn-crossing/kyoto", trip, city)
	}
}

// D3 itemises "N pins in …" as KEPT. There is deliberately no foreign key from
// trips to places, and that absence is the whole mechanism.
func TestDeletingATripKeepsItsPlacesIncludingOnesWithNoVisitsLeft(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `DELETE FROM trips WHERE traveller_id=$1 AND id='kyoto-in-may'`, tid)

	if n := count(t, db, `SELECT count(*) FROM places WHERE traveller_id=$1`, tid); n != 2 {
		t.Errorf("places surviving a trip deletion = %d, want 2 (the visited pin AND the wishlist pin)", n)
	}
	if n := count(t, db, `SELECT count(*) FROM visits WHERE traveller_id=$1 AND place_id='fushimi-inari'`, tid); n != 0 {
		t.Errorf("visits left on fushimi-inari = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 0 {
		t.Errorf("photographs of the deleted trip = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM walks WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 0 {
		t.Errorf("walks of the deleted trip = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 0 {
		t.Errorf("share links of the deleted trip = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM trip_cities WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 0 {
		t.Errorf("trip_cities rows of the deleted trip = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM cities WHERE traveller_id=$1`, tid); n != 2 {
		t.Errorf("cities after a trip deletion = %d, want 2 — a trip deletion destroys no city", n)
	}
}

// ------------------------------------------------------------------ DEC-57 / DEC-69

// Each case clears every OTHER child of kyoto, so the refusal it asserts is the
// one it names. Without that, whichever constraint PostgreSQL happens to check
// first answers for all four and three of the legs prove nothing — which is how
// this was written first, and the run said so: trip_cities_city_fk fired for
// every case.
func TestDeletingACityIsRefusedByEveryChildThatPointsAtIt(t *testing.T) {
	const (
		noPhotos = `DELETE FROM photos WHERE traveller_id=$1`
		noWalks  = `DELETE FROM walks WHERE traveller_id=$1`
		noPlaces = `DELETE FROM places WHERE traveller_id=$1`
		noList   = `DELETE FROM trip_cities WHERE traveller_id=$1 AND city_id='kyoto'`
	)
	cases := []struct {
		name       string
		clear      []string
		constraint string
	}{
		{"a place", []string{noPhotos, noWalks, noList}, "places_city_fk"},
		{"a photograph", []string{noWalks, noPlaces, noList}, "photos_city_fk"},
		{"a walk", []string{noPhotos, noPlaces, noList}, "walks_city_fk"},
		{"a trip's ordered city list", []string{noPhotos, noWalks, noPlaces}, "trip_cities_city_fk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := seeded(t)
			for _, q := range c.clear {
				mustExec(t, db, q, tid)
			}
			_, err := db.Exec(`DELETE FROM cities WHERE traveller_id=$1 AND id='kyoto'`, tid)
			if err == nil {
				t.Fatalf("deleting a city still holding %s succeeded — nothing in the app authorises that cascade", c.name)
			}
			if !strings.Contains(err.Error(), c.constraint) {
				t.Errorf("refusal names %v, want the constraint %s", err, c.constraint)
			}
		})
	}
}

// DEC-69's other half. DEC-64 claimed CASCADE here gave DEC-57's RESTRICT "a
// real child to protect"; executed, CASCADE did the opposite — a city with no
// places, photographs or walks deleted silently and vanished from every trip's
// ordered list, leaving a gap in the ordinals.
// seoul has never had a place, a photograph or a walk — only a trip_cities row.
func TestACityWithNoPlacesPhotosOrWalksIsStillProtectedByItsTrips(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `DELETE FROM photos WHERE traveller_id=$1`, tid)
	mustExec(t, db, `DELETE FROM walks WHERE traveller_id=$1`, tid)
	mustExec(t, db, `DELETE FROM places WHERE traveller_id=$1`, tid)

	if _, err := db.Exec(`DELETE FROM cities WHERE traveller_id=$1 AND id='seoul'`, tid); err == nil {
		t.Fatal("a city listed on a trip deleted silently, leaving a gap in that trip's ordinals")
	}
	if n := count(t, db, `SELECT count(*) FROM trip_cities WHERE traveller_id=$1 AND trip_id='autumn-crossing'`, tid); n != 2 {
		t.Errorf("the trip's ordered city list holds %d rows, want 2", n)
	}
}

// ------------------------------------------------------------------ DEC-58

// The nullable-FK consequence DEC-58 asks to be stated rather than assumed: a
// composite FK is MATCH SIMPLE, so a NULL cover needs no parent lookup.
func TestATripWithNoCoverIsAcceptedAndOneNamingNothingIsRefused(t *testing.T) {
	db := seeded(t)

	if _, err := db.Exec(`INSERT INTO trips (traveller_id, id, name) VALUES ($1,'no-cover','No Cover')`, tid); err != nil {
		t.Errorf("a trip with cover_asset NULL was refused: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO trips (traveller_id, id, name, cover_asset) VALUES ($1,'real-cover','Real',$2)`, tid, assetA); err != nil {
		t.Errorf("a trip naming a committed object was refused: %v", err)
	}
	_, err := db.Exec(`INSERT INTO trips (traveller_id, id, name, cover_asset) VALUES ($1,'ghost','Ghost',$2)`, tid, noSuch)
	if err == nil {
		t.Fatal("a trip naming an object that does not exist was accepted — the FK is not doing its job")
	}
	if !strings.Contains(err.Error(), "trips_cover_fk") {
		t.Errorf("refusal = %v, want it to name trips_cover_fk", err)
	}
}

func TestAPhotographMustNameAnObjectThatExists(t *testing.T) {
	db := seeded(t)
	_, err := db.Exec(`INSERT INTO photos (traveller_id, id, trip_id, city_id, taken_at, asset)
		VALUES ($1,'ghost','kyoto-in-may','kyoto','2027-05-03T00:00:00Z',$2)`, tid, noSuch)
	if err == nil {
		t.Fatal("a photograph naming an object that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "photos_asset_fk") {
		t.Errorf("refusal = %v, want it to name photos_asset_fk", err)
	}
}

func TestAnObjectStillReferencedCannotBeDeleted(t *testing.T) {
	db := seeded(t)
	if _, err := db.Exec(`DELETE FROM media_objects WHERE traveller_id=$1 AND id=$2`, tid, assetA); err == nil {
		t.Fatal("an object a photograph still points at was deleted")
	}
}

// The non-obvious half, and the reason it is written down: account deletion is
// NOT made impossible by seven RESTRICT foreign keys. RESTRICT checks are
// AFTER-ROW triggers evaluated at end of statement, and the recursive CASCADE
// removes every referencing row before they fire.
func TestDeletingATravellerWorksDespiteEveryRestrict(t *testing.T) {
	db := seeded(t)
	res, err := db.Exec(`DELETE FROM travellers WHERE id=$1`, tid)
	if err != nil {
		t.Fatalf("deleting a traveller: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("deleted %d travellers, want 1", n)
	}
	for _, table := range []string{"sessions", "media_objects", "cities", "trips", "trip_cities", "places", "visits", "photos", "walks", "share_links"} {
		if n := count(t, db, fmt.Sprintf(`SELECT count(*) FROM %s`, table)); n != 0 {
			t.Errorf("%s still holds %d rows after the traveller went", table, n)
		}
	}
}

// ------------------------------------------------------------------ DEC-65

func TestTwoAddressesDifferingOnlyInCaseAreRefusedByTheDatabase(t *testing.T) {
	db := seeded(t) // already holds Matt@Example.COM
	_, err := db.Exec(`INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'matt@example.com','x')`, otherT)
	if err == nil {
		t.Fatal("a second registration differing only in case was accepted")
	}
	if !strings.Contains(err.Error(), "travellers_email_lower_key") {
		t.Errorf("refusal = %v, want it to name travellers_email_lower_key", err)
	}
}

func TestTheAddressIsStoredExactlyAsItWasTyped(t *testing.T) {
	db := seeded(t)
	var got string
	if err := db.QueryRow(`SELECT email FROM travellers WHERE id=$1`, tid).Scan(&got); err != nil {
		t.Fatalf("reading the address back: %v", err)
	}
	if got != "Matt@Example.COM" {
		t.Errorf("email = %q, want it stored as typed — the local part is case-sensitive per RFC 5321", got)
	}
}

// The warning DEC-65 gives, made falsifiable: the wrong lookup does not error,
// it silently reports an unknown address.
func TestTheWrongLookupMissesSilentlyAndTheRightOneUsesTheIndex(t *testing.T) {
	db := seeded(t)

	if n := count(t, db, `SELECT count(*) FROM travellers WHERE email = 'matt@example.com'`); n != 0 {
		t.Errorf("`WHERE email = $1` found %d rows; the point is that it finds 0 and looks like an unknown address", n)
	}
	if n := count(t, db, `SELECT count(*) FROM travellers WHERE lower(email) = lower('MATT@example.com')`); n != 1 {
		t.Errorf("`WHERE lower(email) = lower($1)` found %d rows, want 1", n)
	}

	plan := explain(t, db, `SELECT id FROM travellers WHERE lower(email) = lower('matt@example.com')`)
	if !strings.Contains(plan, "travellers_email_lower_key") {
		t.Errorf("the correct lookup does not use the index:\n%s", plan)
	}
}

// The functional index resolves `lower` through search_path, so this asserts
// which function it actually bound to. Reading the index definition cannot
// tell you: pg_get_indexdef prints `lower(email)` either way.
// pg_depend cannot answer this: dependencies on PINNED system objects are not
// recorded, so a functional index on a built-in has no pg_proc row — measured,
// that query returns zero rows. And pg_get_indexdef prints `lower(email)`
// whichever function it bound to. The stored expression tree carries the
// resolved OID and is the only place the answer is.
func TestTheEmailIndexBoundToPgCatalogsLower(t *testing.T) {
	db := migrated(t)
	var nsp, name string
	err := db.QueryRow(`
		SELECT n.nspname, p.proname
		FROM pg_index i
		JOIN pg_proc p ON p.oid = (substring(i.indexprs::text from ':funcid ([0-9]+)'))::oid
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE i.indexrelid = 'travellers_email_lower_key'::regclass`).Scan(&nsp, &name)
	if err != nil {
		t.Fatalf("reading the index expression's function: %v", err)
	}
	if nsp != "pg_catalog" || name != "lower" {
		t.Errorf("travellers_email_lower_key bound %s.%s, want pg_catalog.lower", nsp, name)
	}
}

// THE HAZARD ITSELF, reproduced rather than argued. A schema placed BEFORE
// pg_catalog shadows `lower`, and the lookup then both misses the index and
// returns the wrong answer — silently, with no error anywhere. This is a fact
// about PostgreSQL, not about this schema, and it is the reason the runner
// pins search_path and the reason the application role must never be given one
// that puts a schema ahead of pg_catalog.
// Both sides of the comparison go through the shadow, so they collapse to one
// constant and the predicate matches EVERY row: an address nobody registered
// resolves to a traveller, and nothing is raised.
func TestAShadowedLowerBreaksTheLookupSilently(t *testing.T) {
	db := seeded(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	var schema string
	if err := tx.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if _, err := tx.Exec(`CREATE FUNCTION lower(text) RETURNS text AS $$ SELECT 'shadowed' $$ LANGUAGE sql IMMUTABLE`); err != nil {
		t.Fatalf("creating the shadowing function: %v", err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path = ` + schema + `, pg_catalog`); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM travellers WHERE lower(email) = lower('nobody@example.com')`).Scan(&n); err != nil {
		t.Fatalf("the shadowed lookup: %v", err)
	}
	if n == 0 {
		t.Fatalf("the shadowed lookup behaved correctly; this leg exists to show that it does not")
	}
	t.Logf("a schema ahead of pg_catalog made `lower(email) = lower('nobody@example.com')` match %d traveller(s), "+
		"with no error anywhere", n)
}

// ------------------------------------------------------------------ DEC-66 / DEC-67 / DEC-68

func TestTwoVisitsOfOnePlaceCannotShareAnOrdinal(t *testing.T) {
	db := seeded(t)
	_, err := db.Exec(`INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-second','fushimi-inari','autumn-crossing',0,'2027-09-20T00:00:00Z')`, tid)
	if err == nil {
		t.Fatal("a second visit of the same place took ordinal 0 — `ORDER BY ordinal` is now non-deterministic, " +
			"and a photograph silently rebinds to a different occasion")
	}
	if !strings.Contains(err.Error(), "visits_place_ordinal_uq") {
		t.Errorf("refusal = %v, want it to name visits_place_ordinal_uq", err)
	}
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-second','fushimi-inari','autumn-crossing',1,'2027-09-20T00:00:00Z')`, tid)
}

func TestAReorderWrittenAsDeleteThenInsertPassesTheNonDeferrableUnique(t *testing.T) {
	db := seeded(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM trip_cities WHERE traveller_id=$1 AND trip_id='autumn-crossing'`, tid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
		VALUES ($1,'autumn-crossing','seoul',0),($1,'autumn-crossing','kyoto',1)`, tid); err != nil {
		t.Fatalf("a full reorder written as delete-then-insert was refused: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var first string
	if err := db.QueryRow(`SELECT city_id FROM trip_cities WHERE traveller_id=$1 AND trip_id='autumn-crossing' ORDER BY ordinal LIMIT 1`, tid).Scan(&first); err != nil {
		t.Fatalf("reading the reordered list: %v", err)
	}
	if first != "seoul" {
		t.Errorf("first city after the reorder = %q, want seoul", first)
	}
}

// The counter-case, recorded because it is what a worker will reach for and it
// is the one that fails: a UNIQUE index is checked per ROW during a statement,
// so a set-based UPDATE collides even though its final state is unique.
func TestASetBasedUpdateOfTheOrdinalsCollidesAndThatIsWhyTheStrategyIsDeleteThenInsert(t *testing.T) {
	db := seeded(t)
	_, err := db.Exec(`UPDATE trip_cities SET ordinal = 1 - ordinal WHERE traveller_id=$1 AND trip_id='autumn-crossing'`, tid)
	if err == nil {
		t.Skip("this server allowed the in-place swap; the delete-then-insert strategy is still the mandated one")
	}
	if !strings.Contains(err.Error(), "trip_cities_ordinal_uq") {
		t.Errorf("refusal = %v, want it to name trip_cities_ordinal_uq", err)
	}
}

// Two halves. Two live links for one trip is what the class diagram forbids and
// what nothing but the partial index enforces; and H1's Stop sharing followed
// by New link is a duplicate-key error under PRIMARY KEY (traveller_id,
// trip_id), which is the primary key DEC-67 rejected.
func TestStopSharingThenNewLinkWorksAndOnlyOneLinkIsEverLive(t *testing.T) {
	db := seeded(t)

	_, err := db.Exec(`INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'kyoto-in-may','tok-two')`, tid)
	if err == nil {
		t.Fatal("a second LIVE link was accepted for one trip")
	}
	if !strings.Contains(err.Error(), "share_links_one_live") {
		t.Errorf("refusal = %v, want it to name share_links_one_live", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE share_links SET revoked_at=now() WHERE traveller_id=$1 AND trip_id='kyoto-in-may' AND revoked_at IS NULL`, tid); err != nil {
		t.Fatalf("stop sharing: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'kyoto-in-may','tok-three')`, tid); err != nil {
		t.Fatalf("new link after stop sharing: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 2 {
		t.Errorf("share_links rows = %d, want 2 — the revocation record survives", n)
	}
	if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND revoked_at IS NULL`, tid); n != 1 {
		t.Errorf("live links = %d, want exactly 1", n)
	}
}

// A token has to be unique across the whole table: GET /l/{token} arrives with
// no traveller in hand.
func TestATokenIsUniqueAcrossEveryTraveller(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'other@example.com','x')`, otherT)
	mustExec(t, db, `INSERT INTO trips (traveller_id, id, name) VALUES ($1,'their-trip','Theirs')`, otherT)
	_, err := db.Exec(`INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'their-trip','tok-may')`, otherT)
	if err == nil {
		t.Fatal("two travellers hold the same share token; /l/{token} cannot tell them apart")
	}
	if !strings.Contains(err.Error(), "share_links_token_key") {
		t.Errorf("refusal = %v, want it to name share_links_token_key", err)
	}
}

// DEC-68: the wire carries 07:05 and `date` would throw it away.
func TestAVisitKeepsItsTimeOfDay(t *testing.T) {
	db := seeded(t)
	var got string
	if err := db.QueryRow(`SELECT to_char(at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') FROM visits WHERE traveller_id=$1 AND id='v-fushimi-may'`, tid).Scan(&got); err != nil {
		t.Fatalf("reading the visit back: %v", err)
	}
	if got != "2027-05-03T07:05:00.000Z" {
		t.Errorf("visits.at round-tripped as %q, want 2027-05-03T07:05:00.000Z", got)
	}

	var typ string
	if err := db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_name='visits' AND column_name='at' AND table_schema=current_schema()`).Scan(&typ); err != nil {
		t.Fatalf("reading the column type: %v", err)
	}
	if typ != "timestamp with time zone" {
		t.Errorf("visits.at is %q, want timestamp with time zone", typ)
	}
}

// The other three date-bearing columns genuinely are midnight-UTC on the wire,
// so `date` is lossless for them — asserted so nobody "fixes" them to match
// visits.at.
func TestTheOtherThreeDateColumnsAreDates(t *testing.T) {
	db := migrated(t)
	for _, c := range []struct{ table, column string }{
		{"trips", "started_on"}, {"trips", "ended_on"}, {"walks", "recorded_on"},
	} {
		var typ string
		if err := db.QueryRow(`SELECT data_type FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, c.table, c.column).Scan(&typ); err != nil {
			t.Fatalf("%s.%s: %v", c.table, c.column, err)
		}
		if typ != "date" {
			t.Errorf("%s.%s is %q, want date", c.table, c.column, typ)
		}
	}
}

// ------------------------------------------------------------------ DEC-02 ids

func TestATripWhoseIdIsKyotoRoundTrips(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `INSERT INTO trips (traveller_id, id, name) VALUES ($1,'kyoto','Kyoto')`, tid)
	var got string
	if err := db.QueryRow(`SELECT id FROM trips WHERE traveller_id=$1 AND id='kyoto'`, tid).Scan(&got); err != nil {
		t.Fatalf("reading `kyoto` back: %v", err)
	}
	if got != "kyoto" {
		t.Errorf("id = %q, want kyoto exactly — never char(n), which blank-pads", got)
	}
}

func TestAnIdOutsideTheSlugAlphabetIsRefused(t *testing.T) {
	db := seeded(t)
	for _, bad := range []string{"", "Kyoto", "kyoto_in_may", "kyoto in may", strings.Repeat("k", 65)} {
		if _, err := db.Exec(`INSERT INTO trips (traveller_id, id, name) VALUES ($1,$2,'x')`, tid, bad); err == nil {
			t.Errorf("id %q was accepted", bad)
		}
	}
}

// ------------------------------------------------------------------ CHECK constraints

// EVERY ROW HERE WAS INSERTED SUCCESSFULLY on a real instance before its
// constraint existed. The list is the review's, one leg per row, each naming
// the constraint that must refuse it so a passing test cannot be satisfied by
// the wrong refusal.
//
// `args` is appended AFTER the traveller id, which is $1 in every query except
// the one that creates a traveller — hence ownArgs. Without it that row failed
// with `could not determine data type of parameter $1`, a red for the wrong
// reason.
func TestTheSchemaRefusesTheDataTheAppForbids(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		query      string
		args       []any
		ownArgs    bool
	}{
		{"a five-letter country code", "cities_country_code_ck",
			`INSERT INTO cities (traveller_id,id,name,country_code,country_name,centre_lat,centre_lng) VALUES ($1,'x','X','JAPAN','Japan',1,1)`, nil, false},
		{"an empty city name", "cities_name_present_ck",
			`INSERT INTO cities (traveller_id,id,name,country_code,country_name,centre_lat,centre_lng) VALUES ($1,'x','','JP','Japan',1,1)`, nil, false},
		{"latitude 999", "cities_centre_lat_ck",
			`INSERT INTO cities (traveller_id,id,name,country_code,country_name,centre_lat,centre_lng) VALUES ($1,'x','X','JP','Japan',999,1)`, nil, false},
		{"longitude -4000", "cities_centre_lng_ck",
			`INSERT INTO cities (traveller_id,id,name,country_code,country_name,centre_lat,centre_lng) VALUES ($1,'x','X','JP','Japan',1,-4000)`, nil, false},
		{"a negative byte size", "media_objects_byte_size_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,-5,'image/jpeg')`, []any{noSuch}, false},
		// THE CONSTRAINT IS RENAMED AT 0003 AND THE ROW BELOW IT IS NEW
		// (DEC-51, PD-10, DEC-104). `_present_ck` stopped '' and nothing else,
		// and 0001's own comment called it the weakest check in the file and
		// said `text/html; <script>` was accepted. The name changes with the
		// claim, and the catalog leg that asserted the old one is this line.
		{"an empty content type", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'')`, []any{noSuch}, false},
		// THE PAYLOAD ITSELF, which is what the allowlist is FOR: an object
		// stored as text/html is served AS HTML from the bucket origin, at a
		// URL the public share envelope embeds.
		{"the XSS payload 0001's own comment said was accepted", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'text/html; <script>')`, []any{noSuch}, false},
		// AND heic, WHICH IS OUT (DEC-104). It is a plausible image type and
		// nothing in this system can produce one — the client's shutter is
		// inert by decision — so an allowlist entry for it would be a claim
		// the schema makes that no leg can check.
		{"image/heic, which DEC-104 took out", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'image/heic')`, []any{noSuch}, false},
		// A ROW COMMITTED BEFORE IT WAS CREATED. The sweep's grace window keys
		// off exactly these two timestamps.
		{"an object committed before it was created", "media_objects_uploaded_after_created_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type,created_at,uploaded_at)
			 VALUES ($1,$2,5,'image/png','2027-01-02T00:00:00Z','2027-01-01T00:00:00Z')`, []any{noSuch}, false},
		{"an empty caption", "photos_caption_present_ck",
			`INSERT INTO photos (traveller_id,id,trip_id,city_id,taken_at,asset,caption) VALUES ($1,'ph','kyoto-in-may','kyoto','2027-05-03T00:00:00Z',$2,'')`, []any{assetA}, false},
		{"an empty visit note", "visits_note_present_ck",
			`INSERT INTO visits (traveller_id,id,place_id,trip_id,ordinal,at,note) VALUES ($1,'v-empty','fushimi-inari','kyoto-in-may',9,'2027-05-04T07:05:00Z','')`, nil, false},
		{"an empty trip summary", "trips_summary_present_ck",
			`INSERT INTO trips (traveller_id,id,name,summary) VALUES ($1,'blank','Blank','')`, nil, false},
		{"an empty plan on a place", "places_plan_present_ck",
			`INSERT INTO places (traveller_id,id,city_id,name,lat,lng,plan) VALUES ($1,'blank','kyoto','Blank',35.0,135.0,'')`, nil, false},
		// THE LOWER BOUND `walks_points_array_ck` COULD NOT EXPRESS (PD-21).
		// An empty array IS an array, so the row below passed both 0001
		// constraints and destroyed a recording of a day that has passed.
		{"a walk with an empty track", "walks_points_present_ck",
			`INSERT INTO walks (traveller_id,id,trip_id,city_id,recorded_on,distance_km,points) VALUES ($1,'w','kyoto-in-may','kyoto','2027-05-03',1,'[]'::jsonb)`, nil, false},
		{"an object id that is not hex sha256", "media_objects_id_sha256_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,'not-a-digest',5,'image/jpeg')`, nil, false},
		// THE TRACK IS NON-EMPTY HERE ON PURPOSE. With `'[]'::jsonb` this row
		// now violates TWO constraints, and PostgreSQL does not promise which
		// of two failing CHECKs it names — so the leg would pass or fail on
		// something other than the distance it is about.
		{"a negative walk distance", "walks_distance_km_ck",
			`INSERT INTO walks (traveller_id,id,trip_id,city_id,recorded_on,distance_km,points) VALUES ($1,'w','kyoto-in-may','kyoto','2027-05-03',-1,'[{"lat":1,"lng":2}]'::jsonb)`, nil, false},
		{"a track that is not a list", "walks_points_array_ck",
			`INSERT INTO walks (traveller_id,id,trip_id,city_id,recorded_on,distance_km,points) VALUES ($1,'w','kyoto-in-may','kyoto','2027-05-03',1,'"not an array"'::jsonb)`, nil, false},
		{"a photograph with lat and no lng", "photos_coordinates_paired_ck",
			`INSERT INTO photos (traveller_id,id,trip_id,city_id,taken_at,asset,lat) VALUES ($1,'ph','kyoto-in-may','kyoto','2027-05-03T00:00:00Z',$2,35.0)`, []any{assetA}, false},
		{"a negative accuracy", "photos_accuracy_metres_ck",
			`INSERT INTO photos (traveller_id,id,trip_id,city_id,taken_at,asset,accuracy_metres) VALUES ($1,'ph','kyoto-in-may','kyoto','2027-05-03T00:00:00Z',$2,-3)`, []any{assetA}, false},
		{"a one-byte token hash", "sessions_token_hash_sha256_ck",
			`INSERT INTO sessions (id,traveller_id,token_hash,expires_at) VALUES ('33333333-3333-3333-3333-333333333333',$1,'\x00'::bytea,'2099-01-01T00:00:00Z')`, nil, false},
		{"a session that expired before it was made", "sessions_expires_after_created_ck",
			`INSERT INTO sessions (id,traveller_id,token_hash,created_at,expires_at) VALUES ('33333333-3333-3333-3333-333333333333',$1,repeat('a',32)::bytea,'2027-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, nil, false},
		{"an empty passphrase hash", "travellers_passphrase_hash_present_ck",
			`INSERT INTO travellers (id,email,passphrase_hash) VALUES ($1,'a@b.example','')`, []any{otherT}, true},
		{"a trip ending before it starts", "trips_dates_ordered_ck",
			`INSERT INTO trips (traveller_id,id,name,started_on,ended_on) VALUES ($1,'backwards','Backwards','2027-05-10','2027-05-01')`, nil, false},
		{"a negative ordinal in a trip's city list", "trip_cities_ordinal_ck",
			`INSERT INTO trip_cities (traveller_id,trip_id,city_id,ordinal) VALUES ($1,'kyoto-in-may','seoul',-1)`, nil, false},
	}

	db := seeded(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := c.args
			if !c.ownArgs {
				args = append([]any{tid}, c.args...)
			}
			_, err := db.Exec(c.query, args...)
			if err == nil {
				t.Fatalf("the schema accepted %s", c.name)
			}
			if !strings.Contains(err.Error(), c.constraint) {
				t.Errorf("refused by %v, want the constraint %s", err, c.constraint)
			}
		})
	}
}

// ------------------------------------------------------------------ CATALOG legs

// DEC-70, DERIVED FROM pg_index.indkey RATHER THAN FROM A HAND-KEPT LIST —
// which is the whole correction. DEC-63's list MISSED trip_cities(traveller_id,
// city_id), because DEC-64 created that table after the list was written; and
// it DUPLICATED an index on share_links, because it did not know the primary
// key already covered it. A list cannot see either. This can.
//
// Two details that decide correctness rather than tidiness:
//   - The index must not be PARTIAL. share_links_one_live covers exactly the
//     right columns and is `WHERE revoked_at IS NULL`, and an RI check needs an
//     index over every row.
//   - The match is on the SET of the leading N columns, not their order. A
//     btree on (a,b) serves `a=$1 AND b=$2` whichever order the foreign key
//     happens to declare them in, so requiring the order would fail correct
//     work.
//
// The comparison runs in Go rather than in SQL because indkey is an int2vector
// with a ZERO lower bound while conkey is a smallint[] with a one, and an
// off-by-one between them reads as "covered" rather than as an error. The
// closing total guards the vacuous direction: a query returning nothing would
// pass the loop without asserting anything.
func TestEveryForeignKeyChildColumnSetLeadsSomeIndex(t *testing.T) {
	db := migrated(t)

	type index struct {
		name    string
		table   string
		columns []string
	}
	var indexes []index
	rows, err := db.Query(`
		SELECT i.indexrelid::regclass::text, t.relname, i.indkey::text
		FROM pg_index i
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = current_schema() AND i.indpred IS NULL AND i.indisvalid`)
	if err != nil {
		t.Fatalf("reading pg_index: %v", err)
	}
	for rows.Next() {
		var ix index
		var keys string
		if err := rows.Scan(&ix.name, &ix.table, &keys); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		ix.columns = strings.Fields(keys)
		indexes = append(indexes, ix)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	fks, err := db.Query(`
		SELECT c.conname, t.relname, array_to_string(c.conkey, ' ')
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype = 'f' AND n.nspname = current_schema()
		ORDER BY c.conname`)
	if err != nil {
		t.Fatalf("reading the foreign keys: %v", err)
	}
	defer fks.Close()

	total := 0
	for fks.Next() {
		var name, table, keys string
		if err := fks.Scan(&name, &table, &keys); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total++
		child := strings.Fields(keys)
		covered := ""
		for _, ix := range indexes {
			if ix.table == table && leadsWith(ix.columns, child) {
				covered = ix.name
				break
			}
		}
		if covered == "" {
			t.Errorf("%s on %s (child columns %v) leads no index — "+
				"every cascade and every RESTRICT check through it sequential-scans the child",
				name, table, child)
		}
	}
	if err := fks.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if total == 0 {
		t.Fatal("found no foreign keys at all")
	}
	t.Logf("foreign keys checked: %d, against %d non-partial indexes", total, len(indexes))
}

// leadsWith reports whether want is exactly the SET of the first len(want)
// columns of have. Order within that prefix does not matter: a btree on (a,b)
// serves `a=$1 AND b=$2` whichever order the foreign key declares them in.
func leadsWith(have, want []string) bool {
	if len(have) < len(want) || len(want) == 0 {
		return false
	}
	prefix := append([]string(nil), have[:len(want)]...)
	sorted := append([]string(nil), want...)
	sort.Strings(prefix)
	sort.Strings(sorted)
	for i := range sorted {
		if prefix[i] != sorted[i] {
			return false
		}
	}
	return true
}

// The cascades, as a catalog fact. This cannot see a wrong ORDER of statements
// in Go and it can see a wrong ON DELETE, which is the other half.
//
// It also carries THE BLOCKER AS A CATALOG FACT: an empty confdelsetcols on a
// SET NULL means "null every column of the key", traveller_id included.
func TestTheDeleteActionsAreWhatTheSheetsSay(t *testing.T) {
	want := map[string]string{
		"sessions_traveller_fk":      deleteCascade,
		"media_objects_traveller_fk": deleteCascade,
		"cities_traveller_fk":        deleteCascade,
		"cities_cover_fk":            deleteRestrict,
		"trips_traveller_fk":         deleteCascade,
		"trips_cover_fk":             deleteRestrict,
		"trip_cities_traveller_fk":   deleteCascade,
		"trip_cities_trip_fk":        deleteCascade,
		"trip_cities_city_fk":        deleteRestrict,
		"places_traveller_fk":        deleteCascade,
		"places_city_fk":             deleteRestrict,
		"places_cover_fk":            deleteRestrict,
		"visits_traveller_fk":        deleteCascade,
		"visits_place_fk":            deleteCascade,
		"visits_trip_fk":             deleteCascade,
		"photos_traveller_fk":        deleteCascade,
		"photos_trip_fk":             deleteCascade,
		"photos_city_fk":             deleteRestrict,
		"photos_place_fk":            deleteSetNull,
		"photos_visit_fk":            deleteSetNull,
		"photos_asset_fk":            deleteRestrict,
		"walks_traveller_fk":         deleteCascade,
		"walks_trip_fk":              deleteCascade,
		"walks_city_fk":              deleteRestrict,
		"share_links_traveller_fk":   deleteCascade,
		"share_links_trip_fk":        deleteCascade,
	}

	db := migrated(t)
	rows, err := db.Query(`
		SELECT c.conname, c.confdeltype,
		       coalesce(array_length(c.confdelsetcols,1),0)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype='f' AND n.nspname = current_schema()`)
	if err != nil {
		t.Fatalf("reading pg_constraint: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, action string
		var setCols int
		if err := rows.Scan(&name, &action, &setCols); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = action
		if action == deleteSetNull && setCols != 1 {
			t.Errorf("%s is ON DELETE SET NULL with %d named columns, want exactly 1 — "+
				"an empty column list nulls traveller_id too, and traveller_id is NOT NULL", name, setCols)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for name, action := range want {
		if got[name] != action {
			t.Errorf("%s confdeltype = %q, want %q", name, got[name], action)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s is a foreign key nobody wrote down — add it to this table with its sheet line", name)
		}
	}
}

// THE MOST DANGEROUS LINE IN 0001 IS AN ABSENCE: there is no foreign key
// anywhere from trips to places, and that absence is D3's "the pins stay in
// those cities". An absence cannot be seen by the table above, which only
// walks what exists.
func TestThereIsNoForeignKeyFromTripsToPlaces(t *testing.T) {
	db := migrated(t)
	n := count(t, db, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_class r ON r.oid = c.confrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype='f' AND n.nspname=current_schema()
		  AND t.relname='trips' AND r.relname='places'`)
	if n != 0 {
		t.Errorf("trips references places through %d foreign keys, want 0 — "+
			"D3 keeps the pins, and that is what the absence implements", n)
	}
}

// DEC-69, generalised. The specific finding was `end`; the rule is that no
// column in this schema may be a word PostgreSQL reserves, because a project
// with no ORM writes every column list by hand.
func TestNoColumnIsAReservedWord(t *testing.T) {
	db := migrated(t)
	rows, err := db.Query(`
		SELECT c.table_name, c.column_name, k.catcode
		FROM information_schema.columns c
		JOIN pg_get_keywords() k ON k.word = c.column_name
		WHERE c.table_schema = current_schema() AND k.catcode IN ('R','T')
		ORDER BY 1,2`)
	if err != nil {
		t.Fatalf("reading the keyword catalogue: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, cat string
		if err := rows.Scan(&table, &column, &cat); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Errorf("%s.%s is a reserved word (catcode %s) — every hand-written query would have to quote it forever",
			table, column, cat)
	}
}

// Auto-generated constraint names are POSITIONAL: they are built from the
// column names, so a rename or a re-add in a later migration moves them and
// this file's catalog legs then redden for a reason unrelated to the schema.
//
// _fkey, _key and _check are PostgreSQL's three generated suffixes. _pkey is
// generated too but is built from the table name alone, so it does not move
// under a column rename and this schema uses it on purpose.
func TestEveryConstraintWasNamedDeliberately(t *testing.T) {
	db := migrated(t)
	rows, err := db.Query(`
		SELECT c.conname, c.contype
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = current_schema() AND c.contype IN ('f','u','c')
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("reading pg_constraint: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, ctype string
		if err := rows.Scan(&name, &ctype); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		for _, auto := range []string{"_fkey", "_check"} {
			if strings.HasSuffix(name, auto) {
				t.Errorf("%s carries PostgreSQL's generated %s suffix — name it explicitly", name, auto)
			}
		}
		if strings.HasSuffix(name, "_key") && !strings.HasSuffix(name, "_pkey") {
			t.Errorf("%s carries PostgreSQL's generated _key suffix — name it explicitly", name)
		}
	}
	if seen == 0 {
		t.Fatal("found no constraints at all")
	}
}

// Sorted in Go, not by the server: `ORDER BY 1` uses the database's collation,
// and under en_US.utf8 punctuation is ignored at the primary level — so
// `trip_cities` and `trips` would sort in an order that is a fact about the
// container's locale rather than about this schema.
func TestTheMigrationCreatesExactlyTheElevenTablesAndTheLedger(t *testing.T) {
	db := migrated(t)
	rows, err := db.Query(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type='BASE TABLE'`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, n)
	}
	sort.Strings(got)
	want := []string{
		"cities", "media_objects", "photos", "places", "schema_migrations",
		"sessions", "share_links", "travellers", "trip_cities", "trips",
		"visits", "walks",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables = %v\nwant     %v", got, want)
	}
}

// explain runs EXPLAIN and joins the plan into one string.
func explain(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN (COSTS OFF) " + q)
	if err != nil {
		t.Fatalf("EXPLAIN %s: %v", firstLine(q), err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatalf("scan: %v", err)
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// === migration 0002: the share defaults, and the rows written before it ===

// LEG SIX. A trip created through the store carries THE CLIENT'S OWN
// DEFAULTS, which are not the schema's.
//
// The client ships `sharePhotos: true, shareNotes: true, shareCoordinates:
// false` and 0001 defaulted all three to false, so a trip created by the
// server was born with photo and note sharing off — a setting nobody chose,
// differing from the same trip created on the phone. `share_coordinates`
// stays false on both sides and for the client's own stated reason: a pin on
// your accommodation is not something to hand out by link, so it has to be
// actively turned on.
//
// EXPECTED RED BEFORE 0002: exactly TWO failures, with the third assertion
// PASSING. Three failures would mean the upsert is writing the columns, which
// is a different defect that 0002 would not fix.
func TestACreatedTripCarriesTheClientsOwnSharingDefaults(t *testing.T) {
	db := migrated(t)
	store := LogbookStore{DB: db}
	id := aTraveller(t, db)

	trip, _, err := store.PutTrip(context.Background(), id, logbook.TripWrite{
		ID: ptr("autumn-crossing"), Name: ptr("Autumn crossing"),
	})
	if err != nil {
		t.Fatalf("PutTrip: %v", err)
	}

	if !trip.SharePhotos {
		t.Errorf("sharePhotos = false on a new trip, want true — the client ships " +
			"true and a trip created on the server would differ from the same " +
			"trip created on the phone")
	}
	if !trip.ShareNotes {
		t.Errorf("shareNotes = false on a new trip, want true — same reason")
	}
	if trip.ShareCoordinates {
		t.Errorf("shareCoordinates = true on a new trip, want false — a pin on your " +
			"accommodation is not something to hand out by link, so it has to be " +
			"actively turned on")
	}
}

// LEG SEVEN. 0002 BACKFILLS THE TRIPS THAT WERE ALREADY THERE (DEC-82).
//
// After the two ALTERs alone, pre-existing rows stay f|f|f while every new row
// reads t|t|f, and NOTHING IN THE TABLE CAN DISTINGUISH "written before 0002"
// from "the user turned sharing off". Those rows carry a default the client
// never had, so they are wrong data rather than a choice.
//
// THE `[false false false]` ASSERTION IS A PRECONDITION AND IS A Fatalf. Without
// it, a harness that silently migrated to head makes this leg pass while
// proving nothing at all.
func TestMigration0002BackfillsTheTripsThatWereAlreadyThere(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	if _, err := m.Migrate(ctx, db, onlyMigration(t, "0001")); err != nil {
		t.Fatalf("applying 0001 alone: %v", err)
	}
	id := aTraveller(t, db)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO trips (traveller_id, id, name) VALUES ($1::uuid, 'before', 'Before 0002')`,
		id); err != nil {
		t.Fatalf("writing a trip under 0001: %v", err)
	}

	if got := sharingOf(t, db, id, "before"); got != [3]bool{false, false, false} {
		t.Fatalf("a trip written under 0001 alone reads %v, want [false false false] — "+
			"this is the PRECONDITION, and without it a harness that silently "+
			"migrated to head would make the rest of this leg pass while proving "+
			"nothing", got)
	}

	applied, err := m.Migrate(ctx, db, migrations.FS)
	if err != nil {
		t.Fatalf("applying 0002: %v", err)
	}
	// THE EXPECTATION IS DERIVED, NOT LISTED. This line read `want [0002]` and
	// went red the moment 0003 landed — correctly, but for the wrong reason:
	// the claim is that the second run applies EVERYTHING AFTER 0001, in
	// order, and a literal turns it into "there are exactly two migrations".
	// That is the same staleness R2 found three times in the arc.
	want := everythingAfter(t, "0001")
	if strings.Join(applied, ",") != strings.Join(want, ",") {
		t.Fatalf("the second run applied %v, want %v", applied, want)
	}
	if applied[0] != "0002" {
		t.Fatalf("the second run began with %s, want 0002", applied[0])
	}

	if got := sharingOf(t, db, id, "before"); got != [3]bool{true, true, false} {
		t.Errorf("the pre-existing trip reads %v after 0002, want [true true false] — "+
			"a DEFAULT applies to rows written AFTER it, and nothing in the table "+
			"can tell 'written before 0002' from 'the user turned sharing off'", got)
	}
}

// LEG EIGHT, CATALOG TIER. The defaults are read out of pg_attrdef BY NAME.
//
// It exists because leg six goes through the store and would go green if
// somebody "fixed" the defaults in Go — which would leave every other writer,
// including a psql session and R4's seed, still producing f|f|f.
func TestTheShareDefaultsAreInTheCatalogAndNotInGo(t *testing.T) {
	db := migrated(t)

	want := map[string]string{
		"share_photos":      "true",
		"share_notes":       "true",
		"share_coordinates": "false",
	}
	rows, err := db.QueryContext(context.Background(),
		`SELECT a.attname, pg_get_expr(d.adbin, d.adrelid)
		   FROM pg_attrdef d
		   JOIN pg_attribute a ON (a.attrelid, a.attnum) = (d.adrelid, d.adnum)
		  WHERE d.adrelid = 'trips'::regclass AND a.attname LIKE 'share\_%'`)
	if err != nil {
		t.Fatalf("reading pg_attrdef: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, expr string
		if err := rows.Scan(&name, &expr); err != nil {
			t.Fatalf("scanning pg_attrdef: %v", err)
		}
		got[name] = expr
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading pg_attrdef: %v", err)
	}
	for column, expr := range want {
		if got[column] != expr {
			t.Errorf("%s DEFAULT is %q, want %q — a default the catalog does not "+
				"carry is a default only this codebase's writers get",
				column, got[column], expr)
		}
	}
}

func sharingOf(t *testing.T, db *sql.DB, travellerID, tripID string) [3]bool {
	t.Helper()
	var out [3]bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT share_photos, share_notes, share_coordinates FROM trips
		  WHERE traveller_id = $1::uuid AND id = $2`,
		travellerID, tripID).Scan(&out[0], &out[1], &out[2]); err != nil {
		t.Fatalf("reading the sharing flags of %s: %v", tripID, err)
	}
	return out
}

// onlyMigration is migrations.FS cut down to one version's pair, so a leg can
// stand at an intermediate schema. It reads the REAL files rather than
// restating them: a hand-written copy of 0001 would drift, and this leg's
// whole point is what the real 0001 leaves behind.
// everythingAfter is every migration version strictly after `version`, in the
// order the runner applies them.
func everythingAfter(t *testing.T, version string) []string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		t.Fatalf("globbing the migrations: %v", err)
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		if v := strings.SplitN(name, "_", 2)[0]; v > version {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no migration after %s — this leg needs one to be about anything", version)
	}
	return out
}

func onlyMigration(t *testing.T, version string) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	for _, suffix := range []string{"up", "down"} {
		matches, err := fs.Glob(migrations.FS, version+"_*."+suffix+".sql")
		if err != nil || len(matches) != 1 {
			t.Fatalf("globbing %s_*.%s.sql: %v (matched %v)", version, suffix, err, matches)
		}
		body, err := fs.ReadFile(migrations.FS, matches[0])
		if err != nil {
			t.Fatalf("reading %s: %v", matches[0], err)
		}
		out[matches[0]] = &fstest.MapFile{Data: body}
	}
	return out
}

// ------------------------------------------------------- 0003: THE CATALOG COMMENTS

// DEC-83's rule lives in Go and the SCHEMA SAYS SO, in the catalog, where a
// DBA's `\d+` will show it (PD-13).
//
// THE COMMENT IS THE DELIVERABLE AND THAT IS WHY IT HAS A LEG. It is the only
// artefact in this repository whose whole job is to stop the next reader
// spending an afternoon re-discovering that the obvious fix does not work: the
// composite FK changes what a visit deletion does to place_id, and the cheap
// `CHECK ((place_id IS NULL) = (visit_id IS NULL))` — the exact shape of
// photos_coordinates_paired_ck, three columns away in the same table — ABORTS
// D2's keep branch outright. Both facts were executed. A comment nobody
// asserts is a comment a later migration drops.
//
// IT ASSERTS THE SUBSTANCE AND NOT ONLY THE PRESENCE. A leg checking
// `IS NOT NULL` passes against `COMMENT ON COLUMN photos.place_id IS 'x'`,
// which is the vacuous shape this project keeps finding.
func TestTheTwoGoOnlyIntegrityColumnsCarryTheirRulingInTheCatalog(t *testing.T) {
	db := migrated(t)

	for _, c := range []struct {
		column string
		must   []string
	}{
		{"place_id", []string{"DEC-83", "composite FK", "D2's keep branch", "SECOND integrity rule"}},
		{"visit_id", []string{"DEC-83", "photos.place_id"}},
	} {
		t.Run(c.column, func(t *testing.T) {
			var comment sql.NullString
			err := db.QueryRowContext(context.Background(),
				`SELECT col_description(a.attrelid, a.attnum)
				   FROM pg_attribute a
				  WHERE a.attrelid = (current_schema() || '.photos')::regclass
				    AND a.attname = $1`, c.column).Scan(&comment)
			if err != nil {
				t.Fatalf("reading the catalog comment on photos.%s: %v", c.column, err)
			}
			if !comment.Valid || strings.TrimSpace(comment.String) == "" {
				t.Fatalf("photos.%s carries no catalog comment — DEC-83's rule is in Go "+
					"and nothing in the schema says so, which is exactly how the missing "+
					"FK indexes got there", c.column)
			}
			for _, want := range c.must {
				if !strings.Contains(comment.String, want) {
					t.Errorf("the comment on photos.%s does not mention %q: %s",
						c.column, want, comment.String)
				}
			}
		})
	}
}

// THE ALLOWLIST IS THE SAME SET IN BOTH PLACES, AND THE LEG READS BOTH RATHER
// THAN RESTATING EITHER (PD-10, DEC-58's precedent).
//
// Enforced twice by decision: in Go for the 422 that names the field, and in
// the schema as the guarantee, because a Go check can be bypassed by the next
// route somebody adds and nothing notices. Two enforcement points is two
// places for the set to be written, so this walks the CHECK's own predicate
// out of pg_constraint and asks internal/logbook about each value it finds.
//
// MUTATION: add 'image/heic' to either side alone and this reddens.
func TestTheSchemaAllowlistAndTheGoAllowlistAreTheSameSet(t *testing.T) {
	db := migrated(t)

	var predicate string
	if err := db.QueryRowContext(context.Background(),
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		  WHERE conname = 'media_objects_content_type_ck'
		    AND connamespace = current_schema()::regnamespace`).Scan(&predicate); err != nil {
		t.Fatalf("reading media_objects_content_type_ck: %v", err)
	}

	inSchema := quotedLiterals(predicate)
	if len(inSchema) == 0 {
		t.Fatalf("no literals in %q — the constraint is not an enumerated list, "+
			"so this leg cannot see what it permits", predicate)
	}

	for _, mediaType := range inSchema {
		if !logbook.ContentTypeAllowed(mediaType) {
			t.Errorf("the schema permits %q and internal/logbook refuses it — "+
				"a 422 the client never sees, and a row the schema accepts", mediaType)
		}
	}
	for _, mediaType := range logbook.AllowedContentTypes() {
		if !slices.Contains(inSchema, mediaType) {
			t.Errorf("internal/logbook permits %q and the schema refuses it — "+
				"a 422 that passes and an INSERT that raises, which reaches the "+
				"client as a 500 with no field on it", mediaType)
		}
	}

	// AND THE ROUND TRIP, because the two lists agreeing proves nothing about
	// what the database does with them.
	for _, mediaType := range logbook.AllowedContentTypes() {
		id := strings.Repeat(fmt.Sprintf("%x", len(mediaType)%16), 64)
		if _, err := db.ExecContext(context.Background(),
			`INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,$2,'x')
			 ON CONFLICT DO NOTHING`, tid, "allow@example.test"); err != nil {
			t.Fatalf("seeding a traveller: %v", err)
		}
		if _, err := db.ExecContext(context.Background(),
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,1,$3)`,
			tid, id, mediaType); err != nil {
			t.Errorf("the schema refused %q, which internal/logbook permits: %v", mediaType, err)
		}
	}
}

// quotedLiterals is every single-quoted string in a constraint predicate,
// which for an IN-list is exactly the set it permits.
func quotedLiterals(predicate string) []string {
	var out []string
	for i := 0; i < len(predicate); i++ {
		if predicate[i] != '\'' {
			continue
		}
		j := i + 1
		for j < len(predicate) && predicate[j] != '\'' {
			j++
		}
		if j >= len(predicate) {
			break
		}
		out = append(out, predicate[i+1:j])
		i = j
	}
	return out
}
