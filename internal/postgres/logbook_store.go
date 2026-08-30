// The storage half of logbook.Store, and the TEN queries one read is made of.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"travellog/internal/logbook"
)

// LogbookStore is logbook.Store over *sql.DB.
type LogbookStore struct{ DB *sql.DB }

// sharedSQL is the derived flag, spelled once and used by both trip reads.
const sharedSQL = `EXISTS (SELECT 1 FROM share_links s
		WHERE s.traveller_id = t.traveller_id AND s.trip_id = t.id AND s.revoked_at IS NULL)`

const readTripsSQL = `SELECT t.id, t.name, t.started_on, t.ended_on, t.summary, t.cover_asset,
		t.share_photos, t.share_notes, t.share_coordinates, ` + sharedSQL + `
	FROM trips t WHERE t.traveller_id = $1::uuid ORDER BY t.id`

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

// Read is the conditional read.
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
			&t.SharePhotos, &t.ShareNotes, &t.ShareCoordinates, &t.Shared); err != nil {
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
		var at time.Time
		var note sql.NullString
		if err := rows.Scan(&v.ID, &v.PlaceID, &v.TripID, &at, &note); err != nil {
			return nil, fmt.Errorf("postgres: scanning a visit: %w", err)
		}
		v.At = logbook.At(at)
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
		var takenAt time.Time
		var filedLater sql.NullTime
		var placeID, visitID, caption sql.NullString
		var lat, lng sql.NullFloat64
		var accuracy sql.NullInt64
		if err := rows.Scan(&p.ID, &p.TripID, &p.CityID, &takenAt, &p.Asset,
			&placeID, &visitID, &caption, &lat, &lng, &accuracy, &filedLater); err != nil {
			return nil, fmt.Errorf("postgres: scanning a photo: %w", err)
		}
		p.TakenAt = logbook.At(takenAt)
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
		var recordedOn time.Time
		var name sql.NullString
		if err := rows.Scan(&w.ID, &w.TripID, &w.CityID, &recordedOn, &w.DistanceKm,
			&name, &w.Dismissed); err != nil {
			return nil, fmt.Errorf("postgres: scanning a walk: %w", err)
		}
		w.RecordedOn = logbook.At(recordedOn)
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
// Traveller.
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

// upsertTripSQL is the idempotent write on a client-minted key, and what it
// does not write is the point — twice over now.
const upsertTripSQL = `INSERT INTO trips
		(traveller_id, id, name, started_on, ended_on, summary, cover_asset)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
	ON CONFLICT ON CONSTRAINT trips_pkey DO UPDATE SET
		name        = CASE WHEN $8::boolean  THEN EXCLUDED.name        ELSE trips.name        END,
		started_on  = CASE WHEN $9::boolean  THEN EXCLUDED.started_on  ELSE trips.started_on  END,
		ended_on    = CASE WHEN $10::boolean THEN EXCLUDED.ended_on    ELSE trips.ended_on    END,
		summary     = CASE WHEN $11::boolean THEN EXCLUDED.summary     ELSE trips.summary     END,
		cover_asset = CASE WHEN $12::boolean THEN EXCLUDED.cover_asset ELSE trips.cover_asset END`

// readTripForWriteSQL is the row as it stands BEFORE the upsert, and it
// answers two questions the pointer contract asks that nothing else can.
const readTripForWriteSQL = `SELECT name, started_on, ended_on FROM trips
	WHERE traveller_id = $1::uuid AND id = $2`

const deleteTripCitiesSQL = `DELETE FROM trip_cities WHERE traveller_id = $1::uuid AND trip_id = $2`

const insertTripCitySQL = `INSERT INTO trip_cities (traveller_id, trip_id, city_id, ordinal)
	VALUES ($1::uuid, $2, $3, $4)`

const cityExistsSQL = `SELECT 1 FROM cities WHERE traveller_id = $1::uuid AND id = $2`

// mediaObjectCommittedSQL is `uploaded_at is not null`, and the predicate is
// the whole of it.
const mediaObjectCommittedSQL = `SELECT 1 FROM media_objects
	WHERE traveller_id = $1::uuid AND id = $2 AND uploaded_at IS NOT NULL`

const readOneTripSQL = `SELECT t.id, t.name, t.started_on, t.ended_on, t.summary, t.cover_asset,
		t.share_photos, t.share_notes, t.share_coordinates, ` + sharedSQL + `
	FROM trips t WHERE t.traveller_id = $1::uuid AND t.id = $2`

const readOneTripsCitiesSQL = `SELECT city_id FROM trip_cities
	WHERE traveller_id = $1::uuid AND trip_id = $2 ORDER BY ordinal`

// PutTrip is the whole-state upsert, inside WithTravellerTx.
func (s LogbookStore) PutTrip(ctx context.Context, travellerID string, w logbook.TripWrite) (logbook.Trip, int64, error) {
	var trip logbook.Trip
	if w.ID == nil {
		return logbook.Trip{}, 0, logbook.InvalidFieldError{Field: "id", Why: "a write names its trip"}
	}
	id := *w.ID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		before, err := requireWritableTrip(ctx, tx, travellerID, id, w)
		if err != nil {
			return err
		}
		if w.CityIDs != nil {
			if err := requireCities(ctx, tx, travellerID, *w.CityIDs); err != nil {
				return err
			}
		}
		if err := requireCover(ctx, tx, travellerID, logbook.Value(w.CoverAsset)); err != nil {
			return err
		}
		name := before.name
		if w.Name != nil {
			name = *w.Name
		}
		if _, err := tx.ExecContext(ctx, upsertTripSQL, travellerID, id,
			name,
			timeOrNil(logbook.Value(w.Start)), timeOrNil(logbook.Value(w.End)),
			logbook.Value(w.Summary), logbook.Value(w.CoverAsset),
			w.Name != nil,
			logbook.Sent(w.Start), logbook.Sent(w.End),
			logbook.Sent(w.Summary), logbook.Sent(w.CoverAsset),
		); err != nil {
			return fmt.Errorf("postgres: upserting the trip %s: %w", id, err)
		}
		if w.CityIDs != nil {
			if err := replaceTripCities(ctx, tx, travellerID, id, *w.CityIDs); err != nil {
				return err
			}
		}

		read, err := readOneTrip(ctx, tx, travellerID, id)
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

// requireWritableTrip is the half of the validation that needs the stored row,
// It names a field rather than letting a constraint answer.
func requireWritableTrip(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.TripWrite) (tripBeforeWrite, error) {
	var before tripBeforeWrite
	err := tx.QueryRowContext(ctx, readTripForWriteSQL, travellerID, id).
		Scan(&before.name, &before.started, &before.ended)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if w.Name == nil {
			return before, logbook.InvalidFieldError{Field: "name",
				Why: "a trip that is not in this log yet has no name to leave alone"}
		}
	case err != nil:
		return before, fmt.Errorf("postgres: reading the trip %s before writing it: %w", id, err)
	}

	start, end := before.started, before.ended
	if logbook.Sent(w.Start) {
		start = nullTimeOf(logbook.Value(w.Start))
	}
	if logbook.Sent(w.End) {
		end = nullTimeOf(logbook.Value(w.End))
	}
	if start.Valid && end.Valid && end.Time.Before(start.Time) {
		return before, logbook.InvalidFieldError{Field: "end", Why: "a trip cannot end before it starts"}
	}
	return before, nil
}

// tripBeforeWrite is's three columns the upsert has to know about the row
// it is replacing.
type tripBeforeWrite struct {
	name           string
	started, ended sql.NullTime
}

func nullTimeOf(i *logbook.Instant) sql.NullTime {
	if i == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: i.Time(), Valid: true}
}

// replaceTripCities is DELETE-THEN-INSERT, which is the mandated strategy and
// a measurement rather than a style.
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
	if err := requireRow(ctx, tx, mediaObjectCommittedSQL, travellerID, *asset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "coverAsset",
				Why: "that object has not been uploaded — begin it, PUT the bytes, " +
					"and commit it before anything references it"}
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
			&t.SharePhotos, &t.ShareNotes, &t.ShareCoordinates, &t.Shared); {
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

// setTravellerNameSQL is the only write that touches the traveller row's own
// data.
const setTravellerNameSQL = `UPDATE travellers SET name = $2 WHERE id = $1::uuid`

// SetTravellerName is U1's pencil.
func (s LogbookStore) SetTravellerName(ctx context.Context, travellerID, name string) (logbook.Traveller, int64, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return logbook.Traveller{}, 0, logbook.InvalidFieldError{Field: "name",
			Why: "a traveller needs a name, and an empty one is not a way to clear it"}
	}
	if len(trimmed) > logbook.MaxNameBytes {
		return logbook.Traveller{}, 0, logbook.InvalidFieldError{Field: "name",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d",
				len(trimmed), logbook.MaxNameBytes)}
	}

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, setTravellerNameSQL, travellerID, trimmed); err != nil {
			return fmt.Errorf("postgres: naming the traveller %s: %w", travellerID, err)
		}
		return nil
	})
	if err != nil {
		return logbook.Traveller{}, 0, travellerError(err, travellerID)
	}
	return logbook.Traveller{Name: trimmed}, version, nil
}

// deleteTripSQL is one statement, and every other row D3 itemises goes
// The schema says so rather than because go asks twice.
const deleteTripSQL = `DELETE FROM trips WHERE traveller_id = $1::uuid AND id = $2`

// errNothingDeleted is how a miss rolls the version bump back.
var errNothingDeleted = errors.New("postgres: that trip was not in this log")

// DeleteTrip is D3, and it answers the whole log.
func (s LogbookStore) DeleteTrip(ctx context.Context, travellerID, tripID string) (logbook.Snapshot, error) {
	var snap logbook.Snapshot

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, deleteTripSQL, travellerID, tripID)
		if err != nil {
			return fmt.Errorf("postgres: deleting the trip %s: %w", tripID, err)
		}
		gone, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("postgres: counting the deleted trip %s: %w", tripID, err)
		}
		if gone == 0 {
			return errNothingDeleted
		}
		doc, err := readDocument(ctx, tx, travellerID)
		if err != nil {
			return err
		}
		snap.Document = &doc
		return nil
	})
	switch {
	case errors.Is(err, errNothingDeleted):
		return s.Read(ctx, travellerID, func(int64) bool { return true })
	case err != nil:
		return logbook.Snapshot{}, travellerError(err, travellerID)
	}
	snap.Version = version
	return snap, nil
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
