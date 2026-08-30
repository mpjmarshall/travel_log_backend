// N1's 'Name it' and N1's 'Discard' against a real PostgreSQL.
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

// N1's discard sends `{dismissed:true}` and the track survives.
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

	if len(got.Points) != len(before) {
		t.Errorf("the ANSWER carries %d points and the row holds %d — a response built "+
			"from the input would have the client splice an empty track into its "+
			"cached log and C2 would draw nothing", len(got.Points), len(before))
	}
}

// So does N1's 'NAME it', which sends `{name}` and nothing else.
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

// A create names every not null column or is refused by the one it missed.
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

// A track round-trips through jsonb float for float.
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

// The order of A track is the order it was recorded in.
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

// an unknown city or trip names the field rather than raising A foreign key.
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
func fieldNamed(err error) string {
	var invalid logbook.InvalidFieldError
	if !asInvalidField(err, &invalid) {
		return ""
	}
	return invalid.Field
}

// mustDay is a date column's value, parsed once so a leg reads as a date
// Than as a time.Date call.
func mustDay(t *testing.T, day string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("%q is not a day: %v", day, err)
	}
	return parsed.UTC()
}
