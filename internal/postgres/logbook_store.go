// The storage half of logbook.Store, and the six queries one read is made of.
//
// EVERY ONE OF THEM RUNS INSIDE ONE REPEATABLE-READ SNAPSHOT, and the version
// was read inside it before any of them (read_tx.go). Under READ COMMITTED
// each statement sees a newer database than the last, so a write landing
// mid-read is in the photographs and not in the trips — and the phone stores
// that torn document under a number describing a different moment, believes
// it, and stops asking.
//
// TWO CHILD LISTS ARE THEIR OWN QUERY RATHER THAN A JOIN, AND THE REASON IS
// THE SAME BOTH TIMES. A trip's cities and a place's visits are ordered lists
// nested inside their parent on the wire; joining would multiply the parent's
// row by its children and every scan would have to de-duplicate a trip's name
// against itself. Two queries and a map from parent id to children is smaller,
// and it is what keeps `ORDER BY ordinal` legible — which is load-bearing
// (DEC-64, DEC-26) rather than tidy.
//
// WALKS' POINTS ARE UNNESTED IN SQL, NOT DECODED IN GO, AND THAT IS NOT A
// PREFERENCE. `walks.points` is jsonb, and the obvious Go answer —
// json.Unmarshal into []LatLng — would make this the SECOND non-test file in
// the module importing encoding/json, which internal/httpx's AST sweep asserts
// against (spec L19). `jsonb_array_elements … WITH ORDINALITY` answers the same
// question in SQL, and Postgres decoding its own jsonb is not payload
// encoding. It also keeps the ORDER explicit: `ORDER BY w.id, pt.ord`, where a
// Go decode would have inherited it silently from the array.
//
// EVERY LIST IS ORDERED BY ITS ID, AND THAT IS ABOUT DETERMINISM RATHER THAN
// DISPLAY. Two reads with no write between them must be byte-identical or the
// ETag is a claim the server cannot keep; the client sorts for display itself
// and always has. The two exceptions are the ordered lists the schema
// mandates: trip_cities by `ordinal`, and visits by `ordinal, id` — the second
// key so that a pre-existing duplicate degrades to stable rather than random,
// because emitting visits in a different order silently rebinds a photograph
// to a different occasion (DEC-26).
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"travellog/internal/logbook"
)

// LogbookStore is logbook.Store over *sql.DB.
type LogbookStore struct{ DB *sql.DB }

const readTripsSQL = `SELECT id, name, started_on, ended_on, summary, cover_asset,
		share_photos, share_notes, share_coordinates
	FROM trips WHERE traveller_id = $1::uuid ORDER BY id`

const readTripCitiesSQL = `SELECT trip_id, city_id
	FROM trip_cities WHERE traveller_id = $1::uuid ORDER BY trip_id, ordinal`

const readCitiesSQL = `SELECT id, name, country_code, country_name, centre_lat, centre_lng, cover_asset
	FROM cities WHERE traveller_id = $1::uuid ORDER BY id`

const readPlacesSQL = `SELECT id, city_id, name, lat, lng, plan, cover_asset
	FROM places WHERE traveller_id = $1::uuid ORDER BY id`

const readVisitsSQL = `SELECT id, place_id, trip_id, at, note
	FROM visits WHERE traveller_id = $1::uuid ORDER BY place_id, ordinal, id`

const readPhotosSQL = `SELECT id, trip_id, city_id, taken_at, asset, place_id, visit_id,
		caption, lat, lng, accuracy_metres, filed_later
	FROM photos WHERE traveller_id = $1::uuid ORDER BY id`

const readWalksSQL = `SELECT id, trip_id, city_id, recorded_on, distance_km, name, dismissed
	FROM walks WHERE traveller_id = $1::uuid ORDER BY id`

const readWalkPointsSQL = `SELECT w.id,
		(pt.value->>'lat')::double precision,
		(pt.value->>'lng')::double precision
	FROM walks w
	CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	WHERE w.traveller_id = $1::uuid
	ORDER BY w.id, pt.ord`

const readTravellerNameSQL = `SELECT name FROM travellers WHERE id = $1::uuid`

// Read is DEC-31's conditional read. The version has already been taken inside
// the snapshot by WithReadSnapshot; `assemble` is asked BEFORE any other query
// runs, so a request that has not changed costs exactly that one indexed row
// read.
func (s LogbookStore) Read(ctx context.Context, travellerID string, assemble func(int64) bool) (logbook.Snapshot, error) {
	var snap logbook.Snapshot

	err := WithReadSnapshot(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx, version int64) error {
		snap.Version = version
		if !assemble(version) {
			return nil
		}
		doc, err := readDocument(ctx, tx, travellerID)
		if err != nil {
			return err
		}
		snap.Document = &doc
		return nil
	})
	if err != nil {
		return logbook.Snapshot{}, travellerError(err, travellerID)
	}
	return snap, nil
}

func readDocument(ctx context.Context, tx *sql.Tx, travellerID string) (logbook.Document, error) {
	var doc logbook.Document
	var err error

	if doc.Trips, err = readTrips(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	if doc.Cities, err = readCities(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	if doc.Places, err = readPlaces(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	if doc.Photos, err = readPhotos(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	if doc.Walks, err = readWalks(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	if doc.Traveller, err = readTravellerName(ctx, tx, travellerID); err != nil {
		return doc, err
	}
	return doc, nil
}

func readTrips(ctx context.Context, tx *sql.Tx, travellerID string) ([]logbook.Trip, error) {
	cities, err := readTripCities(ctx, tx, travellerID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, readTripsSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading trips: %w", err)
	}
	defer rows.Close()

	var out []logbook.Trip
	for rows.Next() {
		var t logbook.Trip
		var started, ended sql.NullTime
		var summary, cover sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &started, &ended, &summary, &cover,
			&t.SharePhotos, &t.ShareNotes, &t.ShareCoordinates); err != nil {
			return nil, fmt.Errorf("postgres: scanning a trip: %w", err)
		}
		t.Start, t.End = instantOrNil(started), instantOrNil(ended)
		t.Summary, t.CoverAsset = textOrNil(summary), textOrNil(cover)
		t.CityIDs = cities[t.ID]
		out = append(out, t)
	}
	return out, rows.Err()
}

func readTripCities(ctx context.Context, tx *sql.Tx, travellerID string) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, readTripCitiesSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading trip_cities: %w", err)
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var tripID, cityID string
		if err := rows.Scan(&tripID, &cityID); err != nil {
			return nil, fmt.Errorf("postgres: scanning a trip's city: %w", err)
		}
		out[tripID] = append(out[tripID], cityID)
	}
	return out, rows.Err()
}

func readCities(ctx context.Context, tx *sql.Tx, travellerID string) ([]logbook.City, error) {
	rows, err := tx.QueryContext(ctx, readCitiesSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading cities: %w", err)
	}
	defer rows.Close()

	var out []logbook.City
	for rows.Next() {
		var c logbook.City
		var cover sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Country.Code, &c.Country.Name,
			&c.Centre.Lat, &c.Centre.Lng, &cover); err != nil {
			return nil, fmt.Errorf("postgres: scanning a city: %w", err)
		}
		c.CoverAsset = textOrNil(cover)
		out = append(out, c)
	}
	return out, rows.Err()
}

func readPlaces(ctx context.Context, tx *sql.Tx, travellerID string) ([]logbook.Place, error) {
	visits, err := readVisits(ctx, tx, travellerID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, readPlacesSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading places: %w", err)
	}
	defer rows.Close()

	var out []logbook.Place
	for rows.Next() {
		var p logbook.Place
		var plan, cover sql.NullString
		if err := rows.Scan(&p.ID, &p.CityID, &p.Name, &p.Coordinates.Lat, &p.Coordinates.Lng,
			&plan, &cover); err != nil {
			return nil, fmt.Errorf("postgres: scanning a place: %w", err)
		}
		p.Plan, p.CoverAsset = textOrNil(plan), textOrNil(cover)
		p.Visits = visits[p.ID]
		out = append(out, p)
	}
	return out, rows.Err()
}

func readVisits(ctx context.Context, tx *sql.Tx, travellerID string) (map[string][]logbook.Visit, error) {
	rows, err := tx.QueryContext(ctx, readVisitsSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading visits: %w", err)
	}
	defer rows.Close()

	out := map[string][]logbook.Visit{}
	for rows.Next() {
		var v logbook.Visit
		var at sql.NullTime
		var note sql.NullString
		if err := rows.Scan(&v.ID, &v.PlaceID, &v.TripID, &at, &note); err != nil {
			return nil, fmt.Errorf("postgres: scanning a visit: %w", err)
		}
		v.At = logbook.At(at.Time)
		v.Note = textOrNil(note)
		out[v.PlaceID] = append(out[v.PlaceID], v)
	}
	return out, rows.Err()
}

func readPhotos(ctx context.Context, tx *sql.Tx, travellerID string) ([]logbook.Photo, error) {
	rows, err := tx.QueryContext(ctx, readPhotosSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading photos: %w", err)
	}
	defer rows.Close()

	var out []logbook.Photo
	for rows.Next() {
		var p logbook.Photo
		var takenAt, filedLater sql.NullTime
		var placeID, visitID, caption sql.NullString
		var lat, lng sql.NullFloat64
		var accuracy sql.NullInt64
		if err := rows.Scan(&p.ID, &p.TripID, &p.CityID, &takenAt, &p.Asset,
			&placeID, &visitID, &caption, &lat, &lng, &accuracy, &filedLater); err != nil {
			return nil, fmt.Errorf("postgres: scanning a photo: %w", err)
		}
		p.TakenAt = logbook.At(takenAt.Time)
		p.PlaceID, p.VisitID, p.Caption = textOrNil(placeID), textOrNil(visitID), textOrNil(caption)
		p.FiledLater = instantOrNil(filedLater)
		p.AccuracyMetres = intOrNil(accuracy)
		if lat.Valid && lng.Valid {
			p.Coordinates = &logbook.LatLng{Lat: lat.Float64, Lng: lng.Float64}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readWalks(ctx context.Context, tx *sql.Tx, travellerID string) ([]logbook.Walk, error) {
	points, err := readWalkPoints(ctx, tx, travellerID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, readWalksSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading walks: %w", err)
	}
	defer rows.Close()

	var out []logbook.Walk
	for rows.Next() {
		var w logbook.Walk
		var recordedOn sql.NullTime
		var name sql.NullString
		if err := rows.Scan(&w.ID, &w.TripID, &w.CityID, &recordedOn, &w.DistanceKm,
			&name, &w.Dismissed); err != nil {
			return nil, fmt.Errorf("postgres: scanning a walk: %w", err)
		}
		w.RecordedOn = logbook.At(recordedOn.Time)
		w.Name = textOrNil(name)
		w.Points = points[w.ID]
		out = append(out, w)
	}
	return out, rows.Err()
}

func readWalkPoints(ctx context.Context, tx *sql.Tx, travellerID string) (map[string][]logbook.LatLng, error) {
	rows, err := tx.QueryContext(ctx, readWalkPointsSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a walk's points: %w", err)
	}
	defer rows.Close()

	out := map[string][]logbook.LatLng{}
	for rows.Next() {
		var walkID string
		var point logbook.LatLng
		if err := rows.Scan(&walkID, &point.Lat, &point.Lng); err != nil {
			return nil, fmt.Errorf("postgres: scanning a walk's point: %w", err)
		}
		out[walkID] = append(out[walkID], point)
	}
	return out, rows.Err()
}

// readTravellerName answers nil for a name nobody has set, never an empty
// Traveller: the client casts `json['name'] as String`, non-nullable, so `{}`
// throws where `null` reads as "a log nobody has named yet".
func readTravellerName(ctx context.Context, tx *sql.Tx, travellerID string) (*logbook.Traveller, error) {
	var name sql.NullString
	switch err := tx.QueryRowContext(ctx, readTravellerNameSQL, travellerID).Scan(&name); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: %s", logbook.ErrNoTraveller, travellerID)
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a traveller's name: %w", err)
	}
	if !name.Valid {
		return nil, nil
	}
	return &logbook.Traveller{Name: name.String}, nil
}

// upsertTripSQL is DEC-32's whole-state write, and WHAT IT DOES NOT NAME IS
// THE POINT. share_photos, share_notes and share_coordinates appear in neither
// the column list nor the SET clause, so a create leaves them at their schema
// defaults and an update leaves them exactly as they were. Naming them in
// EXCLUDED-form would silently reset a group this route does not own (SF6) on
// every rename.
const upsertTripSQL = `INSERT INTO trips
		(traveller_id, id, name, started_on, ended_on, summary, cover_asset)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
	ON CONFLICT ON CONSTRAINT trips_pkey DO UPDATE SET
		name = EXCLUDED.name,
		started_on = EXCLUDED.started_on,
		ended_on = EXCLUDED.ended_on,
		summary = EXCLUDED.summary,
		cover_asset = EXCLUDED.cover_asset`

const deleteTripCitiesSQL = `DELETE FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2`

const insertTripCitySQL = `INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
	VALUES ($1::uuid, $2, $3, $4)`

const cityExistsSQL = `SELECT 1 FROM cities WHERE traveller_id = $1::uuid AND id = $2`

const mediaObjectExistsSQL = `SELECT 1 FROM media_objects WHERE traveller_id = $1::uuid AND id = $2`

const readOneTripSQL = `SELECT id, name, started_on, ended_on, summary, cover_asset,
		share_photos, share_notes, share_coordinates
	FROM trips WHERE traveller_id = $1::uuid AND id = $2`

const readOneTripsCitiesSQL = `SELECT city_id FROM trip_cities
	WHERE traveller_id = $1::uuid AND trip_id = $2 ORDER BY ordinal`

// PutTrip is the whole-state upsert, inside WithTravellerTx: one transaction,
// the traveller's advisory lock, and the version bump taken before the body
// runs so the write knows the version it will commit under.
//
// THE ANSWER IS RE-READ FROM THE ROW AND NOT ASSEMBLED FROM THE REQUEST. The
// three sharing flags are not in the body at all, so a response built from the
// input could only guess at them — and a response built from the input is a
// response that agrees with the client about a write the database may have
// shaped differently.
//
// THE EXISTENCE CHECKS ARE HERE RATHER THAN IN ValidateTrip, and they are here
// rather than left to the foreign keys. Under the traveller's advisory lock
// the check is race-free, which is exactly what DEC-02 says the lock buys; and
// reading a violation back off the driver would mean importing pgconn to read
// SQLSTATE 23503, which cmd/api's import sweep forbids (spec L20, pgx "solely
// as a blank import driver"). Without them an unknown city is a 500 with
// nothing the client can show. DEC-64 deleted the Go check that SUBSTITUTED
// for referential integrity; this is the one that names the field before the
// constraint fires, and the constraint is still what enforces it.
func (s LogbookStore) PutTrip(ctx context.Context, travellerID string, w logbook.TripWrite) (logbook.Trip, int64, error) {
	var trip logbook.Trip

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if err := requireCities(ctx, tx, travellerID, w.CityIDs); err != nil {
			return err
		}
		if err := requireCover(ctx, tx, travellerID, w.CoverAsset); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, upsertTripSQL, travellerID, w.ID, w.Name,
			timeOrNil(w.Start), timeOrNil(w.End), w.Summary, w.CoverAsset); err != nil {
			return fmt.Errorf("postgres: upserting the trip %s: %w", w.ID, err)
		}
		if err := replaceTripCities(ctx, tx, travellerID, w.ID, w.CityIDs); err != nil {
			return err
		}

		read, err := readOneTrip(ctx, tx, travellerID, w.ID)
		if err != nil {
			return err
		}
		trip = read
		return nil
	})
	if err != nil {
		return logbook.Trip{}, 0, travellerError(err, travellerID)
	}
	return trip, version, nil
}

// replaceTripCities is DELETE-THEN-INSERT, which is the mandated strategy and
// a measurement rather than a style. trip_cities_ordinal_uq is NOT deferrable,
// so a UNIQUE index is checked per ROW during a statement even when the final
// state is unique — an UPDATE-in-place reorder collides, and two separate
// statements do not, because the DELETE completes first.
func replaceTripCities(ctx context.Context, tx *sql.Tx, travellerID, tripID string, cityIDs []string) error {
	if _, err := tx.ExecContext(ctx, deleteTripCitiesSQL, travellerID, tripID); err != nil {
		return fmt.Errorf("postgres: clearing the cities of %s: %w", tripID, err)
	}
	for ordinal, cityID := range cityIDs {
		if _, err := tx.ExecContext(ctx, insertTripCitySQL, travellerID, tripID, cityID, ordinal); err != nil {
			return fmt.Errorf("postgres: filing %s under %s: %w", cityID, tripID, err)
		}
	}
	return nil
}

func requireCities(ctx context.Context, tx *sql.Tx, travellerID string, cityIDs []string) error {
	for _, cityID := range cityIDs {
		if err := requireRow(ctx, tx, cityExistsSQL, travellerID, cityID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return logbook.InvalidFieldError{Field: "cityIds",
					Why: fmt.Sprintf("%q is not a city in this log", cityID)}
			}
			return fmt.Errorf("postgres: looking up the city %s: %w", cityID, err)
		}
	}
	return nil
}

func requireCover(ctx context.Context, tx *sql.Tx, travellerID string, asset *string) error {
	if asset == nil {
		return nil
	}
	if err := requireRow(ctx, tx, mediaObjectExistsSQL, travellerID, *asset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "coverAsset",
				Why: "that object has not been uploaded"}
		}
		return fmt.Errorf("postgres: looking up the cover %s: %w", *asset, err)
	}
	return nil
}

func requireRow(ctx context.Context, tx *sql.Tx, query, travellerID, id string) error {
	var one int
	return tx.QueryRowContext(ctx, query, travellerID, id).Scan(&one)
}

func readOneTrip(ctx context.Context, tx *sql.Tx, travellerID, tripID string) (logbook.Trip, error) {
	var t logbook.Trip
	var started, ended sql.NullTime
	var summary, cover sql.NullString

	switch err := tx.QueryRowContext(ctx, readOneTripSQL, travellerID, tripID).
		Scan(&t.ID, &t.Name, &started, &ended, &summary, &cover,
			&t.SharePhotos, &t.ShareNotes, &t.ShareCoordinates); {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.Trip{}, fmt.Errorf("%w: %s", logbook.ErrNoTrip, tripID)
	case err != nil:
		return logbook.Trip{}, fmt.Errorf("postgres: reading the trip %s back: %w", tripID, err)
	}
	t.Start, t.End = instantOrNil(started), instantOrNil(ended)
	t.Summary, t.CoverAsset = textOrNil(summary), textOrNil(cover)

	rows, err := tx.QueryContext(ctx, readOneTripsCitiesSQL, travellerID, tripID)
	if err != nil {
		return logbook.Trip{}, fmt.Errorf("postgres: reading the cities of %s: %w", tripID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cityID string
		if err := rows.Scan(&cityID); err != nil {
			return logbook.Trip{}, fmt.Errorf("postgres: scanning a city of %s: %w", tripID, err)
		}
		t.CityIDs = append(t.CityIDs, cityID)
	}
	return t, rows.Err()
}

// travellerError translates the transaction helpers' sentinel into the
// domain's own, so a handler never has to know internal/postgres exists.
func travellerError(err error, travellerID string) error {
	if errors.Is(err, ErrNoTraveller) {
		return fmt.Errorf("%w: %s", logbook.ErrNoTraveller, travellerID)
	}
	return err
}

func instantOrNil(t sql.NullTime) *logbook.Instant {
	if !t.Valid {
		return nil
	}
	i := logbook.At(t.Time)
	return &i
}

func timeOrNil(i *logbook.Instant) any {
	if i == nil {
		return nil
	}
	return i.Time()
}

func textOrNil(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func intOrNil(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
