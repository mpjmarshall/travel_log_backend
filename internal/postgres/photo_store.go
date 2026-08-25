// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change' — and the two
// columns this file writes in exactly ONE of its four methods.
//
// `place_id` AND `visit_id` MOVE TOGETHER, ALWAYS, AND ONLY IN RefilePhoto.
// DEC-83 ruled that the pair is coherent by a GO RULE and not by the schema,
// with the reason executed rather than argued: the paired CHECK a reader
// reaches for — `CHECK ((place_id IS NULL) = (visit_id IS NULL))`, the exact
// shape of `photos_coordinates_paired_ck` three columns away — ABORTS D2's
// keep branch, because the two `ON DELETE SET NULL` rules fire as two separate
// single-column UPDATEs and the intermediate row is always incoherent. So the
// rule lives in Go, and the way it is kept here is by having as few writers as
// possible: `upsertPhotoSQL` names neither column in its column list nor in
// its SET clause, and `logbook.PhotoWrite` has no slot for either.
//
// WHAT THAT FORECLOSES IS THE STEP'S WORST DEFECT, MEASURED. `ph-0` carried
// `place_id=bukchon, visit_id=v-bukchon-0`; the whole-state form of M2's
// caption edit wrote both to NULL alongside the note; and ALL THREE standing
// guards stayed green — the dangling check because the reference is GONE
// rather than dangling, R6's place-without-occasion query because there is no
// place left, and the pair-agreement assertion because two NULLs agree. The
// one assertion that sees it is a COUNT THAT MUST NOT FALL:
// `SELECT count(*) FROM photos WHERE place_id IS NOT NULL`, which is 95 on the
// seeded log and is unchanged by every method here except RefilePhoto (which
// RAISES it when the photograph was unfiled) and DeletePhoto (which lowers it
// by one when the photograph was filed).
//
// THERE IS NO CASCADE ANYWHERE IN THIS FILE, and that is a fact about the
// schema rather than a choice: nothing in this database references a
// photograph. `DELETE FROM photos` is one statement and takes exactly one row.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"travellog/internal/logbook"
)

// PhotoStore satisfies logbook.PhotoStore over the same pool.
type PhotoStore struct{ DB *sql.DB }

// upsertPhotoSQL is DEC-89's contract as a statement, AND THE INTERESTING
// THING ABOUT IT IS WHAT IS NOT IN IT.
//
// `place_id` and `visit_id` are in neither the column list nor the SET clause.
// On a CREATE they default to NULL, which is correct — a photograph arrives
// unfiled and K1's filing sheet or M2.2's 'Change' files it. On an UPDATE they
// are untouched, which is the whole of SAF-MAJ-5.
//
// THE SHAPE IS THE ONE THE AUTHOR OF `PUT /v1/trips/{id}` ALREADY REASONED OUT
// FOR THREE COLUMNS AND THEN DID NOT APPLY TO FIVE. That file's own comment:
// "share_photos, share_notes and share_coordinates appear in neither the
// column list nor the SET clause … naming them in EXCLUDED-form would silently
// reset a group this route does not own". This is the same sentence about the
// same kind of group, applied at the start rather than after a lens found it.
const upsertPhotoSQL = `INSERT INTO photos
		(traveller_id, id, trip_id, city_id, taken_at, asset,
		 caption, lat, lng, accuracy_metres, filed_later)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT ON CONSTRAINT photos_pkey DO UPDATE SET
		trip_id         = CASE WHEN $12::boolean THEN EXCLUDED.trip_id         ELSE photos.trip_id         END,
		city_id         = CASE WHEN $13::boolean THEN EXCLUDED.city_id         ELSE photos.city_id         END,
		taken_at        = CASE WHEN $14::boolean THEN EXCLUDED.taken_at        ELSE photos.taken_at        END,
		asset           = CASE WHEN $15::boolean THEN EXCLUDED.asset           ELSE photos.asset           END,
		caption         = CASE WHEN $16::boolean THEN EXCLUDED.caption         ELSE photos.caption         END,
		lat             = CASE WHEN $17::boolean THEN EXCLUDED.lat             ELSE photos.lat             END,
		lng             = CASE WHEN $17::boolean THEN EXCLUDED.lng             ELSE photos.lng             END,
		accuracy_metres = CASE WHEN $18::boolean THEN EXCLUDED.accuracy_metres ELSE photos.accuracy_metres END,
		filed_later     = CASE WHEN $19::boolean THEN EXCLUDED.filed_later     ELSE photos.filed_later     END`

// `lat` AND `lng` SHARE ONE FLAG, and that is the same call `PutPlace` makes
// for a place's centre: `photos_coordinates_paired_ck` means writing one
// without the other is not a state a row may hold, so a second flag would be a
// second way to get it wrong for no expressible gain.

const readPhotoForWriteSQL = `SELECT trip_id, city_id, taken_at, asset
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

const readOnePhotoSQL = `SELECT id, trip_id, city_id, taken_at, asset, place_id, visit_id,
		caption, lat, lng, accuracy_metres, filed_later
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

// PutPhoto is M2's 'Write a note' and DEC-33's create, inside WithTravellerTx.
//
// THE ANSWER IS RE-READ FROM THE ROW AND NOT ASSEMBLED FROM THE REQUEST, for
// PutTrip's reason — and here that is what puts `placeId` and `visitId` in the
// answer to a caption-only write at all. A response built from a
// `logbook.PhotoWrite` could not carry them: the type has no slot.
func (s PhotoStore) PutPhoto(ctx context.Context, travellerID string, w logbook.PhotoWrite) (logbook.Photo, int64, error) {
	var photo logbook.Photo
	if w.ID == nil {
		return logbook.Photo{}, 0, logbook.InvalidFieldError{Field: "id", Why: "a write names its photograph"}
	}
	id := *w.ID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		before, err := requireWritablePhoto(ctx, tx, travellerID, id, w)
		if err != nil {
			return err
		}
		tripID, cityID, takenAt, asset := before.tripID, before.cityID, before.takenAt, before.asset
		if w.TripID != nil {
			tripID = *w.TripID
		}
		if w.CityID != nil {
			cityID = *w.CityID
		}
		if w.TakenAt != nil {
			takenAt = w.TakenAt.Time()
		}
		if w.Asset != nil {
			asset = *w.Asset
		}
		if err := requireTripForPhoto(ctx, tx, travellerID, tripID); err != nil {
			return err
		}
		if err := requireCityForPhoto(ctx, tx, travellerID, cityID); err != nil {
			return err
		}
		// THE ASSET IS CHECKED WHENEVER IT IS BEING WRITTEN AND NOT ONLY ON A
		// CREATE, because `photos_asset_fk` guarantees the ROW exists and says
		// nothing about `uploaded_at` — an FK cannot see a column it does not
		// reference. A photograph pointing at an object that was begun and
		// never uploaded is bytes the user does not have, drawn as a broken
		// plate for ever.
		if w.Asset != nil || before.isCreate {
			if err := requirePhotoAsset(ctx, tx, travellerID, asset); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, upsertPhotoSQL, travellerID, id,
			tripID, cityID, takenAt, asset,
			logbook.StoredCaption(w.Caption),
			latOrNil(logbook.Value(w.Coordinates)), lngOrNil(logbook.Value(w.Coordinates)),
			logbook.Value(w.AccuracyMetres), instantValue(logbook.Value(w.FiledLater)),
			w.TripID != nil, w.CityID != nil, w.TakenAt != nil, w.Asset != nil,
			logbook.Sent(w.Caption), logbook.Sent(w.Coordinates),
			logbook.Sent(w.AccuracyMetres), logbook.Sent(w.FiledLater),
		); err != nil {
			return fmt.Errorf("postgres: upserting the photograph %s: %w", id, err)
		}

		read, err := readOnePhoto(ctx, tx, travellerID, id)
		if err != nil {
			return err
		}
		photo = read
		return nil
	})
	if err != nil {
		return logbook.Photo{}, 0, travellerError(err, travellerID)
	}
	return photo, version, nil
}

// photoBeforeWrite is the four NOT NULL columns the upsert has to be able to
// propose when the body did not carry them, plus which case it is.
type photoBeforeWrite struct {
	tripID, cityID, asset string
	takenAt               time.Time
	isCreate              bool
}

// requireWritablePhoto refuses a CREATE missing a NOT NULL field and names it.
//
// AN UNKNOWN ID IS A CREATE AND NOT A 404, AND THAT IS DEC-33 RATHER THAN A
// CONCESSION. `PUT /v1/places/{id}` already answers a body of `{plan}` on an
// unknown id with a 422 naming `cityId`, for the same reason: the key is the
// client's, so the request is idempotent by construction and the only thing
// missing is what a NOT NULL column needs.
func requireWritablePhoto(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.PhotoWrite) (photoBeforeWrite, error) {
	var before photoBeforeWrite
	err := tx.QueryRowContext(ctx, readPhotoForWriteSQL, travellerID, id).
		Scan(&before.tripID, &before.cityID, &before.takenAt, &before.asset)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before.isCreate = true
		for _, missing := range []struct {
			absent bool
			field  string
			why    string
		}{
			{w.TripID == nil, "tripId", "a photograph is taken on a trip, and one that is " +
				"not in this log yet has no trip to leave alone"},
			{w.CityID == nil, "cityId", "a photograph is taken in a city, and one that is " +
				"not in this log yet has no city to leave alone"},
			{w.TakenAt == nil, "takenAt", "a photograph is taken at a moment — it is what " +
				"M1 and L1 group by — and one that is not in this log yet has none to " +
				"leave alone"},
			{w.Asset == nil, "asset", "a photograph IS its asset, and one that is not in " +
				"this log yet has none to leave alone"},
		} {
			if missing.absent {
				return before, logbook.InvalidFieldError{Field: missing.field, Why: missing.why}
			}
		}
	case err != nil:
		return before, fmt.Errorf("postgres: reading the photograph %s before writing it: %w", id, err)
	}
	return before, nil
}

func requireTripForPhoto(ctx context.Context, tx *sql.Tx, travellerID, tripID string) error {
	if err := requireRow(ctx, tx, tripExistsSQL, travellerID, tripID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "tripId",
				Why: fmt.Sprintf("%q is not a trip in this log, and a photograph is taken on one", tripID)}
		}
		return fmt.Errorf("postgres: looking up the trip %s: %w", tripID, err)
	}
	return nil
}

func requireCityForPhoto(ctx context.Context, tx *sql.Tx, travellerID, cityID string) error {
	if err := requireRow(ctx, tx, cityExistsSQL, travellerID, cityID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "cityId",
				Why: fmt.Sprintf("%q is not a city in this log, and a photograph is taken in one", cityID)}
		}
		return fmt.Errorf("postgres: looking up the city %s: %w", cityID, err)
	}
	return nil
}

// requirePhotoAsset is `requireCover` with the OTHER FIELD NAME, and it is a
// second function for exactly that reason: a cover's field is `coverAsset` and
// a photograph's is `asset`, so reusing the helper would tell a client to fix
// a key its request never carried.
//
// IT ASKS FOR A COMMITTED OBJECT AND NOT MERELY AN EXISTING ROW, which is what
// `mediaObjectCommittedSQL` adds over the foreign key. `photos_asset_fk`
// cannot see `uploaded_at`, so without this a photograph can reference bytes
// that were begun and never uploaded — the exact state SAF-MIN-12's
// bucket-versus-database seam leaves behind.
func requirePhotoAsset(ctx context.Context, tx *sql.Tx, travellerID, asset string) error {
	if err := requireRow(ctx, tx, mediaObjectCommittedSQL, travellerID, asset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "asset",
				Why: fmt.Sprintf("%q is not an uploaded object in this log — begin the "+
					"upload, PUT the bytes, and commit it before a photograph names it", asset)}
		}
		return fmt.Errorf("postgres: looking up the object %s: %w", asset, err)
	}
	return nil
}

func readOnePhoto(ctx context.Context, tx *sql.Tx, travellerID, photoID string) (logbook.Photo, error) {
	var p logbook.Photo
	// taken_at is NOT NULL and is scanned as such; filed_later is nullable and
	// keeps its sql.NullTime. DEC-102, and the same split readPhotos makes.
	var takenAt time.Time
	var filedLater sql.NullTime
	var placeID, visitID, caption sql.NullString
	var lat, lng sql.NullFloat64
	var accuracy sql.NullInt64
	switch err := tx.QueryRowContext(ctx, readOnePhotoSQL, travellerID, photoID).
		Scan(&p.ID, &p.TripID, &p.CityID, &takenAt, &p.Asset, &placeID, &visitID,
			&caption, &lat, &lng, &accuracy, &filedLater); {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.Photo{}, fmt.Errorf("%w: %s", logbook.ErrNoPhoto, photoID)
	case err != nil:
		return logbook.Photo{}, fmt.Errorf("postgres: reading the photograph %s back: %w", photoID, err)
	}
	p.TakenAt = logbook.At(takenAt)
	p.PlaceID, p.VisitID, p.Caption = textOrNil(placeID), textOrNil(visitID), textOrNil(caption)
	p.AccuracyMetres, p.FiledLater = intOrNil(accuracy), instantOrNil(filedLater)
	if lat.Valid && lng.Valid {
		p.Coordinates = &logbook.LatLng{Lat: lat.Float64, Lng: lng.Float64}
	}
	return p, nil
}

func latOrNil(at *logbook.LatLng) *float64 {
	if at == nil {
		return nil
	}
	return &at.Lat
}

func lngOrNil(at *logbook.LatLng) *float64 {
	if at == nil {
		return nil
	}
	return &at.Lng
}

func instantValue(i *logbook.Instant) *time.Time {
	if i == nil {
		return nil
	}
	at := i.Time()
	return &at
}

// ------------------------------------------------------------------ D1

const deletePhotoSQL = `DELETE FROM photos WHERE traveller_id = $1::uuid AND id = $2`

// errNoSuchPhoto is how a miss ROLLS THE VERSION BUMP BACK — the same device
// `errNoSuchPlace` is for D2 and `errNothingDeleted` is for D3, and a separate
// sentinel because the three are caught in different functions and a shared
// one would be caught by the wrong caller.
var errNoSuchPhoto = errors.New("postgres: that photograph was not in this log")

// DeletePhoto is D1, and it is the only destructive route in this plan that
// cascades NOWHERE.
//
// Nothing in this schema references `photos`. There is no sheet copy to
// implement, no second statement whose order matters, and no `_repointed` to
// write: the row goes and the log is coherent. The whole-log answer D2 and D3
// give exists because "the cache cannot splice a cascade", and one row leaving
// is precisely what a cache CAN splice — so this answers 204 and a new ETag.
//
// AN UNKNOWN ID IS A SUCCESS AND MOVES NO VERSION, exactly as it is on
// DeleteTrip and RemovePlace. The client's `deletePhoto` answers true for an
// id the log does not hold, and a bump on a retried delete throws away the
// phone's whole cached document — which DEC-103 makes concrete, because a
// delete against a build that predates the route is retried.
func (s PhotoStore) DeletePhoto(ctx context.Context, travellerID, photoID string) (int64, error) {
	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, deletePhotoSQL, travellerID, photoID)
		if err != nil {
			return fmt.Errorf("postgres: deleting the photograph %s: %w", photoID, err)
		}
		gone, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("postgres: counting the deleted photograph %s: %w", photoID, err)
		}
		if gone == 0 {
			return errNoSuchPhoto
		}
		return nil
	})
	switch {
	case errors.Is(err, errNoSuchPhoto):
		// Nothing was written, so the version the caller should stamp its
		// cache with is the one already there.
		snapshot, err := s.read(ctx, travellerID)
		if err != nil {
			return 0, err
		}
		return snapshot.Version, nil
	case err != nil:
		return 0, travellerError(err, travellerID)
	}
	return version, nil
}

// ------------------------------------------------------------------ N1's 'Later'

// snoozePhotosSQL is the whole of the bulk write: ONE statement for the whole
// group, and `= ANY($2)` takes a plain `[]string` and imports nothing (see
// place_store.go, where R4's contrary claim is corrected).
//
// IT RETURNS THE IDS IT TOUCHED, which is what makes "an unknown id is
// SKIPPED" observable without a second query — and what tells the caller which
// of its group survived to be snoozed.
const snoozePhotosSQL = `UPDATE photos SET filed_later = $3
	WHERE traveller_id = $1::uuid AND id = ANY($2)
	RETURNING id`

// errNothingSnoozed rolls the version bump back when the group matched no row.
var errNothingSnoozed = errors.New("postgres: none of those photographs is in this log")

// SnoozePhotos is N1's 'Later': ALL-OR-NOTHING IN ONE TRANSACTION WITH ONE
// VERSION BUMP.
//
// THIS IS THE FIRST ROUTE IN THE API THAT TAKES A COLLECTION, so it is the
// first place a partial failure would have to answer for a group — and the
// answer is that there is no partial. One statement inside one transaction
// under one advisory lock; either every match moved or none did.
//
// ONE BUMP AND NOT ONE PER PHOTOGRAPH, and the difference is what the phone
// does with it. `logbook_version` is the ETag's second half, so N bumps for
// one user action would hand the client N-1 versions it can never have held
// and invalidate its cached document N times for one write.
//
// AN UNKNOWN ID IS SKIPPED RATHER THAN FATAL, matching the client exactly:
// "the row was derived from the log a frame ago and a photograph deleted since
// is one that no longer needs filing". A group that matches NOTHING writes
// nothing and moves no version, which is `snoozeUnfiledPhotos` returning false
// without committing — "a commit that changes nothing is a file write and a
// state assignment for no reason".
//
// THE ANSWER IS THE ROWS THAT MOVED, NEVER nil. A nil slice marshals to
// `null`, and even inside httpapi's own body type that is a key the client
// reads as a List — so the slice is MADE rather than appended to from nil, and
// a leg asserts `"photos": []` reaches the wire on an empty match.
func (s PhotoStore) SnoozePhotos(ctx context.Context, travellerID string, w logbook.SnoozeWrite) ([]logbook.Photo, int64, error) {
	if w.PhotoIDs == nil || w.Until == nil {
		return nil, 0, logbook.InvalidFieldError{Field: "photoIds",
			Why: "a snooze names a group and a date"}
	}
	snoozed := []logbook.Photo{}

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, snoozePhotosSQL, travellerID, *w.PhotoIDs, w.Until.Time())
		if err != nil {
			return fmt.Errorf("postgres: snoozing %d photographs: %w", len(*w.PhotoIDs), err)
		}
		var moved []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("postgres: scanning a snoozed photograph: %w", err)
			}
			moved = append(moved, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: snoozing %d photographs: %w", len(*w.PhotoIDs), err)
		}
		rows.Close()

		if len(moved) == 0 {
			return errNothingSnoozed
		}
		// IN ID ORDER AND NOT IN THE ORDER THE UPDATE RETURNED THEM. An
		// UPDATE's RETURNING order is the order rows happened to be visited,
		// which is not stable — and two reads of one write that differ are two
		// bodies under one ETag.
		slices.Sort(moved)
		for _, id := range moved {
			photo, err := readOnePhoto(ctx, tx, travellerID, id)
			if err != nil {
				return err
			}
			snoozed = append(snoozed, photo)
		}
		return nil
	})
	switch {
	case errors.Is(err, errNothingSnoozed):
		snapshot, err := s.read(ctx, travellerID)
		if err != nil {
			return nil, 0, err
		}
		return []logbook.Photo{}, snapshot.Version, nil
	case err != nil:
		return nil, 0, travellerError(err, travellerID)
	}
	return snoozed, version, nil
}

// ------------------------------------------------------------------ M2.2's 'Change'

const readPhotoForRefileSQL = `SELECT trip_id, city_id, place_id, visit_id
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

const readPlaceCitySQL = `SELECT city_id FROM places WHERE traveller_id = $1::uuid AND id = $2`

const readVisitForRefileSQL = `SELECT place_id, trip_id FROM visits
	WHERE traveller_id = $1::uuid AND id = $2`

// insertRefiledVisitSQL opens the occasion the client minted, at an ordinal
// STRICTLY ABOVE every ordinal the place already holds — so the INSERT itself
// can never collide with `visits_place_ordinal_uq`. The renumbering below puts
// it where it belongs.
const insertRefiledVisitSQL = `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
	VALUES ($1::uuid, $2, $3, $4,
		(SELECT coalesce(max(ordinal), -1) + 1 FROM visits
			WHERE traveller_id = $1::uuid AND place_id = $3),
		$5)`

// renumberVisitsByTimeSQL is the ordinal rewrite the plan asks for: `at` DESC,
// which is the order the client reads (`visits.first.at` is `lastVisited`).
//
// IT IS SAFE ONLY BECAUSE THE PARK RAN FIRST, and that is the same lesson
// `offsetVisitOrdinalsSQL` records. `visits_place_ordinal_uq` is checked per
// ROW during a statement, so writing a PERMUTATION of 0..n-1 over itself
// collides mid-statement even when the final state is unique. Park everything
// above n first and every target below is free.
//
// `ORDER BY at DESC, id` AND NOT `at DESC` ALONE, for DEC-26's reason: the
// fixture visits Nishiki FOUR TIMES ON ONE DAY, and two rows with the same
// instant would otherwise renumber non-deterministically — which silently
// rebinds a photograph to a different occasion on the next read.
const renumberVisitsByTimeSQL = `UPDATE visits v SET ordinal = ranked.position
	FROM (
		SELECT id, (row_number() OVER (ORDER BY at DESC, id)) - 1 AS position
		FROM visits WHERE traveller_id = $1::uuid AND place_id = $2
	) ranked
	WHERE v.traveller_id = $1::uuid AND v.id = ranked.id`

// refilePhotoSQL is the one statement that writes the pair, AND IT WRITES BOTH
// COLUMNS IN ONE UPDATE.
//
// Two single-column UPDATEs would leave an intermediate row naming a place
// with no occasion — the half-record state the client's model has never
// expressed, and the state DEC-83's declined CHECK would have aborted on. One
// statement means the incoherent row never exists, not even inside the
// transaction.
const refilePhotoSQL = `UPDATE photos SET place_id = $3, visit_id = $4
	WHERE traveller_id = $1::uuid AND id = $2`

// RefilePhoto is M2.2's 'Change', and it VALIDATES the occasion the client
// chose rather than choosing one.
//
// THE IMPLEMENTATION IT REFUSES TO BE is an unordered
// `SELECT id FROM visits WHERE place_id = $1 AND trip_id = $2 LIMIT 1`, which
// is plausible, short, and files the photograph to whichever row the planner
// happened to return. Every field in the answer would be individually valid.
// The fixture makes it concrete: `nishiki` holds FOUR occasions on
// `japan-2026`, all four on 2026-09-18, so "the newest" is not even
// well-defined by date — which is why `visits.at` is timestamptz (DEC-68) and
// why the leg that guards this runs at `-count=10`.
//
// FOUR REFUSALS, EACH FOR ITS OWN REASON:
//
//   - THE PHOTOGRAPH MUST EXIST. 404, not a create: a re-file is about a
//     photograph the user is looking at.
//   - THE PLACE MUST BE IN THE PHOTOGRAPH'S CITY. The client refuses it too
//     (`if (place.cityId != photo.cityId) return false`) and the server is not
//     entitled to assume the client did. Measured across the fixture: 0 of 284
//     photographs name a place in another city.
//   - AN EXISTING OCCASION MUST BELONG TO THAT PLACE. `visits_pkey` is
//     (traveller_id, id), so a visit id is unique across the whole log and
//     naming another place's would file the photograph somewhere the user
//     never mentioned — the same hazard `refuseVisitsHeldElsewhere` refuses one
//     route over.
//   - AN EXISTING OCCASION MUST BE ON THE PHOTOGRAPH'S TRIP. Measured: 0 of
//     284 fixture photographs name a visit on another trip, and the client's
//     own `place.visitsOn(photo.tripId)` cannot produce one. A photograph filed
//     to an occasion on a different trip puts it in the wrong year row on P1
//     and in D3's cascade for a trip it was not taken on.
//
// AND `visitAt` IS USED ONLY WHEN THE OCCASION IS NEW. An occasion is shared —
// thirty photographs at `fushimi-inari` hang off twenty-eight of them — so
// applying `visitAt` to one that already exists would re-time it for every
// other photograph filed there, reorder the place's visits and move
// `lastVisited` on P1. That is the one-thing-too-many defect, from a control
// whose whole promise is that it moves ONE photograph.
func (s PhotoStore) RefilePhoto(ctx context.Context, travellerID, photoID string, w logbook.RefileWrite) (logbook.PhotoRefiled, error) {
	var out logbook.PhotoRefiled
	if w.PlaceID == nil || w.VisitID == nil {
		// Unreachable through the API — logbook.Service refuses both before
		// this is called — and here anyway, because a store that read a nil
		// pointer would write NULL into the pair.
		return out, logbook.InvalidFieldError{Field: "visitId",
			Why: "a re-file names both the pin and the occasion"}
	}
	placeID, visitID := *w.PlaceID, *w.VisitID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		photo, err := readPhotoForRefile(ctx, tx, travellerID, photoID)
		if err != nil {
			return err
		}
		placeCity, err := readPlaceCity(ctx, tx, travellerID, placeID)
		if err != nil {
			return err
		}
		if placeCity != photo.cityID {
			return logbook.InvalidFieldError{Field: "placeId",
				Why: fmt.Sprintf("%q is in %s and the photograph was taken in %s — M2.2 "+
					"lists the pins in the photograph's OWN city, and moving one between "+
					"cities is a claim about where somebody was",
					placeID, placeCity, photo.cityID)}
		}

		minted, err := requireOrMintOccasion(ctx, tx, travellerID, refiling{
			placeID: placeID, visitID: visitID, tripID: photo.tripID, at: w.VisitAt,
		})
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, refilePhotoSQL, travellerID, photoID, placeID, visitID); err != nil {
			return fmt.Errorf("postgres: filing %s at %s: %w", photoID, placeID, err)
		}

		if out.Photo, err = readOnePhoto(ctx, tx, travellerID, photoID); err != nil {
			return err
		}
		// TWO ENTITIES MOVED, SO THE ANSWER IS THE WHOLE ENVELOPE — the device
		// `PUT /v1/cities/{id}` uses when `attachTo` is honoured, for its
		// reason. A minted occasion changes the PLACE as well as the
		// photograph, and it renumbers every one of that place's ordinals, so
		// the phone cannot splice what it was not sent.
		if minted {
			doc, err := readDocument(ctx, tx, travellerID)
			if err != nil {
				return err
			}
			out.Document = &doc
		}
		return nil
	})
	if err != nil {
		return logbook.PhotoRefiled{}, travellerError(err, travellerID)
	}
	out.Version = version
	return out, nil
}

// refiling is the four values the occasion check needs, named so the call site
// reads as a sentence rather than as five positional strings.
type refiling struct {
	placeID, visitID, tripID string
	at                       *logbook.Instant
}

// requireOrMintOccasion answers whether it MINTED one, which is what decides
// the response shape.
func requireOrMintOccasion(ctx context.Context, tx *sql.Tx, travellerID string, r refiling) (bool, error) {
	var heldPlace, heldTrip string
	switch err := tx.QueryRowContext(ctx, readVisitForRefileSQL, travellerID, r.visitID).
		Scan(&heldPlace, &heldTrip); {
	case errors.Is(err, sql.ErrNoRows):
		// A FRESH ID IS A NEW OCCASION AND `visitAt` IS WHAT OPENS IT. The
		// client mints the id (`_freshId`) and knows the moment
		// (`photo.takenAt`), so both are its to send; the server derives only
		// the trip, from the photograph, because a visit on another trip is
		// not a state the client can produce.
		if r.at == nil {
			return false, logbook.InvalidFieldError{Field: "visitAt",
				Why: fmt.Sprintf("%q is not an occasion in this log, so this re-file is "+
					"opening a new one — and an occasion happens at a moment. The client "+
					"already holds it: `refilePhoto` mints the visit at the photograph's "+
					"own `takenAt`", r.visitID)}
		}
		if _, err := tx.ExecContext(ctx, insertRefiledVisitSQL,
			travellerID, r.visitID, r.placeID, r.tripID, r.at.Time()); err != nil {
			return false, fmt.Errorf("postgres: opening the occasion %s at %s: %w",
				r.visitID, r.placeID, err)
		}
		if err := renumberVisits(ctx, tx, travellerID, r.placeID); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("postgres: reading the occasion %s: %w", r.visitID, err)
	}

	if heldPlace != r.placeID {
		return false, logbook.InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("the occasion %s belongs to %s and this re-file names %s. "+
				"A visit id is unique across the whole log, so filing to another place's "+
				"occasion would put the photograph somewhere nobody mentioned",
				r.visitID, heldPlace, r.placeID)}
	}
	if heldTrip != r.tripID {
		return false, logbook.InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("the occasion %s is on %s and the photograph was taken on %s. "+
				"A photograph filed to another trip's occasion lands in the wrong year "+
				"row on P1 and in that trip's cascade", r.visitID, heldTrip, r.tripID)}
	}
	return false, nil
}

// renumberVisits parks every ordinal above the incoming count and then writes
// 0..n-1 in `at` DESC. Two statements, and the first is not optional — see
// renumberVisitsByTimeSQL.
func renumberVisits(ctx context.Context, tx *sql.Tx, travellerID, placeID string) error {
	var occasions int
	if err := tx.QueryRowContext(ctx, occasionsAtPlaceSQL, travellerID, placeID).Scan(&occasions); err != nil {
		return fmt.Errorf("postgres: counting the occasions at %s: %w", placeID, err)
	}
	if _, err := tx.ExecContext(ctx, offsetVisitOrdinalsSQL, travellerID, placeID, occasions); err != nil {
		return fmt.Errorf("postgres: parking the visit ordinals of %s: %w", placeID, err)
	}
	if _, err := tx.ExecContext(ctx, renumberVisitsByTimeSQL, travellerID, placeID); err != nil {
		return fmt.Errorf("postgres: renumbering the occasions of %s: %w", placeID, err)
	}
	return nil
}

// photoForRefile is what the re-file needs to know about the photograph before
// it moves: which city it was taken in, and which trip.
type photoForRefile struct {
	tripID, cityID string
	placeID        sql.NullString
	visitID        sql.NullString
}

func readPhotoForRefile(ctx context.Context, tx *sql.Tx, travellerID, photoID string) (photoForRefile, error) {
	var p photoForRefile
	switch err := tx.QueryRowContext(ctx, readPhotoForRefileSQL, travellerID, photoID).
		Scan(&p.tripID, &p.cityID, &p.placeID, &p.visitID); {
	case errors.Is(err, sql.ErrNoRows):
		return p, fmt.Errorf("%w: %s", logbook.ErrNoPhoto, photoID)
	case err != nil:
		return p, fmt.Errorf("postgres: reading the photograph %s to re-file it: %w", photoID, err)
	}
	return p, nil
}

func readPlaceCity(ctx context.Context, tx *sql.Tx, travellerID, placeID string) (string, error) {
	var cityID string
	switch err := tx.QueryRowContext(ctx, readPlaceCitySQL, travellerID, placeID).Scan(&cityID); {
	case errors.Is(err, sql.ErrNoRows):
		return "", logbook.InvalidFieldError{Field: "placeId",
			Why: fmt.Sprintf("%q is not a pin in this log", placeID)}
	case err != nil:
		return "", fmt.Errorf("postgres: reading the city of %s: %w", placeID, err)
	}
	return cityID, nil
}

// read is here so a PhotoStore can answer the version a miss should see, and
// it is LogbookStore's own read rather than a second implementation of it.
func (s PhotoStore) read(ctx context.Context, travellerID string) (logbook.Snapshot, error) {
	return LogbookStore{DB: s.DB}.Read(ctx, travellerID, func(int64) bool { return false })
}
