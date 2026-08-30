// The public read's half of the storage contract.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"travellog/internal/logbook"
)

// ShareReadStore is logbook.PublicStore over *sql.DB.
type ShareReadStore struct{ DB *sql.DB }

// shareLinkByHashSQL has no `revoked_at is NULL` and no traveller, and both
// absences are decisions.
const shareLinkByHashSQL = `SELECT traveller_id::text, trip_id, revoked_at IS NOT NULL
	FROM share_links WHERE token_hash = $1`

// publicTripSQL reads the trip and's three switches together, because the
// switches decide what the rest of the read may publish.
const publicTripSQL = `SELECT id, name, started_on, ended_on, summary, cover_asset,
		share_photos, share_notes, share_coordinates
	FROM trips WHERE traveller_id = $1::uuid AND id = $2`

// publicCitiesSQL is ordered by `trip_cities.ordinal` and not by id.
const publicCitiesSQL = `SELECT c.id, c.name, c.country_code, c.country_name,
		c.centre_lat, c.centre_lng
	FROM trip_cities tc
	JOIN cities c ON (c.traveller_id, c.id) = (tc.traveller_id, tc.city_id)
	WHERE tc.traveller_id = $1::uuid AND tc.trip_id = $2
	ORDER BY tc.ordinal`

// publicPlacesSQL is RULE 1: the distinct places having a visit on the shared
// trip.
const publicPlacesSQL = `SELECT p.id, p.city_id, p.name, p.lat, p.lng
	FROM places p
	WHERE p.traveller_id = $1::uuid
	  AND EXISTS (SELECT 1 FROM visits v
	              WHERE v.traveller_id = p.traveller_id
	                AND v.place_id = p.id
	                AND v.trip_id = $2)
	ORDER BY p.id`

// publicVisitsSQL is RULE 2, and it is the one is about: only the published
// place's visits whose `trip_id` is the shared trip.
const publicVisitsSQL = `SELECT place_id, at, note
	FROM visits
	WHERE traveller_id = $1::uuid AND trip_id = $2
	ORDER BY place_id, ordinal, id`

// publicPhotosSQL is RULE 3.
const publicPhotosSQL = `SELECT id, city_id, place_id, taken_at, asset, caption,
		lat, lng, accuracy_metres
	FROM photos
	WHERE traveller_id = $1::uuid AND trip_id = $2
	ORDER BY id`

// publicWalksSQL is RULE 3 for walks, plus the one thing that is not a trip
// id: `not dismissed`.
const publicWalksSQL = `SELECT id, city_id, recorded_on, distance_km
	FROM walks
	WHERE traveller_id = $1::uuid AND trip_id = $2 AND NOT dismissed
	ORDER BY id`

// publicWalkPointsSQL is readWalkPointsSQL scoped to one trip, and it carries
// the same `not dismissed` as the walks query above.
const publicWalkPointsSQL = `SELECT w.id,
		(pt.value->>'lat')::double precision,
		(pt.value->>'lng')::double precision
	FROM walks w
	CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	WHERE w.traveller_id = $1::uuid AND w.trip_id = $2 AND NOT w.dismissed
	ORDER BY w.id, pt.ord`

// ShareLink resolves a digest, revoked or not.
func (s ShareReadStore) ShareLink(ctx context.Context, tokenHash []byte) (logbook.ShareLink, error) {
	var link logbook.ShareLink
	err := s.DB.QueryRowContext(ctx, shareLinkByHashSQL, tokenHash).
		Scan(&link.TravellerID, &link.TripID, &link.Revoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.ShareLink{}, logbook.ErrNoShare
	case err != nil:
		return logbook.ShareLink{}, fmt.Errorf("postgres: resolving a share link: %w", err)
	}
	return link, nil
}

// PublicLog reads the six things the envelope may carry, inside one
// repeatable-read snapshot.
func (s ShareReadStore) PublicLog(ctx context.Context, travellerID, tripID string) (logbook.PublicSource, error) {
	var src logbook.PublicSource

	err := WithReadSnapshot(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx, _ int64) error {
		trip, err := readPublicTrip(ctx, tx, travellerID, tripID)
		if err != nil {
			return err
		}
		src.Trip = trip

		if src.Cities, src.Trip.CityIDs, err = readPublicCities(ctx, tx, travellerID, tripID); err != nil {
			return err
		}
		if src.Places, err = readPublicPlaces(ctx, tx, travellerID, tripID); err != nil {
			return err
		}
		if src.Photos, err = readPublicPhotos(ctx, tx, travellerID, tripID); err != nil {
			return err
		}
		src.Walks, err = readPublicWalks(ctx, tx, travellerID, tripID)
		return err
	})
	if err != nil {
		return logbook.PublicSource{}, travellerError(err, travellerID)
	}
	return src, nil
}

func readPublicTrip(ctx context.Context, tx *sql.Tx, travellerID, tripID string) (logbook.Trip, error) {
	var t logbook.Trip
	var started, ended sql.NullTime
	var summary, cover sql.NullString

	err := tx.QueryRowContext(ctx, publicTripSQL, travellerID, tripID).
		Scan(&t.ID, &t.Name, &started, &ended, &summary, &cover,
			&t.SharePhotos, &t.ShareNotes, &t.ShareCoordinates)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.Trip{}, fmt.Errorf("%w: %s", logbook.ErrNoTrip, tripID)
	case err != nil:
		return logbook.Trip{}, fmt.Errorf("postgres: reading the shared trip %s: %w", tripID, err)
	}
	t.Start, t.End = instantOrNil(started), instantOrNil(ended)
	t.Summary, t.CoverAsset = textOrNil(summary), textOrNil(cover)
	return t, nil
}

// readPublicCities answers the cities and the id list, from one query.
func readPublicCities(ctx context.Context, tx *sql.Tx, travellerID, tripID string) ([]logbook.City, []string, error) {
	rows, err := tx.QueryContext(ctx, publicCitiesSQL, travellerID, tripID)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading the shared trip's cities: %w", err)
	}
	defer rows.Close()

	var cities []logbook.City
	var ids []string
	for rows.Next() {
		var c logbook.City
		if err := rows.Scan(&c.ID, &c.Name, &c.Country.Code, &c.Country.Name,
			&c.Centre.Lat, &c.Centre.Lng); err != nil {
			return nil, nil, fmt.Errorf("postgres: scanning a shared city: %w", err)
		}
		cities = append(cities, c)
		ids = append(ids, c.ID)
	}
	return cities, ids, rows.Err()
}

func readPublicPlaces(ctx context.Context, tx *sql.Tx, travellerID, tripID string) ([]logbook.Place, error) {
	visits, err := readPublicVisits(ctx, tx, travellerID, tripID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, publicPlacesSQL, travellerID, tripID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the shared trip's places: %w", err)
	}
	defer rows.Close()

	var out []logbook.Place
	for rows.Next() {
		var p logbook.Place
		if err := rows.Scan(&p.ID, &p.CityID, &p.Name,
			&p.Coordinates.Lat, &p.Coordinates.Lng); err != nil {
			return nil, fmt.Errorf("postgres: scanning a shared place: %w", err)
		}
		p.Visits = visits[p.ID]
		out = append(out, p)
	}
	return out, rows.Err()
}

func readPublicVisits(ctx context.Context, tx *sql.Tx, travellerID, tripID string) (map[string][]logbook.Visit, error) {
	rows, err := tx.QueryContext(ctx, publicVisitsSQL, travellerID, tripID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the shared trip's visits: %w", err)
	}
	defer rows.Close()

	out := map[string][]logbook.Visit{}
	for rows.Next() {
		var v logbook.Visit
		var at time.Time
		var note sql.NullString
		if err := rows.Scan(&v.PlaceID, &at, &note); err != nil {
			return nil, fmt.Errorf("postgres: scanning a shared visit: %w", err)
		}
		v.TripID = tripID
		v.At = logbook.At(at)
		v.Note = textOrNil(note)
		out[v.PlaceID] = append(out[v.PlaceID], v)
	}
	return out, rows.Err()
}

func readPublicPhotos(ctx context.Context, tx *sql.Tx, travellerID, tripID string) ([]logbook.Photo, error) {
	rows, err := tx.QueryContext(ctx, publicPhotosSQL, travellerID, tripID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the shared trip's photographs: %w", err)
	}
	defer rows.Close()

	var out []logbook.Photo
	for rows.Next() {
		var p logbook.Photo
		var takenAt time.Time
		var placeID, caption sql.NullString
		var lat, lng sql.NullFloat64
		var accuracy sql.NullInt64
		if err := rows.Scan(&p.ID, &p.CityID, &placeID, &takenAt, &p.Asset,
			&caption, &lat, &lng, &accuracy); err != nil {
			return nil, fmt.Errorf("postgres: scanning a shared photograph: %w", err)
		}
		p.TripID = tripID
		p.TakenAt = logbook.At(takenAt)
		p.PlaceID, p.Caption = textOrNil(placeID), textOrNil(caption)
		p.AccuracyMetres = intOrNil(accuracy)
		if lat.Valid && lng.Valid {
			p.Coordinates = &logbook.LatLng{Lat: lat.Float64, Lng: lng.Float64}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readPublicWalks(ctx context.Context, tx *sql.Tx, travellerID, tripID string) ([]logbook.Walk, error) {
	points, err := readPublicWalkPoints(ctx, tx, travellerID, tripID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, publicWalksSQL, travellerID, tripID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the shared trip's walks: %w", err)
	}
	defer rows.Close()

	var out []logbook.Walk
	for rows.Next() {
		var w logbook.Walk
		var recordedOn time.Time
		if err := rows.Scan(&w.ID, &w.CityID, &recordedOn, &w.DistanceKm); err != nil {
			return nil, fmt.Errorf("postgres: scanning a shared walk: %w", err)
		}
		w.TripID = tripID
		w.RecordedOn = logbook.At(recordedOn)
		w.Points = points[w.ID]
		out = append(out, w)
	}
	return out, rows.Err()
}

func readPublicWalkPoints(ctx context.Context, tx *sql.Tx, travellerID, tripID string) (map[string][]logbook.LatLng, error) {
	rows, err := tx.QueryContext(ctx, publicWalkPointsSQL, travellerID, tripID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a shared walk's points: %w", err)
	}
	defer rows.Close()

	out := map[string][]logbook.LatLng{}
	for rows.Next() {
		var walkID string
		var point logbook.LatLng
		if err := rows.Scan(&walkID, &point.Lat, &point.Lng); err != nil {
			return nil, fmt.Errorf("postgres: scanning a shared walk's point: %w", err)
		}
		out[walkID] = append(out[walkID], point)
	}
	return out, rows.Err()
}
