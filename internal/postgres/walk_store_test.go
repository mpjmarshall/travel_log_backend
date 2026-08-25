// N1's 'Name it' and N1's 'Discard' against a real PostgreSQL. Test-first.
//
// This file needs a database and SKIPS, saying so, when there is none.
//
// WHAT ONLY THIS FILE CAN SAY, and it is why the handler legs run against a
// fake rather than duplicating any of it:
//
//  1. A `{dismissed:true}` BODY LEAVES THE TRACK ALONE. Under a whole-state
//     upsert it writes `points='[]'`, which `walks_points_array_ck` does not
//     refuse because an empty array IS an array. No twin executes that column.
//  2. A CREATE THAT NAMES NO TRACK IS REFUSED BY NAME, and a create is what
//     `PUT /v1/walks/{id}` is on an id the log has never held.
//  3. THE TRACK ROUND-TRIPS THROUGH jsonb UNCHANGED, float for float. It is
//     written as two float8 arrays and read back through `->>` and a cast,
//     which is three conversions the types alone cannot vouch for.
package postgres

import (
	"context"
	"database/sql"
	"math/rand"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func walkStore(t *testing.T) (WalkStore, *sql.DB) {
	t.Helper()
	db := seeded(t)
	return WalkStore{DB: db}, db
}

func walkPoints(t *testing.T, db *sql.DB, id string) []logbook.LatLng {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT
			(pt.value->>'lat')::double precision, (pt.value->>'lng')::double precision
		FROM walks w
		CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
		WHERE w.traveller_id = $1::uuid AND w.id = $2 ORDER BY pt.ord`, tid, id)
	if err != nil {
		t.Fatalf("reading the track of %s: %v", id, err)
	}
	defer rows.Close()
	var out []logbook.LatLng
	for rows.Next() {
		var point logbook.LatLng
		if err := rows.Scan(&point.Lat, &point.Lng); err != nil {
			t.Fatalf("scanning a point: %v", err)
		}
		out = append(out, point)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the track of %s: %v", id, err)
	}
	return out
}

// N1's DISCARD SENDS `{dismissed:true}` AND THE TRACK SURVIVES (DEC-89,
// SAF-MAJ-6).
//
// THE BODY IS THE ONE THE CONTROL ACTUALLY SENDS, which is the whole of the
// finding. The plan's own leg — "a dismissed walk still comes back from the
// read with its points intact" — goes GREEN against a test that sends a
// synthesised whole walk, because a whole walk carries the track it is about
// to overwrite with itself. Only a partial body reaches the defect.
//
// AND IT ASSERTS THE POINTS FLOAT FOR FLOAT rather than counting them. A count
// of three is satisfied by three points that are not the ones recorded, and a
// GPS track is a recording of a day that has passed: nothing anywhere holds a
// second copy.
func TestADismissedOnlyBodyLeavesTheTrackExactlyWhereItWas(t *testing.T) {
	store, db := walkStore(t)
	before := walkPoints(t, db, "w-may")
	if len(before) == 0 {
		t.Fatalf("the fixture walk carries no track, so this leg proves nothing")
	}

	discard := true
	got, version, err := store.PutWalk(context.Background(), tid,
		logbook.WalkWrite{ID: ptr("w-may"), Dismissed: &discard})
	if err != nil {
		t.Fatalf("N1's Discard: %v", err)
	}
	if version == 0 {
		t.Error("the write moved no version, so nothing was written at all")
	}
	if !got.Dismissed {
		t.Error("dismissed = false after N1's Discard")
	}

	after := walkPoints(t, db, "w-may")
	if len(after) != len(before) {
		t.Fatalf("the track went from %d points to %d. `walks_points_array_ck` does not "+
			"refuse `[]` — an empty array IS an array — and a List<LatLng> recorded on "+
			"a day that has passed cannot be re-recorded", len(before), len(after))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("point %d: %v -> %v. A count alone is satisfied by three points "+
				"that are not the ones recorded", i, before[i], after[i])
		}
	}

	// AND THE ANSWER CARRIES THE TRACK TOO, which a response assembled from
	// the request would not: the body never mentioned the points.
	if len(got.Points) != len(before) {
		t.Errorf("the ANSWER carries %d points and the row holds %d — a response built "+
			"from the input would have the client splice an empty track into its "+
			"cached log and C2 would draw nothing", len(got.Points), len(before))
	}
}

// AND SO DOES N1's 'NAME IT', WHICH SENDS `{name}` AND NOTHING ELSE.
//
// The second control on the same route, asserted separately because the two
// take different branches of the same statement and a leg over one is not a
// leg over the other — the class R6 recorded as "apply the fix to every widget
// with the shape, not the one in the report".
func TestANameOnlyBodyLeavesTheTrackAndTheDismissedFlagAlone(t *testing.T) {
	store, db := walkStore(t)
	before := walkPoints(t, db, "w-may")

	name := "  Kibune river walk  "
	sent := &name
	got, _, err := store.PutWalk(context.Background(), tid,
		logbook.WalkWrite{ID: ptr("w-may"), Name: &sent})
	if err != nil {
		t.Fatalf("N1's 'Name it': %v", err)
	}
	if got.Name == nil || *got.Name != "Kibune river walk" {
		t.Errorf("name = %v, want the TRIMMED string — the client's own gate is "+
			"`name.trim().isEmpty`, so an untrimmed store puts a different name in "+
			"the log from the one the sheet approved", got.Name)
	}
	if got.Dismissed {
		t.Error("dismissed became true on a body that never mentioned it")
	}

	after := walkPoints(t, db, "w-may")
	if len(after) != len(before) {
		t.Errorf("the track went from %d points to %d on a NAME write", len(before), len(after))
	}
}

// A CREATE NAMES EVERY NOT NULL COLUMN OR IS REFUSED BY THE ONE IT MISSED.
//
// `PUT /v1/walks/{id}` is an upsert on a client-minted key (DEC-33), so an id
// the log has never held is a CREATE — and the INSERT tuple is checked against
// five NOT NULL columns and `walks_points_present_ck` BEFORE ON CONFLICT
// resolves it. Without this the client gets a 500 carrying a constraint name.
func TestACreateWithoutATrackIsRefusedNamingPoints(t *testing.T) {
	store, _ := walkStore(t)
	ctx := context.Background()

	full := func() logbook.WalkWrite {
		distance := 3.2
		day := logbook.At(mustDay(t, "2027-05-04"))
		points := []logbook.LatLng{{Lat: 34.96, Lng: 135.77}, {Lat: 34.97, Lng: 135.78}}
		return logbook.WalkWrite{
			ID: ptr("w-new"), TripID: ptr("kyoto-in-may"), CityID: ptr("kyoto"),
			RecordedOn: &day, DistanceKm: &distance, Points: &points,
		}
	}

	for _, tc := range []struct {
		field string
		strip func(*logbook.WalkWrite)
	}{
		{"tripId", func(w *logbook.WalkWrite) { w.TripID = nil }},
		{"cityId", func(w *logbook.WalkWrite) { w.CityID = nil }},
		{"recordedOn", func(w *logbook.WalkWrite) { w.RecordedOn = nil }},
		{"distanceKm", func(w *logbook.WalkWrite) { w.DistanceKm = nil }},
		{"points", func(w *logbook.WalkWrite) { w.Points = nil }},
	} {
		write := full()
		tc.strip(&write)
		_, _, err := store.PutWalk(ctx, tid, write)
		if got := fieldNamed(err); got != tc.field {
			t.Errorf("a create with no %s named %q, want %q (err %v)", tc.field, got, tc.field, err)
		}
	}

	// THE POSITIVE CONTROL. Without it every row above passes against a store
	// that refuses every create.
	created, _, err := store.PutWalk(ctx, tid, full())
	if err != nil {
		t.Fatalf("a complete create: %v", err)
	}
	if len(created.Points) != 2 {
		t.Errorf("the created walk carries %d points, want 2", len(created.Points))
	}
	if created.Name != nil {
		t.Errorf("name = %v on a create that named none — `Walk.needsNaming` is "+
			"`name == null && !dismissed`, so a new walk belongs on N1", created.Name)
	}
}

// A TRACK ROUND-TRIPS THROUGH jsonb FLOAT FOR FLOAT.
//
// THREE CONVERSIONS ARE INVOLVED AND NONE OF THEM IS THE TYPE SYSTEM'S:
// float8 -> jsonb numeric on the way in (through `jsonb_build_object`),
// numeric -> text -> float8 on the way out (through `->>` and a cast). A
// coordinate that lost its last digit would move a pin by about a centimetre
// and nothing else in this repository would notice.
//
// THE INPUT IS UNROUNDED, which is the case that can actually fail: seven
// decimal places survive almost any float path and seventeen significant
// digits do not.
func TestATrackRoundTripsThroughJSONBFloatForFloat(t *testing.T) {
	store, db := walkStore(t)
	random := rand.New(rand.NewSource(11))
	points := make([]logbook.LatLng, logbook.MaxWalkPoints)
	for i := range points {
		points[i] = logbook.LatLng{Lat: 34 + random.Float64(), Lng: 135 + random.Float64()}
	}

	got, _, err := store.PutWalk(context.Background(), tid,
		logbook.WalkWrite{ID: ptr("w-may"), Points: &points})
	if err != nil {
		t.Fatalf("writing a %d-point track: %v", len(points), err)
	}

	stored := walkPoints(t, db, "w-may")
	if len(stored) != len(points) || len(got.Points) != len(points) {
		t.Fatalf("wrote %d points, the row holds %d and the answer holds %d",
			len(points), len(stored), len(got.Points))
	}
	for i := range points {
		if stored[i] != points[i] {
			t.Fatalf("point %d went in as %v and came back as %v", i, points[i], stored[i])
		}
		if got.Points[i] != points[i] {
			t.Fatalf("point %d is %v in the ANSWER and %v in the request", i, got.Points[i], points[i])
		}
	}
}

// AND THE ORDER OF A TRACK IS THE ORDER IT WAS RECORDED IN.
//
// A polyline drawn in a different order is a different walk. The write pairs
// two arrays through `unnest … WITH ORDINALITY` and aggregates `ORDER BY ord`;
// the read unnests `ORDER BY pt.ord`. Neither is inherited from anything, and
// a leg that only compared SETS of points would pass against a reversal.
func TestATrackKeepsTheOrderItWasRecordedIn(t *testing.T) {
	store, _ := walkStore(t)
	points := []logbook.LatLng{
		{Lat: 34.90, Lng: 135.70}, {Lat: 34.91, Lng: 135.71},
		{Lat: 34.92, Lng: 135.72}, {Lat: 34.93, Lng: 135.73},
	}
	got, _, err := store.PutWalk(context.Background(), tid,
		logbook.WalkWrite{ID: ptr("w-may"), Points: &points})
	if err != nil {
		t.Fatalf("writing an ordered track: %v", err)
	}
	for i := range points {
		if got.Points[i] != points[i] {
			t.Fatalf("point %d = %v, want %v — the track came back in a different "+
				"order, which is a different walk", i, got.Points[i], points[i])
		}
	}
}

// AN UNKNOWN CITY OR TRIP NAMES THE FIELD RATHER THAN RAISING A FOREIGN KEY.
//
// `walks_city_fk` is RESTRICT (DEC-57) and `walks_trip_fk` is CASCADE; both
// reach the client as a 500 with nothing on it. The client can only act on
// which of ITS OWN fields is wrong.
func TestAWalkNamingAnUnknownTripOrCityNamesTheField(t *testing.T) {
	store, _ := walkStore(t)
	ctx := context.Background()

	if got := fieldNamed(walkWriteError(ctx, store, logbook.WalkWrite{
		ID: ptr("w-may"), TripID: ptr("no-such-trip"),
	})); got != "tripId" {
		t.Errorf("an unknown trip named %q, want \"tripId\"", got)
	}
	if got := fieldNamed(walkWriteError(ctx, store, logbook.WalkWrite{
		ID: ptr("w-may"), CityID: ptr("no-such-city"),
	})); got != "cityId" {
		t.Errorf("an unknown city named %q, want \"cityId\"", got)
	}
}

func walkWriteError(ctx context.Context, store WalkStore, w logbook.WalkWrite) error {
	_, _, err := store.PutWalk(ctx, tid, w)
	return err
}

// fieldNamed is `asInvalidField` with the answer a leg actually asserts on.
// It answers "" for anything that is not an InvalidFieldError, so a leg
// comparing against a field name reddens on a 500 as well as on the wrong
// field — which a bare `err != nil` would not.
func fieldNamed(err error) string {
	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) {
		return ""
	}
	return invalid.Field
}

// mustDay is a date column's value, parsed once so a leg reads as a date
// rather than as a time.Date call.
func mustDay(t *testing.T, day string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("%q is not a day: %v", day, err)
	}
	return parsed.UTC()
}
