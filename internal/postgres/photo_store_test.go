// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change' against a real
// PostgreSQL. Test-first.
//
// This file needs a database and SKIPS, saying so, when there is none.
//
// WHAT ONLY THIS FILE CAN SAY, and it is why the handler legs run against a
// fake rather than duplicating any of it:
//
//  1. A CAPTION-ONLY PUT LEAVES THE FILING ALONE. The type makes it
//     unreachable and the STATEMENT is where that becomes true of a row —
//     `upsertPhotoSQL` names neither column, and only a real UPDATE can show
//     that.
//  2. THE COUNT THAT MUST NOT FALL. `count(*) WHERE place_id IS NOT NULL` is
//     the one assertion the dangling check, the place-without-occasion query
//     and the pair-agreement check are all blind to (DEC-89, SAF-MAJ-5).
//  3. THE RE-FILE HONOURS THE OCCASION THE CLIENT NAMED. A server picking for
//     itself is a plausible implementation whose every field is individually
//     valid, and only a place with more than one occasion on one trip can tell
//     the two apart.
//  4. THE ORDINAL REWRITE. A permutation written over itself collides with
//     `visits_place_ordinal_uq` mid-statement, which no twin executes.
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
	// THE FIXTURE'S OBJECTS ARE BEGUN AND NOT COMMITTED, which is what makes
	// the asset refusal testable — and it means every leg that WRITES an asset
	// has to commit one first. `city_store_test.go` does the same for a cover.
	mustExec(t, db, `UPDATE media_objects SET uploaded_at = now()
		WHERE traveller_id = $1::uuid AND id = $2`, tid, assetA)
	return PhotoStore{DB: db}, db
}

// filedCount is DEC-89's count that must not fall, whole-log.
func filedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	return count(t, db, `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`)
}

// halfFiled is the state the client's model has never expressed: a photograph
// naming a place with no occasion. Asserted BESIDE filedCount, because a
// dangling-reference count of zero is satisfied by unfiling everything.
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

// ==================================================== M2's NOTE

// A CAPTION-ONLY PUT DOES NOT UNFILE THE PHOTOGRAPH (DEC-89, SAF-MAJ-5).
//
// THE BODY IS THE ONE M2's 'Write a note' ACTUALLY SENDS. `setPhotoCaption`
// owns the note and nothing else, and under the whole-state convention that
// body writes `place_id = NULL, visit_id = NULL` beside the caption — measured
// by the safety lens on `ph-0`, and green against all three standing guards
// afterwards, because the reference is gone rather than dangling, there is no
// place left to be occasion-less, and two NULLs agree.
//
// SO THE COUNT THAT MUST NOT FALL IS ASSERTED HERE and not only the pair. The
// pair-agreement check passes on a destroyed filing; the count does not.
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

	// THE COUNT THAT MUST NOT FALL. All three standing guards are blind here.
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

// AN EMPTY OR WHITESPACE CAPTION STORES NULL AND NEVER THE EMPTY STRING.
//
// The client's rule and 0003's constraint. Asserted on the COLUMN and not on
// the answer, because a store that wrote ” and then trimmed on the way out
// would satisfy an assertion about the response — and `photos_caption_present_ck`
// would 500 the write instead of naming a field.
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

// A CREATE NAMES EVERY NOT NULL COLUMN OR IS REFUSED BY THE ONE IT MISSED, AND
// ITS ASSET HAS TO BE COMMITTED.
//
// `photos_asset_fk` guarantees the ROW exists and says nothing about
// `uploaded_at` — an FK cannot see a column it does not reference. So a
// photograph could otherwise name an object that was begun and never uploaded,
// which is bytes the user does not have, drawn as a broken plate for ever.
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
		// assetB is BEGUN AND NOT COMMITTED in the fixture, which is exactly
		// the state the bucket-versus-database seam leaves behind.
		{"asset", func(p *logbook.PhotoWrite) { p.Asset = ptr(assetB) }},
	} {
		write := full()
		tc.strip(&write)
		_, _, err := store.PutPhoto(ctx, tid, write)
		if got := fieldNamed(err); got != tc.field {
			t.Errorf("a create missing %s named %q, want %q (err %v)", tc.field, got, tc.field, err)
		}
	}

	// THE POSITIVE CONTROL, and the second half of it is the one that matters:
	// a photograph arrives UNFILED, so `place_id` and `visit_id` default to
	// NULL rather than being writable from this route at all.
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

// ==================================================== D1

// D1 TAKES ONE ROW AND THE COUNT FALLS BY EXACTLY ONE.
//
// Nothing in this schema references a photograph, so there is no cascade and
// no sheet copy to implement. What there IS is the count: deleting a FILED
// photograph lowers `place_id IS NOT NULL` by one, and by one only.
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
	// AND IT TAKES NOTHING ELSE. The occasion belongs to the trip, not to the
	// photograph, so it stands.
	if got := count(t, db, `SELECT count(*) FROM visits WHERE id = 'v-fushimi-may'`); got != 1 {
		t.Errorf("the occasion is gone. A photograph does not own the occasion it was " +
			"filed to — the trip does")
	}
}

// AND AN UNKNOWN ID IS A SUCCESS THAT MOVES NO VERSION.
//
// The client's `deletePhoto` answers true for an id the log does not hold, and
// a bump on a retried delete throws away the phone's whole cached document.
// DEC-103 makes the retry concrete: a delete against a build that predates the
// route is retried.
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

// ==================================================== N1's 'LATER'

// A SNOOZE OF THREE KNOWN AND TWO UNKNOWN IDS SNOOZES THREE, SKIPS TWO, AND
// BUMPS THE VERSION ONCE.
//
// ONE BUMP AND NOT ONE PER PHOTOGRAPH: `logbook_version` is the ETag's second
// half, so N bumps for one user action hand the client N-1 versions it can
// never have held and invalidate its cached document N times for one write.
//
// AN UNKNOWN ID IS SKIPPED RATHER THAN FATAL, matching the client: "the row
// was derived from the log a frame ago and a photograph deleted since is one
// that no longer needs filing".
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
	// THE COUNT THAT MUST NOT FALL. A snooze is a date on a row and touches no
	// filing at all.
	if got := filedCount(t, db); got != beforeFiled {
		t.Errorf("photographs naming a place %d -> %d on a SNOOZE", beforeFiled, got)
	}
}

// AND A GROUP THAT MATCHES NOTHING WRITES NOTHING AND MOVES NO VERSION.
//
// The client's own "returns false without writing when the group is empty":
// a commit that changes nothing is a file write and a state assignment for no
// reason. Here it is an ETag the phone would throw its whole cached document
// away for.
//
// THE ANSWER IS AN EMPTY SLICE AND NEVER nil, because a nil slice marshals to
// `null` and the client reads every list key in this document as a List.
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

// ==================================================== M2.2's 'CHANGE'

// twoOccasionsOnOneTrip gives `wishlist-pin` two occasions on `kyoto-in-may`,
// SIX HOURS APART ON ONE DAY.
//
// THE SAME-DAY PART IS THE WHOLE POINT AND IT MIRRORS THE CLIENT'S OWN LOG:
// `nishiki` holds four occasions on `japan-2026`, all four on 2026-09-18. A
// server picking the occasion itself cannot even use the date to break the
// tie, which is why `visits.at` is timestamptz (DEC-68) rather than date.
func twoOccasionsOnOneTrip(t *testing.T, db *sql.DB) (older, newer string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-wish-morning','wishlist-pin','kyoto-in-may',0,'2027-05-04T07:00:00Z'),
		       ($1,'v-wish-evening','wishlist-pin','kyoto-in-may',1,'2027-05-04T19:00:00Z')`, tid)
	return "v-wish-morning", "v-wish-evening"
}

// THE SERVER VALIDATES THE OCCASION THE CLIENT CHOSE AND DOES NOT CHOOSE ONE.
//
// This is the plan's own named failing test, at mechanism scale. The version
// against the client's whole log — where the place really does hold four
// occasions on one day — is in internal/seed.
//
// A SERVER THAT PICKS THE VISIT ITSELF is the obvious implementation: an
// unordered `SELECT id FROM visits WHERE place_id = $1 AND trip_id = $2 LIMIT 1`.
// It files the photograph to whichever row the planner happened to return, and
// EVERY FIELD IN THE ANSWER IS INDIVIDUALLY VALID. The occasion named here is
// deliberately NOT the newest, so a picker that took the newest is caught as
// surely as one that took the first.
func TestRefilingHonoursTheOccasionTheClientNamedAndNotAnotherOnTheSameDay(t *testing.T) {
	store, db := photoStore(t)
	older, newer := twoOccasionsOnOneTrip(t, db)
	_ = newer

	got, err := store.RefilePhoto(context.Background(), tid, "p-may",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older})
	if err != nil {
		t.Fatalf("re-file = %v, want it to succeed", err)
	}

	place, visit := photoFiling(t, db, "p-may")
	if visit != older {
		t.Errorf("visitId = %q, want %q — the client named the occasion and the server "+
			"must not pick another one, least of all one on the same day", visit, older)
	}
	if place != "wishlist-pin" {
		t.Errorf("placeId = %q — placeId and visitId move together, always", place)
	}
	if got.Photo.VisitID == nil || *got.Photo.VisitID != older {
		t.Errorf("the ANSWER carries visitId %v, want %q", got.Photo.VisitID, older)
	}
	// AN EXISTING OCCASION MOVED ONE ENTITY, so the answer is the bare
	// photograph and NOT the whole envelope.
	if got.Document != nil {
		t.Error("the answer carries the whole log for a re-file that minted nothing — " +
			"one entity moved and DEC-32's splice is the right answer")
	}
	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed, want 0", got)
	}
}

// A PLACE IN ANOTHER CITY IS REFUSED, AND THE OTHER HALF SAYS A PLACE IN THE
// SAME CITY IS NOT.
//
// BOTH HALVES IN ONE LEG, which is DEC-109's lesson: from the refusing side, a
// guard that cannot tell two cases apart looks identical to a correct one. The
// client refuses this too, and the server is not entitled to assume the client
// did.
func TestRefilingToAPlaceInAnotherCityIsRefusedAndToOneInTheSameCityIsNot(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO places (traveller_id, id, city_id, name, lat, lng)
		VALUES ($1,'bukchon','seoul','Bukchon',37.58,126.98)`, tid)
	mustExec(t, db, `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
		VALUES ($1,'v-bukchon-0','bukchon','autumn-crossing',0,'2027-09-30T12:00:00Z')`, tid)

	// p-autumn was taken in KYOTO; bukchon is in SEOUL.
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

	// THE OTHER HALF. `wishlist-pin` is in kyoto, where the photograph was
	// taken, and the same request succeeds.
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

// AN OCCASION BELONGING TO ANOTHER PLACE OR ANOTHER TRIP IS REFUSED BY NAME.
//
// `visits_pkey` is (traveller_id, id), so a visit id is unique across the
// WHOLE log — naming another place's would file the photograph somewhere the
// user never mentioned, which is the hazard `refuseVisitsHeldElsewhere`
// refuses one route over. And a photograph filed to another trip's occasion
// lands in the wrong year row on P1 and in that trip's cascade.
func TestRefilingToAnotherPlacesOrAnotherTripsOccasionIsRefused(t *testing.T) {
	store, db := photoStore(t)
	ctx := context.Background()
	older, _ := twoOccasionsOnOneTrip(t, db)

	// v-fushimi-may belongs to fushimi-inari, not to wishlist-pin.
	_, err := store.RefilePhoto(ctx, tid, "p-may",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: ptr("v-fushimi-may")})
	if got := fieldNamed(err); got != "visitId" {
		t.Errorf("another place's occasion named %q, want \"visitId\" (err %v)", got, err)
	}

	// p-autumn is on `autumn-crossing`; both wishlist occasions are on
	// `kyoto-in-may`.
	_, err = store.RefilePhoto(ctx, tid, "p-autumn",
		logbook.RefileWrite{PlaceID: ptr("wishlist-pin"), VisitID: &older})
	if got := fieldNamed(err); got != "visitId" {
		t.Errorf("another trip's occasion named %q, want \"visitId\" (err %v)", got, err)
	}

	if got := halfFiled(t, db); got != 0 {
		t.Errorf("%d photographs are half-filed after two refusals, want 0", got)
	}
}

// A RE-FILE THAT OPENS AN OCCASION REWRITES THE PLACE'S ORDINALS IN `at` DESC,
// AND ANSWERS THE WHOLE ENVELOPE.
//
// TWO ENTITIES MOVED — the photograph AND the place — so the phone cannot
// splice what it was not sent. That is `PUT /v1/cities/{id}`'s own device, and
// `Document` is nil exactly when nothing was minted.
//
// THE ORDINAL REWRITE IS A PERMUTATION WRITTEN OVER ITSELF, which collides
// with `visits_place_ordinal_uq` mid-statement unless every ordinal is parked
// above the incoming count first — the same lesson `offsetVisitOrdinalsSQL`
// records for the visits array. The new occasion is deliberately placed
// BETWEEN the two existing ones, so a rewrite that appended rather than sorted
// is caught.
func TestARefileThatOpensAnOccasionRenumbersThePlaceAndAnswersTheWholeLog(t *testing.T) {
	store, db := photoStore(t)
	twoOccasionsOnOneTrip(t, db)

	// Noon, between the 07:00 and the 19:00.
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

	// NEWEST FIRST, which is what the client reads as `lastVisited`.
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

// AND OPENING AN OCCASION NEEDS `visitAt`, BECAUSE AN OCCASION HAPPENS AT A
// MOMENT.
//
// Only the store knows whether the id is new, which is why this refusal is
// here and not in ValidateRefile — the same half-answerable shape ValidateTrip
// records for the date ordering.
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

// A RE-FILE OF AN UNFILED PHOTOGRAPH RAISES THE COUNT, AND ONE BETWEEN PINS
// LEAVES IT WHERE IT WAS — AND THE OLD PLACE KEEPS ITS OCCASION.
//
// "A refile out of a place leaves the OLD place's visit standing, because the
// visit belongs to the trip and not to the photograph." That is the same
// sentence D3's "N pins in … — kept" row rests on, and the CRUD reflex tidies
// away an occasion nothing is filed to.
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

	// AND MOVING p-may FROM fushimi-inari LEAVES fushimi's OCCASION STANDING.
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

// A PHOTOGRAPH THIS LOG DOES NOT HOLD IS A 404 AND NOT A CREATE.
//
// The client's own asymmetry: `setPhotoCaption` and `refilePhoto` answer false
// for an id the log does not hold, and `deletePhoto` answers true. A re-file
// is about a photograph the user is looking at.
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
