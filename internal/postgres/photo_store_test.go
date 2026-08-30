// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change' against a real
// PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func photoStore(t *testing.T) (PhotoStore, *sql.DB) {
	t.Helper()
	db := seeded(t)
	mustExec(t, db, `UPDATE media_objects SET uploaded_at = now()
		WHERE traveller_id = $1::uuid AND id = $2`, tid, assetA)
	return PhotoStore{DB: db}, db
}

// filedCount is the count that must not fall, whole-log.
func filedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	return count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`)
}

// halfFiled counts a state the client's model has never expressed: a photograph
// naming a place with no occasion.
func halfFiled(t *testing.T, db *sql.DB) int {
	t.Helper()
	return count(t, db,
		`SELECT count(*) FROM photos WHERE (place_id IS NULL) <> (visit_id IS NULL)`)
}

func photoFiling(t *testing.T, db *sql.DB, id string) (place, visit string) {
	t.Helper()
	var p, v sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT place_id, visit_id FROM photos WHERE traveller_id = $1::uuid AND id = $2`,
		tid, id).Scan(&p, &v); err != nil {
		t.Fatalf("reading the filing of %s: %v", id, err)
	}
	return p.String, v.String
}

// A caption-only put does not unfile the photograph.
func TestACaptionOnlyWriteLeavesThePinAndTheOccasionExactlyWhereTheyWere(t *testing.T) {
	store, db := photoStore(t)
	beforePlace, beforeVisit := photoFiling(t, db, "p-may")
	if beforePlace == "" || beforeVisit == "" {
		t.Fatalf("p-may is not filed (%q/%q), so this leg proves nothing", beforePlace, beforeVisit)
	}
	beforeFiled := filedCount(t, db)

	note := "Last morning, and the lanes were empty for once"
	sent := &note
	got, _, err := store.PutPhoto(context.Background(), tid,
		logbook.PhotoWrite{ID: ptr("p-may"), Caption: &sent})
	if err != nil {
		t.Fatalf("M2's 'Write a note': %v", err)
	}
	if got.Caption == nil || *got.Caption != note {
		t.Fatalf("the caption did not land (%v), so the rest of this leg is about a "+
			"write that did nothing", got.Caption)
	}

	afterPlace, afterVisit := photoFiling(t, db, "p-may")
	if afterPlace != beforePlace || afterVisit != beforeVisit {
		t.Errorf("filing %q/%q -> %q/%q on a body that never mentioned it. The log now "+
			"has NO record that the photograph was ever there",
			beforePlace, beforeVisit, afterPlace, afterVisit)
	}
	if got.PlaceID == nil || *got.PlaceID != beforePlace {
		t.Errorf("the ANSWER carries placeId %v, want %q", got.PlaceID, beforePlace)
	}
	if got.VisitID == nil || *got.VisitID != beforeVisit {
		t.Errorf("the ANSWER carries visitId %v, want %q", got.VisitID, beforeVisit)
	}

	if after := filedCount(t, db); after != beforeFiled {
		t.Errorf("photographs naming a place: %d -> %d. The dangling check sees no "+
			"dangling reference, the place-without-occasion query sees no place, and "+
			"the pair-agreement assertion sees two NULLs that agree — this is the "+
			"only count that can see it", beforeFiled, after)
	}
	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
}

// an empty or whitespace caption stores null and never the empty string.
func TestAWhitespaceCaptionStoresNullAndClearsTheNote(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()

	first := "a note"
	sent := &first
	if _, _, err := store.PutPhoto(ctx, tid, logbook.PhotoWrite{ID: ptr("p-may"), Caption: &sent}); err != nil {
		t.Fatalf("writing a note: %v", err)
	}

	for _, blank := range []string{"", "   ", "\t\n "} {
		value := blank
		cleared := &value
		got, _, err := store.PutPhoto(ctx, tid, logbook.PhotoWrite{ID: ptr("p-may"), Caption: &cleared})
		if err != nil {
			t.Fatalf("clearing with %q: %v", blank, err)
		}
		if got.Caption != nil {
			t.Errorf("caption = %q after clearing with %q, want NULL — M2's two note "+
				"blocks are both guarded by `caption != null`, so '' is an empty box "+
				"with no way back out of it", *got.Caption, blank)
		}
		var stored sql.NullString
		if err := db.QueryRow(`SELECT caption FROM photos WHERE id = 'p-may'`).Scan(&stored); err != nil {
			t.Fatalf("reading the caption back: %v", err)
		}
		if stored.Valid {
			t.Errorf("the COLUMN holds %q after clearing with %q, want NULL", stored.String, blank)
		}
	}
}

// A create names every not null column or is refused by the one it missed,
// Its asset has to be committed.
func TestACreateNeedsItsFourColumnsAndACommittedAsset(t *testing.T) {
	store, _ := photoStore(t)
	ctx := context.Background()

	full := func() logbook.PhotoWrite {
		taken := logbook.At(time.Date(2027, time.May, 3, 7, 6, 0, 0, time.UTC))
		return logbook.PhotoWrite{
			ID: ptr("p-new"), TripID: ptr("kyoto-in-may"), CityID: ptr("kyoto"),
			TakenAt: &taken, Asset: ptr(assetA),
		}
	}

	for _, tc := range []struct {
		field string
		strip func(*logbook.PhotoWrite)
	}{
		{"tripId", func(p *logbook.PhotoWrite) { p.TripID = nil }},
		{"cityId", func(p *logbook.PhotoWrite) { p.CityID = nil }},
		{"takenAt", func(p *logbook.PhotoWrite) { p.TakenAt = nil }},
		{"asset", func(p *logbook.PhotoWrite) { p.Asset = nil }},
		{"asset", func(p *logbook.PhotoWrite) { p.Asset = ptr(assetB) }},
	} {
		write := full()
		tc.strip(&write)
		_, _, err := store.PutPhoto(ctx, tid, write)
		if got := fieldNamed(err); got != tc.field {
			t.Errorf("a create missing %s named %q, want %q (err %v)", tc.field, got, tc.field, err)
		}
	}

	created, _, err := store.PutPhoto(ctx, tid, full())
	if err != nil {
		t.Fatalf("a complete create: %v", err)
	}
	if created.PlaceID != nil || created.VisitID != nil {
		t.Errorf("a created photograph is filed at %v/%v — it should arrive unfiled, "+
			"because K1's filing sheet and M2.2's 'Change' are what file one",
			created.PlaceID, created.VisitID)
	}
}

// D1 takes one row and the count falls by exactly one.
func TestDeletingAFiledPhotographLowersTheCountByExactlyOne(t *testing.T) {
	store, db := photoStore(t)
	before := filedCount(t, db)
	photos := count(t, db, `SELECT count(*) FROM photos`)

	version, err := store.DeletePhoto(context.Background(), tid, "p-may")
	if err != nil {
		t.Fatalf("D1: %v", err)
	}
	if version == 0 {
		t.Error("the delete moved no version")
	}
	if got := count(t, db, `SELECT count(*) FROM photos`); got != photos-1 {
		t.Errorf("photographs %d -> %d, want %d", photos, got, photos-1)
	}
	if got := filedCount(t, db); got != before-1 {
		t.Errorf("photographs naming a place %d -> %d, want %d — a delete lowers the "+
			"count by a KNOWN amount and never by more", before, got, before-1)
	}
	if got := count(t, db, `SELECT count(*) FROM visits WHERE id = 'v-fushimi-may'`); got != 1 {
		t.Errorf("the occasion is gone. A photograph does not own the occasion it was " +
			"filed to — the trip does")
	}
}

// An unknown id is a success that moves no version.
func TestDeletingAPhotographThatIsNotThereMovesNoVersion(t *testing.T) {
	store, db := photoStore(t)
	before := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid)

	version, err := store.DeletePhoto(context.Background(), tid, "no-such-photo")
	if err != nil {
		t.Fatalf("deleting an absent photograph: %v — the caller asked for it to be "+
			"absent and it is", err)
	}
	if version != int64(before) {
		t.Errorf("the version moved %d -> %d on a delete that deleted nothing", before, version)
	}
	if after := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid); after != before {
		t.Errorf("logbook_version %d -> %d — the bump rides the transaction and has to "+
			"roll back with it", before, after)
	}
}

// A snooze of three known and two unknown ids snoozes three, skips two, and
// bumps the version once.
func TestASnoozeSkipsWhatIsGoneAndBumpsTheVersionOnce(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO photos (traveller_id, id, trip_id, city_id, taken_at, asset)
		VALUES ($1,'p-unfiled','kyoto-in-may','kyoto','2027-05-03T08:00:00Z',$2)`, tid, assetA)

	before := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid)
	beforeFiled := filedCount(t, db)
	until := logbook.At(time.Date(2027, time.October, 19, 0, 0, 0, 0, time.UTC))
	ids := []string{"p-may", "p-autumn", "p-unfiled", "gone-1", "gone-2"}

	snoozed, version, err := store.SnoozePhotos(ctx, tid,
		logbook.SnoozeWrite{PhotoIDs: &ids, Until: &until})
	if err != nil {
		t.Fatalf("N1's 'Later': %v", err)
	}
	if len(snoozed) != 3 {
		t.Fatalf("snoozed %d photographs, want 3 — an id the log does not hold is "+
			"SKIPPED and not fatal", len(snoozed))
	}
	for i, want := range []string{"p-autumn", "p-may", "p-unfiled"} {
		if snoozed[i].ID != want {
			t.Errorf("answer[%d] = %s, want %s — an UPDATE's RETURNING order is the "+
				"order rows happened to be visited, and two reads of one write that "+
				"differ are two bodies under one ETag", i, snoozed[i].ID, want)
		}
		if snoozed[i].FiledLater == nil || !snoozed[i].FiledLater.Time().Equal(until.Time()) {
			t.Errorf("%s carries filedLater %v, want %v", want, snoozed[i].FiledLater, until)
		}
	}

	after := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid)
	if after != before+1 {
		t.Errorf("logbook_version %d -> %d, want ONE bump for the whole group", before, after)
	}
	if version != int64(after) {
		t.Errorf("the answered version is %d and the stored one is %d", version, after)
	}
	if got := count(t, db, `SELECT count(*) FROM photos WHERE filed_later IS NOT NULL`); got != 3 {
		t.Errorf("%d photographs carry a snooze, want 3", got)
	}
	if got := filedCount(t, db); got != beforeFiled {
		t.Errorf("photographs naming a place %d -> %d on a SNOOZE", beforeFiled, got)
	}
}

// A group that matches nothing writes nothing and moves no version.
func TestASnoozeThatMatchesNothingWritesNothingAndSaysSo(t *testing.T) {
	store, db := photoStore(t)
	before := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid)

	until := logbook.At(time.Date(2027, time.October, 19, 0, 0, 0, 0, time.UTC))
	ids := []string{"gone-1", "gone-2"}
	snoozed, version, err := store.SnoozePhotos(context.Background(), tid,
		logbook.SnoozeWrite{PhotoIDs: &ids, Until: &until})
	if err != nil {
		t.Fatalf("a snooze that matches nothing: %v", err)
	}
	if snoozed == nil {
		t.Error("the answer is a nil slice, which marshals to `null` — the client reads " +
			"it as a List and throws")
	}
	if len(snoozed) != 0 {
		t.Errorf("snoozed %d photographs against a group of two ids the log does not hold",
			len(snoozed))
	}
	if version != int64(before) {
		t.Errorf("the answered version is %d and nothing was written; the stored one is %d",
			version, before)
	}
	if after := count(t, db, `SELECT logbook_version FROM travellers WHERE id = $1::uuid`, tid); after != before {
		t.Errorf("logbook_version %d -> %d on a write that wrote nothing", before, after)
	}
	if got := count(t, db, `SELECT count(*) FROM photos WHERE filed_later IS NOT NULL`); got != 0 {
		t.Errorf("%d photographs carry a snooze, want 0", got)
	}
}

// twoOccasionsOnOneTrip gives `wishlist-pin` two occasions on `kyoto-in-may`,
// six hours apart on one day.
func twoOccasionsOnOneTrip(t *testing.T, db *sql.DB) (older, newer string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-wish-morning','wishlist-pin','kyoto-in-may',0,'2027-05-04T07:00:00Z'),
		       ($1,'v-wish-evening','wishlist-pin','kyoto-in-may',1,'2027-05-04T19:00:00Z')`, tid)
	return "v-wish-morning", "v-wish-evening"
}

// The server validates the occasion the client chose and does not choose one.
func TestRefilingHonoursTheOccasionTheClientNamedAndNotAnotherOnTheSameDay(t *testing.T) {
	store, db := photoStore(t)
	older, newer := twoOccasionsOnOneTrip(t, db)

	for _, wanted := range []string{older, newer} {
		got, err := store.RefilePhoto(context.Background(), tid, "p-may",
			logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &wanted})
		if err != nil {
			t.Fatalf("re-file to %s = %v, want it to succeed", wanted, err)
		}

		place, visit := photoFiling(t, db, "p-may")
		if visit != wanted {
			t.Errorf("visitId = %q, want %q — the client named the occasion and the "+
				"server must not pick another one, least of all one on the same day. "+
				"Both occasions are asserted because a server that PICKS agrees with "+
				"the client on one of them by luck", visit, wanted)
		}
		if place != "wishlist-pin" {
			t.Errorf("placeId = %q — placeId and visitId move together, always", place)
		}
		if got.Photo.VisitID == nil || *got.Photo.VisitID != wanted {
			t.Errorf("the ANSWER carries visitId %v, want %q", got.Photo.VisitID, wanted)
		}
		if got.Document != nil {
			t.Error("the answer carries the whole log for a re-file that minted " +
				"nothing — one entity moved and DEC-32's splice is the right answer")
		}
		if got := halfFiled(t, db); got != 0 {
			t.Errorf("%d photographs are half-filed, want 0", got)
		}
	}
}

// A place in another city is refused, and the other half says A place in the
// same city is not.
func TestRefilingToAPlaceInAnotherCityIsRefusedAndToOneInTheSameCityIsNot(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO places (traveller_id, id, city_id, name, lat, lng)
		VALUES ($1,'bukchon','seoul','Bukchon',37.58,126.98)`, tid)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-bukchon-0','bukchon','autumn-crossing',0,'2027-09-30T12:00:00Z')`, tid)

	_, err := store.RefilePhoto(ctx, tid, "p-autumn",
		logbook.RefileWrite{PlaceID: ptr("bukchon"), VisitID: ptr("v-bukchon-0")})
	if got := fieldNamed(err); got != "placeId" {
		t.Errorf("re-filing across cities named %q, want \"placeId\" (err %v)", got, err)
	}
	if place, _ := photoFiling(t, db, "p-autumn"); place != "fushimi-inari" {
		t.Errorf("the photograph moved to %q on a refused re-file — asserted on the "+
			"ROW and not on the error, because a route that refused AFTER the UPDATE "+
			"would satisfy an error assertion perfectly", place)
	}

	older, _ := twoOccasionsOnOneTrip(t, db)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-wish-autumn','wishlist-pin','autumn-crossing',2,'2027-09-20T07:00:00Z')`, tid)
	_ = older
	if _, err := store.RefilePhoto(ctx, tid, "p-autumn",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: ptr("v-wish-autumn")}); err != nil {
		t.Errorf("re-filing within the city = %v, want it to succeed. Without this half "+
			"the leg above passes against a build that refuses every re-file", err)
	}
}

// an occasion belonging to another place or another trip is refused by name.
func TestRefilingToAnotherPlacesOrAnotherTripsOccasionIsRefused(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	older, _ := twoOccasionsOnOneTrip(t, db)

	_, err := store.RefilePhoto(ctx, tid, "p-may",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: ptr("v-fushimi-may")})
	if got := fieldNamed(err); got != "visitId" {
		t.Errorf("another place's occasion named %q, want \"visitId\" (err %v)", got, err)
	}

	_, err = store.RefilePhoto(ctx, tid, "p-autumn",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older})
	if got := fieldNamed(err); got != "visitId" {
		t.Errorf("another trip's occasion named %q, want \"visitId\" (err %v)", got, err)
	}

	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed after two refusals, want 0", got)
	}
}

// a re-file that opens an occasion rewrites the place's ordinals in `at`
// desc, and answers the whole envelope.
func TestARefileThatOpensAnOccasionRenumbersThePlaceAndAnswersTheWholeLog(t *testing.T) {
	store, db := photoStore(t)
	twoOccasionsOnOneTrip(t, db)

	at := logbook.At(time.Date(2027, time.May, 4, 12, 0, 0, 0, time.UTC))
	got, err := store.RefilePhoto(context.Background(), tid, "p-may", logbook.RefileWrite{
		PlaceID: ptr("wishlist-pin"), VisitID: ptr("v-wish-noon"), VisitAt: &at,
	})
	if err != nil {
		t.Fatalf("a re-file that opens an occasion: %v", err)
	}
	if got.Document == nil {
		t.Error("the answer is a bare photograph for a re-file that MINTED an occasion. " +
			"The place gained a visit and every one of its ordinals was rewritten, so " +
			"the phone cannot splice what it was not sent")
	}

	rows, err := db.QueryContext(context.Background(),
		`SELECT id FROM visits WHERE place_id = 'wishlist-pin' ORDER BY ordinal`)
	if err != nil {
		t.Fatalf("reading the ordinals: %v", err)
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning an ordinal: %v", err)
		}
		order = append(order, id)
	}
	want := []string{"v-wish-evening", "v-wish-noon", "v-wish-morning"}
	if len(order) != len(want) {
		t.Fatalf("the place holds %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ordinals: %v, want %v — the rewrite is `at` DESC and the new "+
				"occasion belongs BETWEEN the two that were there", order, want)
		}
	}

	place, visit := photoFiling(t, db, "p-may")
	if place != "wishlist-pin" || visit != "v-wish-noon" {
		t.Errorf("filing = %q/%q, want wishlist-pin/v-wish-noon", place, visit)
	}
	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
}

// Opening an occasion needs `visitAt`, because an occasion happens at A
// moment.
func TestOpeningAnOccasionWithoutAMomentIsRefusedNamingVisitAt(t *testing.T) {
	store, db := photoStore(t)
	before := filedCount(t, db)

	_, err := store.RefilePhoto(context.Background(), tid, "p-may",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: ptr("v-wish-fresh")})
	if got := fieldNamed(err); got != "visitAt" {
		t.Errorf("a fresh occasion with no moment named %q, want \"visitAt\" (err %v)", got, err)
	}
	if got := count(t, db, `SELECT count(*) FROM visits WHERE id = 'v-wish-fresh'`); got != 0 {
		t.Error("the occasion was opened anyway")
	}
	if got := filedCount(t, db); got != before {
		t.Errorf("photographs naming a place %d -> %d on a refused re-file", before, got)
	}
}

// a re-file of an unfiled photograph raises the count, and one between pins
// leaves it where it was — and the old place keeps its occasion.
func TestARefileRaisesTheCountOnceAndNeverTidiesTheOldOccasionAway(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	older, _ := twoOccasionsOnOneTrip(t, db)
	mustExec(t, db, `INSERT INTO photos (traveller_id, id, trip_id, city_id, taken_at, asset)
		VALUES ($1,'p-unfiled','kyoto-in-may','kyoto','2027-05-04T08:00:00Z',$2)`, tid, assetA)

	before := filedCount(t, db)
	if _, err := store.RefilePhoto(ctx, tid, "p-unfiled",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older}); err != nil {
		t.Fatalf("filing an unfiled photograph: %v", err)
	}
	if got := filedCount(t, db); got != before+1 {
		t.Errorf("photographs naming a place %d -> %d, want %d — a re-file RAISES the "+
			"count when the photograph was unfiled", before, got, before+1)
	}

	raised := filedCount(t, db)
	if _, err := store.RefilePhoto(ctx, tid, "p-may",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older}); err != nil {
		t.Fatalf("moving a photograph between pins: %v", err)
	}
	if got := filedCount(t, db); got != raised {
		t.Errorf("photographs naming a place %d -> %d on a move BETWEEN pins", raised, got)
	}
	if got := count(t, db, `SELECT count(*) FROM visits WHERE id = 'v-fushimi-may'`); got != 1 {
		t.Error("the old place's occasion was tidied away. A visit belongs to the TRIP " +
			"and not to the photograph — the same sentence D3's 'pins are kept' row " +
			"rests on")
	}
	if got := count(t, db, `SELECT count(*) FROM places WHERE id = 'fushimi-inari'`); got != 1 {
		t.Error("the old place is gone")
	}
	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
}

// A photograph this log does not hold is a 404 and not A create.
func TestRefilingAPhotographThatIsNotThereIsANotFoundAndNotACreate(t *testing.T) {
	store, db := photoStore(t)
	older, _ := twoOccasionsOnOneTrip(t, db)

	_, err := store.RefilePhoto(context.Background(), tid, "no-such-photo",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older})
	if !errors.Is(err, logbook.ErrNoPhoto) {
		t.Errorf("re-filing an absent photograph = %v, want logbook.ErrNoPhoto", err)
	}
	if got := count(t, db, `SELECT count(*) FROM photos WHERE id = 'no-such-photo'`); got != 0 {
		t.Error("a row was created")
	}
}
