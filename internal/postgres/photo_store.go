// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change' — and's two
// columns this file writes in exactly one of its four methods.
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

// upsertPhotoSQL is the contract as a statement, and the interesting thing
// about it is what is not in it.
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

const readPhotoForWriteSQL = `SELECT trip_id, city_id, taken_at, asset
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

const readOnePhotoSQL = `SELECT id, trip_id, city_id, taken_at, asset, place_id, visit_id,
		caption, lat, lng, accuracy_metres, filed_later
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

// PutPhoto is M2's 'Write a note' and the create, inside WithTravellerTx.
func (s PhotoStore) PutPhoto(ctx context.Context, travellerID string, w logbook.PhotoWrite) (logbook.Photo, int64, error) {
	var photo logbook.Photo
	if err := logbook.CheckWriteID(w.ID); err != nil {
		return logbook.Photo{}, 0, err
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

// photoBeforeWrite is the four not NULL columns the upsert has to be able to
// propose when the body did not carry them, plus which case it is.
type photoBeforeWrite struct {
	tripID, cityID, asset string
	takenAt               time.Time
	isCreate              bool
}

// requireWritablePhoto refuses a create missing a not NULL field and names
// it.
func requireWritablePhoto(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.PhotoWrite) (photoBeforeWrite, error) {
	var before photoBeforeWrite
	err := tx.QueryRowContext(ctx, readPhotoForWriteSQL, travellerID, id).
		Scan(&before.tripID, &before.cityID, &before.takenAt, &before.asset)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before.isCreate = true
	case err != nil:
		return before, fmt.Errorf("postgres: reading the photograph %s before writing it: %w", id, err)
	}
	return before, logbook.CheckPhotoWritable(before.isCreate, w)
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

// requirePhotoAsset is `requireCover` with the other field name, and it is a
// second function for exactly that reason.
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

const deletePhotoSQL = `DELETE FROM photos WHERE traveller_id = $1::uuid AND id = $2`

// errNoSuchPhoto is how a miss rolls the version bump back — the same device
// `errNoSuchPlace` is for D2 and `errNothingDeleted` is for D3.
var errNoSuchPhoto = errors.New("postgres: that photograph was not in this log")

// DeletePhoto is D1, and it is the only destructive route in this plan that
// cascades nowhere.
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

// snoozePhotosSQL is the whole of the bulk write.
const snoozePhotosSQL = `UPDATE photos SET filed_later = $3
	WHERE traveller_id = $1::uuid AND id = ANY($2)
	RETURNING id`

// errNothingSnoozed rolls the version bump back when the group matched no
// row.
var errNothingSnoozed = errors.New("postgres: none of those photographs is in this log")

// SnoozePhotos is N1's 'Later': all-or-nothing in one transaction with one
// version bump.
func (s PhotoStore) SnoozePhotos(ctx context.Context, travellerID string, w logbook.SnoozeWrite) ([]logbook.Photo, int64, error) {
	if err := logbook.CheckSnoozeWritable(w); err != nil {
		return nil, 0, err
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

const readPhotoForRefileSQL = `SELECT trip_id, city_id, place_id, visit_id
	FROM photos WHERE traveller_id = $1::uuid AND id = $2`

const readPlaceCitySQL = `SELECT city_id FROM places WHERE traveller_id = $1::uuid AND id = $2`

const readVisitForRefileSQL = `SELECT place_id, trip_id FROM visits
	WHERE traveller_id = $1::uuid AND id = $2`

// insertRefiledVisitSQL opens the occasion the client minted, at an ordinal
// strictly above every ordinal the place already holds.
const insertRefiledVisitSQL = `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at)
	VALUES ($1::uuid, $2, $3, $4,
		(SELECT coalesce(max(ordinal), -1) + 1 FROM visits
			WHERE traveller_id = $1::uuid AND place_id = $3),
		$5)`

// renumberVisitsByTimeSQL is the ordinal rewrite the plan asks for.
const renumberVisitsByTimeSQL = `UPDATE visits v SET ordinal = ranked.position
	FROM (
		SELECT id, (row_number() OVER (ORDER BY at DESC, id)) - 1 AS position
		FROM visits WHERE traveller_id = $1::uuid AND place_id = $2
	) ranked
	WHERE v.traveller_id = $1::uuid AND v.id = ranked.id`

// refilePhotoSQL is the one statement that writes the pair, and it writes
// both columns in one update.
const refilePhotoSQL = `UPDATE photos SET place_id = $3, visit_id = $4
	WHERE traveller_id = $1::uuid AND id = $2`

// RefilePhoto is M2.2's 'Change', and it validates the occasion the client
// chose rather than choosing one.
func (s PhotoStore) RefilePhoto(ctx context.Context, travellerID, photoID string, w logbook.RefileWrite) (logbook.PhotoRefiled, error) {
	var out logbook.PhotoRefiled
	if err := logbook.CheckRefileWritable(w); err != nil {
		return out, err
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
		if err := logbook.CheckRefilePlace(placeID, placeCity, photo.cityID); err != nil {
			return err
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

// refiling is the four values the occasion check needs, named so the call
// site reads as a sentence rather than as five positional strings.
type refiling struct {
	placeID, visitID, tripID string
	at                       *logbook.Instant
}

// requireOrMintOccasion answers whether it minted one, which is what decides
// the response shape.
func requireOrMintOccasion(ctx context.Context, tx *sql.Tx, travellerID string, r refiling) (bool, error) {
	var heldPlace, heldTrip string
	switch err := tx.QueryRowContext(ctx, readVisitForRefileSQL, travellerID, r.visitID).
		Scan(&heldPlace, &heldTrip); {
	case errors.Is(err, sql.ErrNoRows):
		if err := logbook.CheckNewOccasionHasAMoment(r.visitID, r.at); err != nil {
			return false, err
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

	if err := logbook.CheckOccasionBelongsHere(r.visitID, heldPlace, r.placeID, heldTrip, r.tripID); err != nil {
		return false, err
	}
	return false, nil
}

// renumberVisits parks every ordinal above the incoming count and then writes
// 0..n-1 in `at` desc.
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

// photoForRefile is what the re-file needs to know about the photograph
// before it moves: which city it was taken in, and which trip.
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
