// T5's 'Add a city', and the one write in this API whose answer shape depends
// on the request body.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"travellog/internal/logbook"
)

// CityStore satisfies logbook.CityStore over the same pool every other store
// here uses.
type CityStore struct{ DB *sql.DB }

// upsertCitySQL is the trip upsert the shape, and every clause it borrows was
// borrowed for a stated reason.
const upsertCitySQL = `INSERT INTO cities
		(traveller_id, id, name, country_code, country_name, centre_lat, centre_lng, cover_asset)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT ON CONSTRAINT cities_pkey DO UPDATE SET
		name         = CASE WHEN $9::boolean  THEN EXCLUDED.name         ELSE cities.name         END,
		country_code = CASE WHEN $10::boolean THEN EXCLUDED.country_code ELSE cities.country_code END,
		country_name = CASE WHEN $10::boolean THEN EXCLUDED.country_name ELSE cities.country_name END,
		centre_lat   = CASE WHEN $11::boolean THEN EXCLUDED.centre_lat   ELSE cities.centre_lat   END,
		centre_lng   = CASE WHEN $11::boolean THEN EXCLUDED.centre_lng   ELSE cities.centre_lng   END,
		cover_asset  = CASE WHEN $12::boolean THEN EXCLUDED.cover_asset  ELSE cities.cover_asset  END`

const readCityForWriteSQL = `SELECT name, country_code, country_name, centre_lat, centre_lng
	FROM cities WHERE traveller_id = $1::uuid AND id = $2`

const readOneCitySQL = `SELECT id, name, country_code, country_name, centre_lat, centre_lng, cover_asset
	FROM cities WHERE traveller_id = $1::uuid AND id = $2`

// attachCitySQL appends the city to the end of that trip's ordered itinerary,
// It is one statement doing three things on purpose.
const attachCitySQL = `INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
	SELECT $1::uuid, $2, $3, coalesce(max(ordinal), -1) + 1
		FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2
	ON CONFLICT ON CONSTRAINT trip_cities_pkey DO NOTHING`

// PutCity is createCity, inside WithTravellerTx: one transaction, the
// traveller's advisory lock, and the version bump taken before the body runs.
func (s CityStore) PutCity(ctx context.Context, travellerID string, w logbook.CityWrite) (logbook.CityWritten, error) {
	var out logbook.CityWritten
	if err := logbook.CheckWriteID(w.ID); err != nil {
		return out, err
	}
	id := *w.ID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		before, err := requireWritableCity(ctx, tx, travellerID, id, w)
		if err != nil {
			return err
		}
		if err := requireCover(ctx, tx, travellerID, logbook.Value(w.CoverAsset)); err != nil {
			return err
		}

		name, country, centre := before.name, before.country, before.centre
		if w.Name != nil {
			name = *w.Name
		}
		if w.Country != nil {
			country = *w.Country
		}
		if w.Centre != nil {
			centre = *w.Centre
		}

		if _, err := tx.ExecContext(ctx, upsertCitySQL, travellerID, id,
			name, country.Code, country.Name, centre.Lat, centre.Lng,
			logbook.Value(w.CoverAsset),
			w.Name != nil, w.Country != nil, w.Centre != nil, logbook.Sent(w.CoverAsset),
		); err != nil {
			return fmt.Errorf("postgres: upserting the city %s: %w", id, err)
		}

		if w.AttachTo != nil {
			if err := requireTrip(ctx, tx, travellerID, *w.AttachTo); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, attachCitySQL, travellerID, *w.AttachTo, id); err != nil {
				return fmt.Errorf("postgres: attaching %s to %s: %w", id, *w.AttachTo, err)
			}
		}

		read, err := readOneCity(ctx, tx, travellerID, id)
		if err != nil {
			return err
		}
		out.City = read

		if w.AttachTo != nil {
			doc, err := readDocument(ctx, tx, travellerID)
			if err != nil {
				return err
			}
			out.Document = &doc
		}
		return nil
	})
	if err != nil {
		return logbook.CityWritten{}, travellerError(err, travellerID)
	}
	out.Version = version
	return out, nil
}

// cityBeforeWrite is the five columns the upsert has to be able to propose
// when the body did not carry them.
type cityBeforeWrite struct {
	name     string
	country  logbook.Country
	centre   logbook.LatLng
	isCreate bool
}

// requireWritableCity refuses a create that is missing a not NULL field, and
// names the field rather than letting a constraint answer.
func requireWritableCity(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.CityWrite) (cityBeforeWrite, error) {
	var before cityBeforeWrite
	err := tx.QueryRowContext(ctx, readCityForWriteSQL, travellerID, id).
		Scan(&before.name, &before.country.Code, &before.country.Name,
			&before.centre.Lat, &before.centre.Lng)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before.isCreate = true
	case err != nil:
		return before, fmt.Errorf("postgres: reading the city %s before writing it: %w", id, err)
	}
	return before, logbook.CheckCityWritable(before.isCreate, w)
}

// requireTrip names `attachTo` rather than letting trip_cities_trip_fk
// answer.
func requireTrip(ctx context.Context, tx *sql.Tx, travellerID, tripID string) error {
	if err := requireRow(ctx, tx, tripExistsSQL, travellerID, tripID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "attachTo",
				Why: fmt.Sprintf("%q is not a trip in this log", tripID)}
		}
		return fmt.Errorf("postgres: looking up the trip %s: %w", tripID, err)
	}
	return nil
}

func readOneCity(ctx context.Context, tx *sql.Tx, travellerID, cityID string) (logbook.City, error) {
	var c logbook.City
	var cover sql.NullString
	switch err := tx.QueryRowContext(ctx, readOneCitySQL, travellerID, cityID).
		Scan(&c.ID, &c.Name, &c.Country.Code, &c.Country.Name,
			&c.Centre.Lat, &c.Centre.Lng, &cover); {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.City{}, fmt.Errorf("postgres: the city %s vanished between the write and the read back", cityID)
	case err != nil:
		return logbook.City{}, fmt.Errorf("postgres: reading the city %s back: %w", cityID, err)
	}
	c.CoverAsset = textOrNil(cover)
	return c, nil
}
