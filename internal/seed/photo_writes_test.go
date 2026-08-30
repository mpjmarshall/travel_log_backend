// R7's five routes at fixture scale, against the client's own log.
package seed_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"travellog/internal/logbook"
	"travellog/internal/postgres"
)

// theFixtureNumbers are's own, re-derived at this working tree by counting
// Than copied from a report.
const (
	fixturePhotos = 284
	fixtureFiled  = 95
	fixtureWalks  = 2
)

// nishikiOnJapan2026 is the shape the whole re-file argument rests on, and it
// is asserted before anything relies on it.
const (
	nishikiTrip     = "japan-2026"
	nishikiOccasion = 4
	refiledPhoto    = "ph-133"
)

func loadedWithNishikiChecked(t *testing.T) (*sql.DB, postgres.PhotoStore, []string) {
	t.Helper()
	db, _, _ := loaded(t)

	if got := rows(t, db, `SELECT count(*) FROM photos`); got != fixturePhotos {
		t.Fatalf("the log holds %d photographs, want %d — the fixture has moved and "+
			"every number in this file is about the one that was measured",
			got, fixturePhotos)
	}
	if got := filedWholeLog(t, db); got != fixtureFiled {
		t.Fatalf("%d photographs name a place, want %d", got, fixtureFiled)
	}

	occasions := occasionsOf(t, db, "nishiki", nishikiTrip)
	if len(occasions) != nishikiOccasion {
		t.Fatalf("nishiki holds %d occasions on %s; this leg needs %d. The fixture is "+
			"what makes 'the server must not choose' testable at all",
			len(occasions), nishikiTrip, nishikiOccasion)
	}
	if got := rows(t, db, `SELECT count(DISTINCT at) FROM visits
		WHERE place_id = 'nishiki' AND trip_id = $1`, nishikiTrip); got != 1 {
		t.Fatalf("nishiki's %d occasions on %s fall at %d distinct instants, want 1 — "+
			"the reason visits.at is timestamptz (DEC-68) is that a date column "+
			"cannot break this tie, and this leg is about the case where even the "+
			"timestamp cannot", nishikiOccasion, nishikiTrip, got)
	}
	return db, postgres.PhotoStore{DB: db}, occasions
}

func occasionsOf(t *testing.T, db *sql.DB, placeID, tripID string) []string {
	t.Helper()
	got, err := db.QueryContext(context.Background(),
		`SELECT id FROM visits WHERE place_id = $1 AND trip_id = $2 ORDER BY id`, placeID, tripID)
	if err != nil {
		t.Fatalf("reading the occasions of %s: %v", placeID, err)
	}
	defer got.Close()
	var out []string
	for got.Next() {
		var id string
		if err := got.Scan(&id); err != nil {
			t.Fatalf("scanning an occasion: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// filedWholeLog is the count that must not fall.
func filedWholeLog(t *testing.T, db *sql.DB) int {
	t.Helper()
	return rows(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`)
}

// halfFiledWholeLog counts a state the client's model has never expressed.
func halfFiledWholeLog(t *testing.T, db *sql.DB) int {
	t.Helper()
	return rows(t, db,
		`SELECT count(*) FROM photos WHERE (place_id IS NULL) <> (visit_id IS NULL)`)
}

// danglingWholeLog is R5's standing guard, run here so that "all three answer
// zero and the count still fell" is a claim this file can make directly.
func danglingWholeLog(t *testing.T, db *sql.DB) int {
	t.Helper()
	return rows(t, db, `SELECT count(*) FROM photos p
		LEFT JOIN visits v ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
		WHERE p.visit_id IS NOT NULL AND v.id IS NULL`)
}

// The server validates the occasion the client chose and does not choose one.
func TestRefilingHonoursTheOccasionTheClientNamedAndNotAnotherOnTheSameDay(t *testing.T) {
	db, store, occasions := loadedWithNishikiChecked(t)
	ctx := context.Background()

	before := filedWholeLog(t, db)
	for _, wanted := range occasions {
		if _, err := store.RefilePhoto(ctx, travellerUUID, refiledPhoto, logbook.RefileWrite{
			PlaceID: ptrTo("nishiki"), VisitID: ptrTo(wanted),
		}); err != nil {
			t.Fatalf("re-file to %s = %v, want it to succeed", wanted, err)
		}
		place, visit := filingOf(t, db, refiledPhoto)
		if visit != wanted {
			t.Errorf("visitId = %q, want %q — the client named the occasion and the "+
				"server must not pick another one. All four are asserted because a "+
				"server that PICKS agrees with the client on one of them by luck",
				visit, wanted)
		}
		if place != "nishiki" {
			t.Errorf("placeId = %q — placeId and visitId move together, always", place)
		}
	}

	if after := filedWholeLog(t, db); after != before {
		t.Errorf("photographs naming a place %d -> %d on a move BETWEEN pins", before, after)
	}
	if got := halfFiledWholeLog(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
	if got := danglingWholeLog(t, db); got != 0 {
		t.Errorf("%d photographs name a visit that is gone, want 0", got)
	}
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id = 'kibune'`); got == 0 {
		t.Error("kibune lost its occasions when a photograph moved off it")
	}
}

// A place in another city is refused, and one in the same city is not.
func TestRefilingAcrossCitiesIsRefusedAndWithinOneIsNot(t *testing.T) {
	db, store, occasions := loadedWithNishikiChecked(t)
	ctx := context.Background()
	before := filedWholeLog(t, db)

	_, err := store.RefilePhoto(ctx, travellerUUID, refiledPhoto, logbook.RefileWrite{
		PlaceID: ptrTo("bukchon"), VisitID: ptrTo("v-bukchon-0"),
	})
	if got := fieldOfError(err); got != "placeId" {
		t.Errorf("re-filing across cities named %q, want \"placeId\" (err %v)", got, err)
	}
	if place, _ := filingOf(t, db, refiledPhoto); place != "kibune" {
		t.Errorf("the photograph moved to %q on a REFUSED re-file — asserted on the "+
			"row and not on the error, because a route refusing AFTER the UPDATE "+
			"would satisfy an error assertion perfectly", place)
	}

	if _, err := store.RefilePhoto(ctx, travellerUUID, refiledPhoto, logbook.RefileWrite{
		PlaceID: ptrTo("nishiki"), VisitID: ptrTo(occasions[0]),
	}); err != nil {
		t.Errorf("re-filing within the city = %v, want it to succeed. Without this half "+
			"the leg above passes against a build that refuses every re-file", err)
	}
	if after := filedWholeLog(t, db); after != before {
		t.Errorf("photographs naming a place %d -> %d", before, after)
	}
}

// a re-file of an unfiled photograph raises the count by exactly one.
func TestRefilingAnUnfiledPhotographRaisesTheCountByExactlyOne(t *testing.T) {
	db, store, _ := loadedWithNishikiChecked(t)
	const unfiled = "ph-45"

	if place, visit := filingOf(t, db, unfiled); place != "" || visit != "" {
		t.Fatalf("%s is already filed at %q/%q, so this leg proves nothing", unfiled, place, visit)
	}
	occasions := occasionsOf(t, db, "fushimi-inari", "autumn-crossing")
	if len(occasions) == 0 {
		t.Fatalf("fushimi-inari has no occasion on autumn-crossing")
	}
	before := filedWholeLog(t, db)

	if _, err := store.RefilePhoto(context.Background(), travellerUUID, unfiled,
		logbook.RefileWrite{PlaceID: ptrTo("fushimi-inari"), VisitID: ptrTo(occasions[0])}); err != nil {
		t.Fatalf("filing an unfiled photograph: %v", err)
	}
	if after := filedWholeLog(t, db); after != before+1 {
		t.Errorf("photographs naming a place %d -> %d, want %d", before, after, before+1)
	}
	if got := halfFiledWholeLog(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
}

// A caption-only write leaves the filing alone, and all three standing guards
// are blind to the alternative.
func TestACaptionOnlyWriteLeavesAllNinetyFiveFilingsWhereTheyWere(t *testing.T) {
	db, store, _ := loadedWithNishikiChecked(t)
	const noted = "ph-0" // filed at bukchon in the fixture, and it carries a caption

	beforePlace, beforeVisit := filingOf(t, db, noted)
	if beforePlace == "" || beforeVisit == "" {
		t.Fatalf("%s is not filed, so this leg proves nothing", noted)
	}
	before := filingsOfEverything(t, db)

	note := "a new note"
	sent := &note
	if _, _, err := store.PutPhoto(context.Background(), travellerUUID,
		logbook.PhotoWrite{ID: ptrTo(noted), Caption: &sent}); err != nil {
		t.Fatalf("M2's 'Write a note': %v", err)
	}

	moved := 0
	for id, want := range before {
		if got := filingsOfEverything(t, db)[id]; got != want {
			moved++
			if moved <= 3 {
				t.Errorf("photograph %s: %q -> %q after a body that carried only a "+
					"caption", id, want, got)
			}
		}
	}
	if moved > 3 {
		t.Errorf("… and %d more", moved-3)
	}
	if got := filedWholeLog(t, db); got != fixtureFiled {
		t.Errorf("photographs naming a place %d -> %d. This is the one count that can "+
			"see it: the dangling check answers %d, the half-filed query answers %d, "+
			"and both are zero on a log whose filings have been destroyed",
			fixtureFiled, got, danglingWholeLog(t, db), halfFiledWholeLog(t, db))
	}
	if got := halfFiledWholeLog(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}

	var stored sql.NullString
	if err := db.QueryRow(`SELECT caption FROM photos WHERE id = $1`, noted).Scan(&stored); err != nil {
		t.Fatalf("reading the caption back: %v", err)
	}
	if stored.String != note {
		t.Errorf("caption = %q — without this the leg above passes against a write that "+
			"did nothing at all", stored.String)
	}
}

// deleting A filed photograph lowers the count by exactly one and takes
// nothing else.
func TestDeletingAFiledPhotographTakesOneRowAndOneFiling(t *testing.T) {
	db, store, _ := loadedWithNishikiChecked(t)
	const doomed = "ph-0"

	visits := rows(t, db, `SELECT count(*) FROM visits`)
	places := rows(t, db, `SELECT count(*) FROM places`)

	if _, err := store.DeletePhoto(context.Background(), travellerUUID, doomed); err != nil {
		t.Fatalf("D1: %v", err)
	}

	for _, row := range []struct {
		what  string
		query string
		want  int
	}{
		{"photographs", `SELECT count(*) FROM photos`, fixturePhotos - 1},
		{"naming a place", `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`, fixtureFiled - 1},
		{"occasions", `SELECT count(*) FROM visits`, visits},
		{"places", `SELECT count(*) FROM places`, places},
		{"walks", `SELECT count(*) FROM walks`, fixtureWalks},
	} {
		if got := rows(t, db, row.query); got != row.want {
			t.Errorf("%s = %d, want %d — a photograph owns nothing, so a delete takes "+
				"one row and one filing and no more", row.what, got, row.want)
		}
	}
	if got := danglingWholeLog(t, db); got != 0 {
		t.Errorf("%d photographs name a visit that is gone, want 0", got)
	}
}

// A snooze of three known and two unknown ids moves three rows and one
// version, and touches no filing.
func TestASnoozeAtFixtureScaleMovesThreeRowsAndOneVersion(t *testing.T) {
	db, store, _ := loadedWithNishikiChecked(t)
	before := rows(t, db, `SELECT logbook_version FROM travellers`)

	until := logbook.At(time.Date(2027, time.October, 19, 0, 0, 0, 0, time.UTC))
	ids := []string{"ph-45", "ph-46", "ph-47", "gone-1", "gone-2"}
	snoozed, version, err := store.SnoozePhotos(context.Background(), travellerUUID,
		logbook.SnoozeWrite{PhotoIDs: &ids, Until: &until})
	if err != nil {
		t.Fatalf("N1's 'Later': %v", err)
	}
	if len(snoozed) != 3 {
		t.Fatalf("snoozed %d photographs, want 3 — an id the log does not hold is "+
			"SKIPPED and not fatal", len(snoozed))
	}
	if got := rows(t, db, `SELECT logbook_version FROM travellers`); got != before+1 {
		t.Errorf("logbook_version %d -> %d, want ONE bump for the whole group", before, got)
	} else if version != int64(got) {
		t.Errorf("the answered version is %d and the stored one is %d", version, got)
	}
	if got := rows(t, db, `SELECT count(*) FROM photos WHERE filed_later IS NOT NULL`); got != 3 {
		t.Errorf("%d photographs carry a snooze, want 3", got)
	}
	if got := filedWholeLog(t, db); got != fixtureFiled {
		t.Errorf("photographs naming a place %d -> %d on a SNOOZE, which is a date on "+
			"a row and touches no filing at all", fixtureFiled, got)
	}
}

// A `{dismissed:true}` body leaves the track intact, point for point, and so
// does A `{name}` body.
func TestNeitherWalkControlTouchesTheTrackAndNoStoredWalkHasAnEmptyOne(t *testing.T) {
	db, _, _ := loadedWithNishikiChecked(t)
	store := postgres.WalkStore{DB: db}
	ctx := context.Background()

	if got := rows(t, db, `SELECT count(*) FROM walks`); got != fixtureWalks {
		t.Fatalf("the log holds %d walks, want %d", got, fixtureWalks)
	}
	if got := rows(t, db, `SELECT count(*) FROM walks
		WHERE jsonb_array_length(points) = 0`); got != 0 {
		t.Fatalf("%d walks in the client's own log carry an empty track. Refusing "+
			"`points: []` on SHAPE is only right because nothing produces one — that "+
			"is the difference from `visits: []`, which nine of seventeen places "+
			"legitimately emit", got)
	}

	before := trackOf(t, db, "w-busan")
	if len(before) == 0 {
		t.Fatalf("w-busan carries no track, so this leg proves nothing")
	}

	discard := true
	if _, _, err := store.PutWalk(ctx, travellerUUID,
		logbook.WalkWrite{ID: ptrTo("w-busan"), Dismissed: &discard}); err != nil {
		t.Fatalf("N1's Discard: %v", err)
	}
	assertTrack(t, db, "w-busan", before, "N1's Discard")

	name := "Gamcheon and back"
	sent := &name
	if _, _, err := store.PutWalk(ctx, travellerUUID,
		logbook.WalkWrite{ID: ptrTo("w-busan"), Name: &sent}); err != nil {
		t.Fatalf("N1's 'Name it': %v", err)
	}
	assertTrack(t, db, "w-busan", before, "N1's 'Name it'")

	var dismissed bool
	var stored sql.NullString
	if err := db.QueryRow(`SELECT dismissed, name FROM walks WHERE id = 'w-busan'`).
		Scan(&dismissed, &stored); err != nil {
		t.Fatalf("reading the walk back: %v", err)
	}
	if !dismissed || stored.String != name {
		t.Errorf("dismissed=%v name=%q, want true and %q", dismissed, stored.String, name)
	}

	if got := filedWholeLog(t, db); got != fixtureFiled {
		t.Errorf("photographs naming a place %d -> %d on a WALK write", fixtureFiled, got)
	}
}

func trackOf(t *testing.T, db *sql.DB, walkID string) []logbook.LatLng {
	t.Helper()
	got, err := db.QueryContext(context.Background(), `SELECT
			(pt.value->>'lat')::double precision, (pt.value->>'lng')::double precision
		FROM walks w
		CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
		WHERE w.id = $1 ORDER BY pt.ord`, walkID)
	if err != nil {
		t.Fatalf("reading the track of %s: %v", walkID, err)
	}
	defer got.Close()
	var out []logbook.LatLng
	for got.Next() {
		var point logbook.LatLng
		if err := got.Scan(&point.Lat, &point.Lng); err != nil {
			t.Fatalf("scanning a point: %v", err)
		}
		out = append(out, point)
	}
	return out
}

// assertTrack compares point for point and not by length.
func assertTrack(t *testing.T, db *sql.DB, walkID string, want []logbook.LatLng, after string) {
	t.Helper()
	got := trackOf(t, db, walkID)
	if len(got) != len(want) {
		t.Fatalf("after %s the track of %s went from %d points to %d. "+
			"`walks_points_array_ck` does not refuse `[]` — an empty array IS an "+
			"array", after, walkID, len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("after %s, point %d: %v -> %v", after, i, want[i], got[i])
		}
	}
}

func filingOf(t *testing.T, db *sql.DB, photoID string) (place, visit string) {
	t.Helper()
	var p, v sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT place_id, visit_id FROM photos WHERE id = $1`, photoID).Scan(&p, &v); err != nil {
		t.Fatalf("reading the filing of %s: %v", photoID, err)
	}
	return p.String, v.String
}

// filingsOfEverything is `filings` under a name that says it is whole-log.
func filingsOfEverything(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	return filings(t, db)
}

// fieldOfError answers the one additive key, or "" for anything that is not an
// InvalidFieldError.
func fieldOfError(err error) string {
	for err != nil {
		if invalid, is := err.(logbook.InvalidFieldError); is {
			return invalid.Field
		}
		unwrapped, can := err.(interface{ Unwrap() error })
		if !can {
			return ""
		}
		err = unwrapped.Unwrap()
	}
	return ""
}
