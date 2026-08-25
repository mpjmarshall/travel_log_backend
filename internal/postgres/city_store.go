// T5's 'Add a city', and the one write in this API whose ANSWER SHAPE depends
// on the request body.
//
// THE CLIENT DOES THIS IN TWO METHODS AND THE SERVER DOES IT IN ONE
// TRANSACTION. T5 drives `createCity` and `setTripCities`
// (`logbook.dart:321` and `:542`), and `createCity` already takes `attachTo`
// and does both under one `_commit` — so the two writes were never separable
// on the client either. Splitting them here would mean a window in which a
// city exists and belongs to no trip, on a route whose whole purpose is to add
// one to an itinerary.
//
// COUNTRY IS TWO COLUMNS AND ONE WIRE FIELD (DEC-59), so it is ONE `sent` flag
// governing both. Writing `country_code` without `country_name` is not a
// request this API can receive and is not a state a row may hold —
// `cities_country_name_present_ck` refuses the empty string — so a second flag
// would be a second way to get it wrong for no expressible gain. The same
// holds for `centre`, which is `centre_lat` and `centre_lng`.
//
// THERE IS NO DELETE HERE, AND THE ABSENCE IS DEC-57 (see 0001's places
// comment). The client has no delete-a-city control, so no sheet copy
// authorises the cascade, and `places_city_fk`, `photos_city_fk` and
// `walks_city_fk` are all RESTRICT. Whoever adds the control is stopped by the
// database at exactly the moment they should be writing the sentence.
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

// upsertCitySQL is the trip upsert's shape, and every clause it borrows was
// borrowed for a stated reason (DEC-89).
//
// THE PROPOSED ROW MUST BE VALID EVEN WHEN THE CONFLICT WILL DISCARD IT. This
// is the lesson `readTripForWriteSQL` records, and it costs MORE here than it
// does for trips: five NOT NULL columns and four CHECK constraints are
// evaluated against the tuple the INSERT proposes, BEFORE the conflict is
// resolved. So a rename that names only `{id, name}` has to propose the
// country and the centre the row already holds, or `cities_country_code_ck`
// refuses the empty string and the client gets a 500 with no field on it.
//
// FOUR FLAGS FOR SIX COLUMNS. `country` and `centre` are each one wire field
// over two columns — see the file comment.
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

// `tripExistsSQL` is share_store.go's, reused rather than respelled. Two
// identical statement literals in one package is two things to keep in step
// and nothing to gain by it.

// attachCitySQL appends the city to the END of that trip's ordered itinerary,
// and it is ONE statement doing three things on purpose.
//
// THE ORDINAL IS COMPUTED IN THE SAME STATEMENT THAT INSERTS IT. A `SELECT
// max(ordinal)` followed by an INSERT is two round trips and one more place
// for the number to be stale; the subquery here is evaluated against the
// statement's own snapshot, and the traveller's advisory lock is held for the
// whole transaction, so nothing can slot a row in between.
//
// `DO NOTHING` IS WHAT MAKES A RE-PUT HARMLESS. `PUT /v1/cities/{id}` is an
// UPSERT on a client-minted key, so it can be retried — and a retry that
// appended a second row would violate `trip_cities_pkey` and reach the client
// as a 500 on a request that had already succeeded. A city already on the
// itinerary is already attached; the ordinal it holds is where the user put
// it, and moving it to the end would be a reorder nobody asked for.
//
// A GAP IN THE ORDINALS IS LEGAL AND EXPECTED. `trip_cities_ordinal_uq` is
// UNIQUE and `trip_cities_ordinal_ck` is `>= 0`; neither asks for contiguity,
// and the read is `ORDER BY ordinal` rather than by value. So a DO NOTHING
// that has consumed a number costs nothing.
const attachCitySQL = `INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
	SELECT $1::uuid, $2, $3, coalesce(max(ordinal), -1) + 1
		FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2
	ON CONFLICT ON CONSTRAINT trip_cities_pkey DO NOTHING`

// PutCity is createCity, inside WithTravellerTx: one transaction, the
// traveller's advisory lock, and the version bump taken before the body runs.
//
// THE WHOLE DOCUMENT IS READ INSIDE THE WRITE TRANSACTION when `attachTo` was
// sent, which is the arrangement DeleteTrip already uses and for the same
// reason: the lock is held for all of it, so no other write for this traveller
// can land between the INSERT and the ten SELECTs, and the reads see this
// transaction's own work because they are in it.
func (s CityStore) PutCity(ctx context.Context, travellerID string, w logbook.CityWrite) (logbook.CityWritten, error) {
	var out logbook.CityWritten
	if w.ID == nil {
		return out, logbook.InvalidFieldError{Field: "id", Why: "a write names its city"}
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

		// The proposed row, which is the stored one wherever the body was
		// silent — see upsertCitySQL for why an unsent field cannot simply
		// propose NULL.
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
	name    string
	country logbook.Country
	centre  logbook.LatLng
}

// requireWritableCity refuses a CREATE that is missing a NOT NULL field, and
// names the field rather than letting a constraint answer.
//
// THREE FIELDS ARE REQUIRED ON A CREATE AND OPTIONAL ON AN UPDATE, which is
// the shape DEC-89 gives every write in this API. On an update there is a
// stored value to leave alone; on a create there is not, and `cities.name`,
// the two country columns and the two coordinates are all NOT NULL.
//
// THE ORDER OF THE THREE REFUSALS IS THE ORDER OF THE FORM. T5 asks for a
// city, and the geocoder answers with a country and a centre — so a body
// missing the name is a different mistake from one missing the geocoder's
// half, and the first field named is the first one a user could act on.
func requireWritableCity(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.CityWrite) (cityBeforeWrite, error) {
	var before cityBeforeWrite
	err := tx.QueryRowContext(ctx, readCityForWriteSQL, travellerID, id).
		Scan(&before.name, &before.country.Code, &before.country.Name,
			&before.centre.Lat, &before.centre.Lng)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if w.Name == nil {
			return before, logbook.InvalidFieldError{Field: "name",
				Why: "a city that is not in this log yet has no name to leave alone"}
		}
		if w.Country == nil {
			return before, logbook.InvalidFieldError{Field: "country",
				Why: "a city that is not in this log yet has no country to leave alone, " +
					"and country is derived from the city rather than typed (DEC-59)"}
		}
		if w.Centre == nil {
			return before, logbook.InvalidFieldError{Field: "centre",
				Why: "a city that is not in this log yet has no centre to leave alone, " +
					"and C1 pins a new place at it"}
		}
	case err != nil:
		return before, fmt.Errorf("postgres: reading the city %s before writing it: %w", id, err)
	}
	return before, nil
}

// requireTrip names `attachTo` rather than letting trip_cities_trip_fk answer.
//
// IT IS A 422 AND NOT A 404, and the reason is which thing the request is
// about: the path names the CITY, and the trip is a field of the body. The
// client's own `createCity` treats it the same way — it answers null without
// writing when `log.trip(attachTo) == null`, rather than reporting that
// something was not found.
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
		// Unreachable through PutCity, which has just written the row under
		// the traveller's lock. It is here because a re-read that invents a
		// body is the failure this shape exists to prevent — see
		// logbook.ErrNoTrip's own comment.
		return logbook.City{}, fmt.Errorf("postgres: the city %s vanished between the write and the read back", cityID)
	case err != nil:
		return logbook.City{}, fmt.Errorf("postgres: reading the city %s back: %w", cityID, err)
	}
	c.CoverAsset = textOrNil(cover)
	return c, nil
}
