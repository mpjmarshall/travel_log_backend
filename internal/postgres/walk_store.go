// N1's 'Name it' and N1's 'Discard', and the one column in this schema that
// records something nobody can produce a second time.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"travellog/internal/logbook"
)

// upsertWalkSQL is the contract as a statement, and `points` is the fifth
// `case when` rather than a special case — which is the point.
const upsertWalkSQL = `INSERT INTO walks
		(traveller_id, id, trip_id, city_id, recorded_on, distance_km, points, name, dismissed)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, ` + trackFromArraysSQL + `, $9, $10)
	ON CONFLICT ON CONSTRAINT walks_pkey DO UPDATE SET
		trip_id     = CASE WHEN $11::boolean THEN EXCLUDED.trip_id     ELSE walks.trip_id     END,
		city_id     = CASE WHEN $12::boolean THEN EXCLUDED.city_id     ELSE walks.city_id     END,
		recorded_on = CASE WHEN $13::boolean THEN EXCLUDED.recorded_on ELSE walks.recorded_on END,
		distance_km = CASE WHEN $14::boolean THEN EXCLUDED.distance_km ELSE walks.distance_km END,
		points      = CASE WHEN $15::boolean THEN EXCLUDED.points      ELSE walks.points      END,
		name        = CASE WHEN $16::boolean THEN EXCLUDED.name        ELSE walks.name        END,
		dismissed   = CASE WHEN $17::boolean THEN EXCLUDED.dismissed   ELSE walks.dismissed   END`

// trackFromArraysSQL turns two parallel float8 arrays into the jsonb array
// the column holds, in sql, and it is `$7` and `$8` of the statement above.
const trackFromArraysSQL = `(SELECT coalesce(
		jsonb_agg(jsonb_build_object('lat', p.lat, 'lng', p.lng) ORDER BY p.ord),
		'[]'::jsonb)
	FROM unnest($7::float8[], $8::float8[]) WITH ORDINALITY AS p(lat, lng, ord))`

// readWalkForWriteSQL is the row before the write, and it answers the same
// two questions readPlaceForWriteSQL answers.
const readWalkForWriteSQL = `SELECT w.trip_id, w.city_id, w.recorded_on, w.distance_km,
		coalesce(t.lats, '{}'), coalesce(t.lngs, '{}')
	FROM walks w
	LEFT JOIN LATERAL (
		SELECT array_agg((pt.value->>'lat')::double precision ORDER BY pt.ord) AS lats,
		       array_agg((pt.value->>'lng')::double precision ORDER BY pt.ord) AS lngs
		FROM jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	) t ON true
	WHERE w.traveller_id = $1::uuid AND w.id = $2`

const readOneWalkSQL = `SELECT id, trip_id, city_id, recorded_on, distance_km, name, dismissed
	FROM walks WHERE traveller_id = $1::uuid AND id = $2`

// readOneWalksPointsSQL is `readWalkPointsSQL` for one walk.
const readOneWalksPointsSQL = `SELECT
		(pt.value->>'lat')::double precision,
		(pt.value->>'lng')::double precision
	FROM walks w
	CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	WHERE w.traveller_id = $1::uuid AND w.id = $2
	ORDER BY pt.ord`

// WalkStore satisfies logbook.WalkStore over the same pool.
type WalkStore struct{ DB *sql.DB }

// PutWalk is N1's two controls and the create, inside WithTravellerTx.
func (s WalkStore) PutWalk(ctx context.Context, travellerID string, w logbook.WalkWrite) (logbook.Walk, int64, error) {
	var walk logbook.Walk
	if err := logbook.CheckWriteID(w.ID); err != nil {
		return logbook.Walk{}, 0, err
	}
	id := *w.ID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		before, err := requireWritableWalk(ctx, tx, travellerID, id, w)
		if err != nil {
			return err
		}
		tripID, cityID := before.tripID, before.cityID
		recordedOn, distanceKm := before.recordedOn, before.distanceKm
		if w.TripID != nil {
			tripID = *w.TripID
		}
		if w.CityID != nil {
			cityID = *w.CityID
		}
		if w.RecordedOn != nil {
			recordedOn = w.RecordedOn.Time()
		}
		if w.DistanceKm != nil {
			distanceKm = *w.DistanceKm
		}
		lats, lngs := before.lats, before.lngs
		if w.Points != nil {
			lats, lngs = splitTrack(*w.Points)
		}
		if err := requireTripForWalk(ctx, tx, travellerID, tripID); err != nil {
			return err
		}
		if err := requireCityForWalk(ctx, tx, travellerID, cityID); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, upsertWalkSQL, travellerID, id,
			tripID, cityID, recordedOn, distanceKm, lats, lngs,
			storedWalkName(w.Name), w.Dismissed != nil && *w.Dismissed,
			w.TripID != nil, w.CityID != nil, w.RecordedOn != nil, w.DistanceKm != nil,
			w.Points != nil,
			logbook.Sent(w.Name), w.Dismissed != nil,
		); err != nil {
			return fmt.Errorf("postgres: upserting the walk %s: %w", id, err)
		}

		read, err := readOneWalk(ctx, tx, travellerID, id)
		if err != nil {
			return err
		}
		walk = read
		return nil
	})
	if err != nil {
		return logbook.Walk{}, 0, travellerError(err, travellerID)
	}
	return walk, version, nil
}

// walkBeforeWrite is the five columns the upsert has to be able to propose
// when the body did not carry them.
type walkBeforeWrite struct {
	tripID, cityID string
	recordedOn     time.Time
	distanceKm     float64
	lats, lngs     []float64
	isCreate       bool
}

// requireWritableWalk refuses a create missing a not NULL field and names it.
func requireWritableWalk(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.WalkWrite) (walkBeforeWrite, error) {
	var before walkBeforeWrite
	err := tx.QueryRowContext(ctx, readWalkForWriteSQL, travellerID, id).
		Scan(&before.tripID, &before.cityID, &before.recordedOn, &before.distanceKm,
			(*float64Slice)(&before.lats), (*float64Slice)(&before.lngs))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before.isCreate = true
	case err != nil:
		return before, fmt.Errorf("postgres: reading the walk %s before writing it: %w", id, err)
	}
	return before, logbook.CheckWalkWritable(before.isCreate, w)
}

// storedWalkName is the name as it goes to the column.
func storedWalkName(sent **string) *string {
	name := logbook.Value(sent)
	if name == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*name)
	return &trimmed
}

// splitTrack is the one line of Go the track costs: two parallel float8
// arrays, which `unnest` pairs back up in the statement.
func splitTrack(points []logbook.LatLng) ([]float64, []float64) {
	lats := make([]float64, len(points))
	lngs := make([]float64, len(points))
	for i, point := range points {
		lats[i], lngs[i] = point.Lat, point.Lng
	}
	return lats, lngs
}

func requireTripForWalk(ctx context.Context, tx *sql.Tx, travellerID, tripID string) error {
	if err := requireRow(ctx, tx, tripExistsSQL, travellerID, tripID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "tripId",
				Why: fmt.Sprintf("%q is not a trip in this log, and a walk happens on one", tripID)}
		}
		return fmt.Errorf("postgres: looking up the trip %s: %w", tripID, err)
	}
	return nil
}

// requireCityForWalk is `requireCityForPlace` with the same field name and a
// different sentence.
func requireCityForWalk(ctx context.Context, tx *sql.Tx, travellerID, cityID string) error {
	if err := requireRow(ctx, tx, cityExistsSQL, travellerID, cityID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "cityId",
				Why: fmt.Sprintf("%q is not a city in this log, and a walk happens in one", cityID)}
		}
		return fmt.Errorf("postgres: looking up the city %s: %w", cityID, err)
	}
	return nil
}

// readOneWalk answers the row, points included.
func readOneWalk(ctx context.Context, tx *sql.Tx, travellerID, walkID string) (logbook.Walk, error) {
	var w logbook.Walk
	var recordedOn time.Time
	var name sql.NullString
	switch err := tx.QueryRowContext(ctx, readOneWalkSQL, travellerID, walkID).
		Scan(&w.ID, &w.TripID, &w.CityID, &recordedOn, &w.DistanceKm, &name, &w.Dismissed); {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.Walk{}, fmt.Errorf("%w: %s", logbook.ErrNoWalk, walkID)
	case err != nil:
		return logbook.Walk{}, fmt.Errorf("postgres: reading the walk %s back: %w", walkID, err)
	}
	w.RecordedOn, w.Name = logbook.At(recordedOn), textOrNil(name)

	rows, err := tx.QueryContext(ctx, readOneWalksPointsSQL, travellerID, walkID)
	if err != nil {
		return logbook.Walk{}, fmt.Errorf("postgres: reading the track of %s: %w", walkID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var point logbook.LatLng
		if err := rows.Scan(&point.Lat, &point.Lng); err != nil {
			return logbook.Walk{}, fmt.Errorf("postgres: scanning a point of %s: %w", walkID, err)
		}
		w.Points = append(w.Points, point)
	}
	return w, rows.Err()
}

// float64Slice is what lets `array_agg(...)` come back into a []float64
// through database/sql alone.
type float64Slice []float64

func (s *float64Slice) Scan(value any) error {
	var text string
	switch typed := value.(type) {
	case nil:
		*s = nil
		return nil
	case []byte:
		text = string(typed)
	case string:
		text = typed
	default:
		return fmt.Errorf("postgres: a float8[] came back as %T", value)
	}

	text = strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")
	if text == "" {
		*s = []float64{}
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]float64, len(parts))
	for i, part := range parts {
		parsed, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return fmt.Errorf("postgres: %q is not a coordinate in a float8[]: %w", part, err)
		}
		out[i] = parsed
	}
	*s = out
	return nil
}
