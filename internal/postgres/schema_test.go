// migrations/0001_init.up.sql, exercised rather than inspected.
package postgres

import (
	"bytes"
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

	tokenMay   = "kyotomay9f2a"
	tokenTwo   = "secondtoken2"
	tokenThree = "thirdtoken33"
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
	mustExec(t, db, `INSERT INTO walks (traveller_id, id, trip_id, city_id, recorded_on, distance_km, points)
		VALUES ($1,'w-may','kyoto-in-may','kyoto','2027-05-03',3.2,
		        '[{"lat":34.96,"lng":135.77},{"lat":34.97,"lng":135.78}]'::jsonb)`, tid)
	mustExec(t, db, `INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'kyoto-in-may',$2)`,
		tid, logbook.HashShareToken(tokenMay))
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

// Deleting a place clears the pin and leaves the photograph standing.
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

// D2: "the track stays with the day it was recorded either way" — which is
// why walks carries no place_id at all.
func TestDeletingAPlaceLeavesEveryWalkUntouched(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `DELETE FROM places WHERE traveller_id=$1 AND id='fushimi-inari'`, tid)
	if n := count(t, db, `SELECT count(*) FROM walks WHERE traveller_id=$1`, tid); n != 1 {
		t.Errorf("walks after removing a place = %d, want 1", n)
	}
}

// D2's DELETE branch is an order of statements in Go, not a foreign key, and
// the order silently inverts the promise.
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

// D3 itemises "N pins in …" as kept.
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

// Each case clears every other child of kyoto, so the refusal it asserts is
// the one it names.
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

// The other half.
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

// The nullable-FK consequence asks to be stated rather than assumed: a
// composite FK is match simple, so a NULL cover needs no parent lookup.
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

// The non-obvious half, and the reason it is written down: account deletion
// is not made impossible by seven restrict foreign keys.
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

// The warning gives, made falsifiable: the wrong lookup does not error, it
// silently reports an unknown address.
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
// Function it actually bound to.
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

// The hazard itself, reproduced rather than argued.
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

// The counter-case, recorded because it is what a worker will reach for and
// it is the one that fails.
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

// Two halves.
func TestStopSharingThenNewLinkWorksAndOnlyOneLinkIsEverLive(t *testing.T) {
	db := seeded(t)

	_, err := db.Exec(`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'kyoto-in-may',$2)`,
		tid, logbook.HashShareToken(tokenTwo))
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
	if _, err := tx.Exec(`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'kyoto-in-may',$2)`,
		tid, logbook.HashShareToken(tokenThree)); err != nil {
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

// A token has to be unique across the whole table: GET /l/{token} arrives
// with no traveller in hand.
func TestATokenIsUniqueAcrossEveryTraveller(t *testing.T) {
	db := seeded(t)
	mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'other@example.com','x')`, otherT)
	mustExec(t, db, `INSERT INTO trips (traveller_id, id, name) VALUES ($1,'their-trip','Theirs')`, otherT)
	_, err := db.Exec(`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'their-trip',$2)`,
		otherT, logbook.HashShareToken(tokenMay))
	if err == nil {
		t.Fatal("two travellers hold the same share token; /l/{token} cannot tell them apart")
	}
	if !strings.Contains(err.Error(), "share_links_token_key") {
		t.Errorf("refusal = %v, want it to name share_links_token_key", err)
	}
}

// : the wire carries 07:05 and `date` would throw it away.
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

// The other three date-bearing columns genuinely are midnight-UTC on the
// wire.
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

// Every row here was inserted successfully on a real instance before its
// constraint existed.
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
		{"an empty content type", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'')`, []any{noSuch}, false},
		{"the XSS payload 0001's own comment said was accepted", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'text/html; <script>')`, []any{noSuch}, false},
		{"image/heic, which DEC-104 took out", "media_objects_content_type_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,$2,5,'image/heic')`, []any{noSuch}, false},
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
		{"a walk with an empty track", "walks_points_present_ck",
			`INSERT INTO walks (traveller_id,id,trip_id,city_id,recorded_on,distance_km,points) VALUES ($1,'w','kyoto-in-may','kyoto','2027-05-03',1,'[]'::jsonb)`, nil, false},
		{"an object id that is not hex sha256", "media_objects_id_sha256_ck",
			`INSERT INTO media_objects (traveller_id,id,byte_size,content_type) VALUES ($1,'not-a-digest',5,'image/jpeg')`, nil, false},
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

// , derived from pg_index.indkey rather than from A hand-kept list — which is
// the whole correction.
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

// leadsWith reports whether want is exactly the set of's first len(want)
// columns of have.
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

// The cascades, as a catalog fact.
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

		"sign_in_codes_traveller_fk": deleteCascade,

		"invite_codes_used_by_fk": deleteSetNull,
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

// The most dangerous line in 0001 is an absence.
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

// , generalised.
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

// Auto-generated constraint names are positional.
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

// Sorted in Go, not by the server.
func TestTheMigrationCreatesExactlyTheThirteenTablesAndTheLedger(t *testing.T) {
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
		"cities", "invite_codes", "media_objects", "photos", "places",
		"schema_migrations", "sessions", "share_links", "sign_in_codes",
		"travellers", "trip_cities", "trips", "visits", "walks",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables = %v\nwant     %v", got, want)
	}
}

// explain runs explain and joins the plan into one string.
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

// leg six.
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

// leg seven.
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

// leg eight, catalog tier.
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
// stand at an intermediate schema.
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

// The rule lives in Go and the schema says so, in the catalog, where a dba's
// `\d+` will show it.
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

// The allowlist is the same set in both places, and the leg reads both rather
// than restating either (the precedent).
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
// For an IN-list is exactly the set it permits.
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

// , and the precondition is the half that makes it A measurement.
func TestMigration0004HashesTheTokensThatWereAlreadyThere(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	if _, err := m.Migrate(ctx, db, migrationsUpTo(t, "0003")); err != nil {
		t.Fatalf("applying 0001 through 0003: %v", err)
	}
	id := aTraveller(t, db)
	mustExec(t, db, `INSERT INTO trips (traveller_id, id, name) VALUES ($1::uuid,'before','Before 0004')`, id)
	mustExec(t, db, `INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1::uuid,'before',$2)`,
		id, tokenMay)

	var stored string
	if err := db.QueryRowContext(ctx,
		`SELECT token FROM share_links WHERE traveller_id=$1::uuid AND trip_id='before'`,
		id).Scan(&stored); err != nil {
		t.Fatalf("the PRECONDITION failed: reading share_links.token under 0003: %v\n"+
			"    Without a plaintext row standing here, the backfill has nothing to\n"+
			"    convert and the rest of this leg passes while proving nothing.", err)
	}
	if stored != tokenMay {
		t.Fatalf("share_links.token = %q under 0003, want %q", stored, tokenMay)
	}

	applied, err := m.Migrate(ctx, db, migrations.FS)
	if err != nil {
		t.Fatalf("applying 0004: %v", err)
	}
	if want := everythingAfter(t, "0003"); strings.Join(applied, ",") != strings.Join(want, ",") {
		t.Fatalf("the second run applied %v, want %v", applied, want)
	}

	var hash []byte
	if err := db.QueryRowContext(ctx,
		`SELECT token_hash FROM share_links WHERE traveller_id=$1::uuid AND trip_id='before'`,
		id).Scan(&hash); err != nil {
		t.Fatalf("reading token_hash after 0004: %v", err)
	}
	if want := logbook.HashShareToken(tokenMay); !bytes.Equal(hash, want) {
		t.Errorf("token_hash = %x after 0004, want %x — the backfill has to hash the "+
			"token that was there, or every link ever issued stops resolving and "+
			"the revocation history stops meaning anything", hash, want)
	}
}

// The plaintext column is gone, which is the whole of the security claim.
func TestThePlaintextShareTokenColumnIsGone(t *testing.T) {
	db := migrated(t)

	var columns []string
	rows, err := db.Query(`SELECT attname FROM pg_attribute
		WHERE attrelid = 'share_links'::regclass AND attnum > 0 AND NOT attisdropped
		ORDER BY attnum`)
	if err != nil {
		t.Fatalf("reading pg_attribute: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if slices.Contains(columns, "token") {
		t.Errorf("share_links still has a `token` column: %v\n"+
			"    DEC-85 REVERSES DEC-10's plaintext half. Under DEC-67 the table\n"+
			"    revokes and KEEPS, so a dump holds every capability ever issued —\n"+
			"    live and revoked — and adding the hash beside the plaintext buys\n"+
			"    exactly none of that back.", columns)
	}
	if !slices.Contains(columns, "token_hash") {
		t.Errorf("share_links has no `token_hash` column: %v", columns)
	}
}

// The same CHECK `sessions` carries, for the same reason.
func TestAShareTokenHashIsExactlyThirtyTwoBytes(t *testing.T) {
	db := seeded(t)
	_, err := db.Exec(
		`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'autumn-crossing','\x00'::bytea)`, tid)
	if err == nil {
		t.Fatal("a one-byte share token hash was accepted")
	}
	if !strings.Contains(err.Error(), "share_links_token_hash_sha256_ck") {
		t.Errorf("refusal = %v, want it to name share_links_token_hash_sha256_ck", err)
	}
}

// migrationsUpTo is migrations.FS cut down to every version at or below
// `version`.
func migrationsUpTo(t *testing.T, version string) fs.FS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("globbing the migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, name := range names {
		if strings.SplitN(name, "_", 2)[0] > version {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		out[name] = &fstest.MapFile{Data: body}
	}
	if len(out) == 0 {
		t.Fatalf("no migration at or below %s", version)
	}
	return out
}

// 0004's down file is the one in this repository that refuses, and both
// halves of that are exercised here.
func TestTheDownOf0004RefusesWhileALinkExistsAndIsExactWhenNoneDoes(t *testing.T) {
	down := downFile(t, "0004")

	t.Run("it refuses while share_links holds a row", func(t *testing.T) {
		db := seeded(t) // seeded() writes exactly one share link
		err := applySQL(context.Background(), db, down)
		if err == nil {
			t.Fatal("the down of 0004 ran against a table holding a live link. " +
				"That either restored `token` as NULL — a capability column full of " +
				"nulls — or deleted the revocation history without saying so.")
		}
		for _, want := range []string{"cannot be reversed", "sha256 is one-way"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q:\n%v", want, err)
			}
		}
		if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1`, tid); n != 1 {
			t.Errorf("share_links holds %d rows after the refusal, want 1", n)
		}
		var hash []byte
		if err := db.QueryRow(`SELECT token_hash FROM share_links WHERE traveller_id=$1`, tid).
			Scan(&hash); err != nil {
			t.Fatalf("token_hash is unreadable after the refusal: %v", err)
		}
	})

	t.Run("it is an exact inverse on an empty table", func(t *testing.T) {
		db := migrated(t)
		if err := applySQL(context.Background(), db, down); err != nil {
			t.Fatalf("the down of 0004 failed on an empty share_links: %v", err)
		}
		mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash) VALUES ($1,'m@e.com','x')`, tid)
		mustExec(t, db, `INSERT INTO trips (traveller_id, id, name) VALUES ($1,'t','T')`, tid)
		mustExec(t, db, `INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'t',$2)`, tid, tokenMay)
		if _, err := db.Exec(`SELECT token_hash FROM share_links`); err == nil {
			t.Error("token_hash is still a column after the down of 0004")
		}
		if _, err := db.Exec(
			`INSERT INTO share_links (traveller_id, trip_id, token) VALUES ($1,'t',NULL)`, tid); err == nil {
			t.Error("share_links.token is nullable after the down of 0004 — it is half " +
				"the pre-0004 primary key and cannot be")
		}
	})
}

// downFile reads one version's.down.sql out of the embedded FS.
func downFile(t *testing.T, version string) string {
	t.Helper()
	matches, err := fs.Glob(migrations.FS, version+"_*.down.sql")
	if err != nil || len(matches) != 1 {
		t.Fatalf("globbing %s_*.down.sql: %v (matched %v)", version, err, matches)
	}
	body, err := fs.ReadFile(migrations.FS, matches[0])
	if err != nil {
		t.Fatalf("reading %s: %v", matches[0], err)
	}
	return string(body)
}

// applySQL runs a.sql file statement by statement, through the same splitter
// the migration runner uses.
func applySQL(ctx context.Context, db *sql.DB, body string) error {
	for _, statement := range splitStatements(body) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("%s: %w", firstLine(statement), err)
		}
	}
	return nil
}
