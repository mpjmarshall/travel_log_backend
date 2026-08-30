// C1's pin, D2's removal, and the one ordered child collection in this schema
// that something else references.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"travellog/internal/logbook"
)

// PlaceStore satisfies logbook.PlaceStore over the same pool.
type PlaceStore struct{ DB *sql.DB }

// maxVisitsPerStatement bounds one multi-row insert, and the number is
// arithmetic rather than taste.
const maxVisitsPerStatement = 5000

const upsertPlaceSQL = `INSERT INTO places
		(traveller_id, id, city_id, name, lat, lng, plan, cover_asset)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT ON CONSTRAINT places_pkey DO UPDATE SET
		city_id     = CASE WHEN $9::boolean  THEN EXCLUDED.city_id     ELSE places.city_id     END,
		name        = CASE WHEN $10::boolean THEN EXCLUDED.name        ELSE places.name        END,
		lat         = CASE WHEN $11::boolean THEN EXCLUDED.lat         ELSE places.lat         END,
		lng         = CASE WHEN $11::boolean THEN EXCLUDED.lng         ELSE places.lng         END,
		plan        = CASE WHEN $12::boolean THEN EXCLUDED.plan        ELSE places.plan        END,
		cover_asset = CASE WHEN $13::boolean THEN EXCLUDED.cover_asset ELSE places.cover_asset END`

// readPlaceForWriteSQL is the row before the write, and it answers the same
// two questions readTripForWriteSQL answers for trips.
const readPlaceForWriteSQL = `SELECT city_id, name, lat, lng FROM places
	WHERE traveller_id = $1::uuid AND id = $2`

const readOnePlaceSQL = `SELECT id, city_id, name, lat, lng, plan, cover_asset
	FROM places WHERE traveller_id = $1::uuid AND id = $2`

// readOnePlacesVisitsSQL orders by ordinal then id, so a re-read is stable.
const readOnePlacesVisitsSQL = `SELECT id, place_id, trip_id, at, note FROM visits
	WHERE traveller_id = $1::uuid AND place_id = $2 ORDER BY ordinal, id`

// tripsPresentSQL and visitsHeldElsewhereSQL are one statement each for the
// whole array, where PutTrip does one round trip per city.
const tripsPresentSQL = `SELECT id FROM trips WHERE traveller_id = $1::uuid AND id = ANY($2)`

const visitsHeldElsewhereSQL = `SELECT id, place_id FROM visits
	WHERE traveller_id = $1::uuid AND id = ANY($2) AND place_id <> $3`

// occasionsAtPlaceSQL is what makes the empty array's refusal a statement
// about destruction rather than about shape.
const occasionsAtPlaceSQL = `SELECT count(*) FROM visits
	WHERE traveller_id = $1::uuid AND place_id = $2`

// occupiedDepartingVisitsSQL is the guard that keeps the pair coherent.
const occupiedDepartingVisitsSQL = `SELECT v.id, count(*) FROM visits v
	JOIN photos p ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
	WHERE v.traveller_id = $1::uuid AND v.place_id = $2 AND v.id <> ALL($3)
	GROUP BY v.id ORDER BY v.id`

// offsetVisitOrdinalsSQL parks every stored ordinal above every ordinal the
// incoming array will use, in one statement.
const offsetVisitOrdinalsSQL = `UPDATE visits SET ordinal = ordinal + GREATEST(
		$3::int,
		(SELECT max(ordinal) + 1 FROM visits WHERE traveller_id = $1::uuid AND place_id = $2))
	WHERE traveller_id = $1::uuid AND place_id = $2`

// deleteDepartedVisitsSQL removes only what the incoming array left out.
const deleteDepartedVisitsSQL = `DELETE FROM visits
	WHERE traveller_id = $1::uuid AND place_id = $2 AND id <> ALL($3)`

// PutPlace is C1's pin and every later write to a place, inside
// WithTravellerTx.
func (s PlaceStore) PutPlace(ctx context.Context, travellerID string, w logbook.PlaceWrite) (logbook.Place, int64, error) {
	var place logbook.Place
	if w.ID == nil {
		return logbook.Place{}, 0, logbook.InvalidFieldError{Field: "id", Why: "a write names its place"}
	}
	id := *w.ID

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		before, err := requireWritablePlace(ctx, tx, travellerID, id, w)
		if err != nil {
			return err
		}
		cityID, name, at := before.cityID, before.name, before.at
		if w.CityID != nil {
			cityID = *w.CityID
		}
		if w.Name != nil {
			name = *w.Name
		}
		if w.Coordinates != nil {
			at = *w.Coordinates
		}
		if err := requireCityForPlace(ctx, tx, travellerID, cityID); err != nil {
			return err
		}
		if err := requireCover(ctx, tx, travellerID, logbook.Value(w.CoverAsset)); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, upsertPlaceSQL, travellerID, id,
			cityID, name, at.Lat, at.Lng,
			logbook.Value(w.Plan), logbook.Value(w.CoverAsset),
			w.CityID != nil, w.Name != nil, w.Coordinates != nil,
			logbook.Sent(w.Plan), logbook.Sent(w.CoverAsset),
		); err != nil {
			return fmt.Errorf("postgres: upserting the place %s: %w", id, err)
		}

		if w.Visits != nil {
			if err := writeVisits(ctx, tx, travellerID, id, *w.Visits); err != nil {
				return err
			}
		}

		read, err := readOnePlace(ctx, tx, travellerID, id)
		if err != nil {
			return err
		}
		place = read
		return nil
	})
	if err != nil {
		return logbook.Place{}, 0, travellerError(err, travellerID)
	}
	return place, version, nil
}

// refuseClearingAPlaceThatHasOccasions answers the `visits: []` body, and the
// two answers are not two spellings of one thing.
func refuseClearingAPlaceThatHasOccasions(ctx context.Context, tx *sql.Tx, travellerID, placeID string) error {
	var occasions int
	if err := tx.QueryRowContext(ctx, occasionsAtPlaceSQL, travellerID, placeID).Scan(&occasions); err != nil {
		return fmt.Errorf("postgres: counting the occasions at %s: %w", placeID, err)
	}
	if occasions == 0 {
		return nil
	}
	return logbook.InvalidFieldError{Field: "visits",
		Why: fmt.Sprintf("an empty visits array is a request to clear all %d occasions at "+
			"this place, which unfiles every photograph filed to them — no control in "+
			"the client asks for that, so this build refuses it. OMIT the key to leave "+
			"the visits alone", occasions)}
}

// writeVisits is the four phases, in the order that preserves the filing.
func writeVisits(ctx context.Context, tx *sql.Tx, travellerID, placeID string, visits []logbook.Visit) error {
	if len(visits) == 0 {
		return refuseClearingAPlaceThatHasOccasions(ctx, tx, travellerID, placeID)
	}

	ids := make([]string, len(visits))
	trips := make([]string, 0, len(visits))
	seenTrip := map[string]bool{}
	for i, visit := range visits {
		ids[i] = visit.ID
		if !seenTrip[visit.TripID] {
			seenTrip[visit.TripID] = true
			trips = append(trips, visit.TripID)
		}
	}

	if err := requireTripsForVisits(ctx, tx, travellerID, trips); err != nil {
		return err
	}
	if err := refuseVisitsHeldElsewhere(ctx, tx, travellerID, placeID, ids); err != nil {
		return err
	}
	if err := refuseDroppingAnOccupiedOccasion(ctx, tx, travellerID, placeID, ids); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, offsetVisitOrdinalsSQL, travellerID, placeID, len(visits)); err != nil {
		return fmt.Errorf("postgres: parking the visit ordinals of %s: %w", placeID, err)
	}
	for start := 0; start < len(visits); start += maxVisitsPerStatement {
		end := min(start+maxVisitsPerStatement, len(visits))
		if err := upsertVisits(ctx, tx, travellerID, placeID, visits[start:end], start); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, deleteDepartedVisitsSQL, travellerID, placeID, ids); err != nil {
		return fmt.Errorf("postgres: removing the departed visits of %s: %w", placeID, err)
	}
	return nil
}

// upsertVisits writes one batch as a single multi-row insert.
func upsertVisits(ctx context.Context, tx *sql.Tx, travellerID, placeID string, visits []logbook.Visit, firstOrdinal int) error {
	args := make([]any, 0, 2+5*len(visits))
	args = append(args, travellerID, placeID)

	var rows strings.Builder
	for i, visit := range visits {
		if i > 0 {
			rows.WriteString(", ")
		}
		n := len(args)
		rows.WriteString("($1::uuid, $" + strconv.Itoa(n+1) +
			", $2, $" + strconv.Itoa(n+2) +
			", $" + strconv.Itoa(n+3) +
			", $" + strconv.Itoa(n+4) +
			", $" + strconv.Itoa(n+5) + ")")
		args = append(args, visit.ID, visit.TripID, firstOrdinal+i, visit.At.Time(), visit.Note)
	}

	statement := `INSERT INTO visits (traveller_id, id, place_id, trip_id, ordinal, at, note)
		VALUES ` + rows.String() + `
		ON CONFLICT ON CONSTRAINT visits_pkey DO UPDATE SET
			place_id = EXCLUDED.place_id,
			trip_id  = EXCLUDED.trip_id,
			ordinal  = EXCLUDED.ordinal,
			at       = EXCLUDED.at,
			note     = EXCLUDED.note`

	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("postgres: writing %d visits of %s: %w", len(visits), placeID, err)
	}
	return nil
}

// requireCityForPlace is `requireCities` with the other field name, and the
// difference is the whole reason it is a second function.
func requireCityForPlace(ctx context.Context, tx *sql.Tx, travellerID, cityID string) error {
	if err := requireRow(ctx, tx, cityExistsSQL, travellerID, cityID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return logbook.InvalidFieldError{Field: "cityId",
				Why: fmt.Sprintf("%q is not a city in this log, and a place belongs to one", cityID)}
		}
		return fmt.Errorf("postgres: looking up the city %s: %w", cityID, err)
	}
	return nil
}

// requireTripsForVisits names the field rather than letting visits_trip_fk
// answer with a 500 that has nothing on it.
func requireTripsForVisits(ctx context.Context, tx *sql.Tx, travellerID string, trips []string) error {
	rows, err := tx.QueryContext(ctx, tripsPresentSQL, travellerID, trips)
	if err != nil {
		return fmt.Errorf("postgres: looking up the trips a visits array names: %w", err)
	}
	defer rows.Close()

	present := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("postgres: scanning a trip a visits array names: %w", err)
		}
		present[id] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: reading the trips a visits array names: %w", err)
	}
	for _, id := range trips {
		if !present[id] {
			return logbook.InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("%q is not a trip in this log, and an occasion happens on one", id)}
		}
	}
	return nil
}

// refuseVisitsHeldElsewhere stops one place's write from stealing another
// place's occasion.
func refuseVisitsHeldElsewhere(ctx context.Context, tx *sql.Tx, travellerID, placeID string, ids []string) error {
	rows, err := tx.QueryContext(ctx, visitsHeldElsewhereSQL, travellerID, ids, placeID)
	if err != nil {
		return fmt.Errorf("postgres: looking up the visits %s claims: %w", placeID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, held string
		if err := rows.Scan(&id, &held); err != nil {
			return fmt.Errorf("postgres: scanning a claimed visit: %w", err)
		}
		return logbook.InvalidFieldError{Field: "visits",
			Why: fmt.Sprintf("the occasion %s already belongs to %s, and a place write "+
				"does not move one between pins", id, held)}
	}
	return rows.Err()
}

// refuseDroppingAnOccupiedOccasion keeps `place_id` and `visit_id` in step
// through the one route that could put them out of step.
func refuseDroppingAnOccupiedOccasion(ctx context.Context, tx *sql.Tx, travellerID, placeID string, keeping []string) error {
	rows, err := tx.QueryContext(ctx, occupiedDepartingVisitsSQL, travellerID, placeID, keeping)
	if err != nil {
		return fmt.Errorf("postgres: looking up the departing visits of %s: %w", placeID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var filed int
		if err := rows.Scan(&id, &filed); err != nil {
			return fmt.Errorf("postgres: scanning a departing visit: %w", err)
		}
		return logbook.InvalidFieldError{Field: "visits",
			Why: fmt.Sprintf("the occasion %s is left out of this array and %d photograph(s) "+
				"are filed to it — dropping it would unfile them and leave them naming a "+
				"place with no occasion, which is a state this log has never held", id, filed)}
	}
	return rows.Err()
}

// placeBeforeWrite is the four columns the upsert has to be able to propose
// when the body did not carry them.
type placeBeforeWrite struct {
	cityID, name string
	at           logbook.LatLng
}

// requireWritablePlace refuses a create missing a not NULL field and names
// it.
func requireWritablePlace(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.PlaceWrite) (placeBeforeWrite, error) {
	var before placeBeforeWrite
	err := tx.QueryRowContext(ctx, readPlaceForWriteSQL, travellerID, id).
		Scan(&before.cityID, &before.name, &before.at.Lat, &before.at.Lng)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if w.CityID == nil {
			return before, logbook.InvalidFieldError{Field: "cityId",
				Why: "a place belongs to a city, and one that is not in this log yet " +
					"has no city to leave alone"}
		}
		if w.Name == nil {
			return before, logbook.InvalidFieldError{Field: "name",
				Why: "a place that is not in this log yet has no name to leave alone"}
		}
		if w.Coordinates == nil {
			return before, logbook.InvalidFieldError{Field: "coordinates",
				Why: "a place that is not in this log yet has no coordinates to leave " +
					"alone — C1 pins at the city's centre when the user has not moved it"}
		}
	case err != nil:
		return before, fmt.Errorf("postgres: reading the place %s before writing it: %w", id, err)
	}
	return before, nil
}

func readOnePlace(ctx context.Context, tx *sql.Tx, travellerID, placeID string) (logbook.Place, error) {
	var p logbook.Place
	var plan, cover sql.NullString
	switch err := tx.QueryRowContext(ctx, readOnePlaceSQL, travellerID, placeID).
		Scan(&p.ID, &p.CityID, &p.Name, &p.Coordinates.Lat, &p.Coordinates.Lng, &plan, &cover); {
	case errors.Is(err, sql.ErrNoRows):
		return logbook.Place{}, fmt.Errorf("%w: %s", logbook.ErrNoPlace, placeID)
	case err != nil:
		return logbook.Place{}, fmt.Errorf("postgres: reading the place %s back: %w", placeID, err)
	}
	p.Plan, p.CoverAsset = textOrNil(plan), textOrNil(cover)

	rows, err := tx.QueryContext(ctx, readOnePlacesVisitsSQL, travellerID, placeID)
	if err != nil {
		return logbook.Place{}, fmt.Errorf("postgres: reading the visits of %s: %w", placeID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var v logbook.Visit
		var at sql.NullTime
		var note sql.NullString
		if err := rows.Scan(&v.ID, &v.PlaceID, &v.TripID, &at, &note); err != nil {
			return logbook.Place{}, fmt.Errorf("postgres: scanning a visit of %s: %w", placeID, err)
		}
		v.At, v.Note = logbook.At(at.Time), textOrNil(note)
		p.Visits = append(p.Visits, v)
	}
	return p, rows.Err()
}

// deletePlacesPhotosSQL and deletePlaceSQL are two lines whose order silently
// inverts D2'S promise, and that order is the whole of the delete branch.
const deletePlacesPhotosSQL = `DELETE FROM photos WHERE traveller_id = $1::uuid AND place_id = $2`

// deletePlaceSQL is the rest of D2, and everything else the sheet promises is
// the schema's rather than Go's.
const deletePlaceSQL = `DELETE FROM places WHERE traveller_id = $1::uuid AND id = $2`

// errNoSuchPlace is how a miss rolls the version bump back — the same device
// errNothingDeleted is for D3.
var errNoSuchPlace = errors.New("postgres: that place was not in this log")

// RemovePlace is D2, and it answers the whole log.
func (s PlaceStore) RemovePlace(ctx context.Context, travellerID, placeID string, deletePhotos bool) (logbook.Snapshot, error) {
	var snap logbook.Snapshot

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if deletePhotos {
			if _, err := tx.ExecContext(ctx, deletePlacesPhotosSQL, travellerID, placeID); err != nil {
				return fmt.Errorf("postgres: deleting the photographs filed at %s: %w", placeID, err)
			}
		}
		result, err := tx.ExecContext(ctx, deletePlaceSQL, travellerID, placeID)
		if err != nil {
			return fmt.Errorf("postgres: removing the place %s: %w", placeID, err)
		}
		gone, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("postgres: counting the removed place %s: %w", placeID, err)
		}
		if gone == 0 {
			return errNoSuchPlace
		}
		doc, err := readDocument(ctx, tx, travellerID)
		if err != nil {
			return err
		}
		snap.Document = &doc
		return nil
	})
	switch {
	case errors.Is(err, errNoSuchPlace):
		return s.Read(ctx, travellerID, func(int64) bool { return true })
	case err != nil:
		return logbook.Snapshot{}, travellerError(err, travellerID)
	}
	snap.Version = version
	return snap, nil
}

// Read is here so a PlaceStore can answer the log a miss should see, and it
// is LogbookStore's own read rather than a second implementation of it.
func (s PlaceStore) Read(ctx context.Context, travellerID string, assemble func(int64) bool) (logbook.Snapshot, error) {
	return LogbookStore{DB: s.DB}.Read(ctx, travellerID, assemble)
}
