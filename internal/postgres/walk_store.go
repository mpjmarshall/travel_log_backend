// N1's 'Name it' and N1's 'Discard', and the ONE column in this schema that
// records something nobody can produce a second time.
//
// THE WHOLE FILE IS ABOUT ONE `CASE WHEN`. `walks.points` is a jsonb array of
// a day's GPS fixes, and the two controls that write this row — 'Name it' and
// 'Discard' — send `{name}` and `{dismissed}` and never a track. Under a
// whole-state upsert both bodies write `points = '[]'`, and the safety lens
// executed exactly that:
//
//	UPDATE walks SET dismissed=true, points='[]'::jsonb WHERE id='w-busan'
//	-> UPDATE 1, no constraint raised
//	jsonb_array_length(points)  3 -> 0
//
// `walks_points_array_ck CHECK (jsonb_typeof(points) = 'array')` does not stop
// it, because AN EMPTY ARRAY IS AN ARRAY. The row survives, the read returns
// it, `dismissed` is correct, and C2 draws nothing.
//
// SO THERE ARE THREE GUARDS AND EACH REFUSES A DIFFERENT THING. DEC-89's
// pointer contract means an ABSENT `points` writes nothing at all — that is
// the `CASE WHEN $n::boolean` below, and it is what makes both real controls
// safe. `ValidateWalk` refuses an EMPTY array with a 422 naming the field.
// And 0003's `walks_points_present_ck` is the guarantee under both, on
// DEC-58's precedent — a Go check can be bypassed by the next route somebody
// adds and nothing notices.
//
// THERE IS NO `DeleteWalk` AND THAT IS THE CLIENT'S OWN DESIGN. N1's 'Discard'
// is a flag: "Discarding the nudge and discarding the recording are different
// things, and only the first is drawn on N1." D2's sheet promises the track
// stays with its day on BOTH branches, and `walks` has no `place_id` at all,
// so removePlace cannot reach one. Nothing in this app authorises destroying a
// walk, so no route offers it.
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

// upsertWalkSQL is DEC-89's contract as a statement, and `points` is the fifth
// `CASE WHEN` rather than a special case — which is the point. A column that
// needs a rule nobody else has is a column somebody will forget; a column that
// wears the same shape as its four neighbours is one they cannot.
//
// THE TRACK IS BUILT IN SQL FROM TWO float8 ARRAYS AND IS NEITHER MARSHALLED
// NOR STRING-ASSEMBLED IN GO — see trackFromArraysSQL, which is where the
// argument is.
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

// trackFromArraysSQL turns two parallel float8 arrays into the jsonb array the
// column holds, IN SQL, and it is `$7` and `$8` of the statement above.
//
// GO NEVER TOUCHES JSON HERE, AND THAT WAS THE POINT OF WRITING IT THIS WAY.
// There were three candidates and this is the only one that costs nothing:
//
//   - `json.Marshal` in this file. It works, and it would make
//     internal/postgres the THIRD entry on internal/httpx's encoding/json
//     monopoly list — a list whose whole value is that a new entry has to
//     argue for itself. Spending that on a column value is a bad trade.
//   - Hand-rendering the array as text, which is what internal/seed does and
//     says it does under protest. It is a second implementation of a format,
//     and the seed's own comment concedes it exists only because of the same
//     monopoly.
//   - This. `unnest` pairs the two arrays, `WITH ORDINALITY` keeps the index,
//     and `jsonb_agg(… ORDER BY ord)` makes the order EXPLICIT rather than
//     inherited — which is exactly the argument logbook_store.go already makes
//     for unnesting the read in SQL rather than decoding it in Go. The write
//     and the read now use the same mechanism in the same direction.
//
// `coalesce(…, '[]'::jsonb)` IS NOT A WAY TO WRITE AN EMPTY TRACK. jsonb_agg
// over no rows answers NULL, and `walks.points` is NOT NULL, so without this
// an empty array would fail on the wrong constraint and name the wrong thing.
// With it the value reaches `walks_points_present_ck`, which refuses it and
// says which claim is being broken. `ValidateWalk` refuses it long before
// either, with a 422 naming the field.
const trackFromArraysSQL = `(SELECT coalesce(
		jsonb_agg(jsonb_build_object('lat', p.lat, 'lng', p.lng) ORDER BY p.ord),
		'[]'::jsonb)
	FROM unnest($7::float8[], $8::float8[]) WITH ORDINALITY AS p(lat, lng, ord))`

// readWalkForWriteSQL is the row before the write, and it answers the same two
// questions readPlaceForWriteSQL answers: whether this is a CREATE — which
// decides whether an absent NOT NULL field is legal — and what the proposed
// INSERT tuple must carry so five NOT NULL columns and three CHECKs are
// satisfied BEFORE the conflict is resolved.
//
// AN UNSENT FIELD CANNOT PROPOSE NULL, which is what makes this read
// unavoidable rather than an optimisation. The INSERT tuple is validated
// against `walks_points_present_ck` before ON CONFLICT ever runs, so a body of
// `{dismissed:true}` has to propose the track the row already holds. It is
// then discarded by the `CASE WHEN`, and it still has to be there.
//
// IT READS THE TRACK BACK AS TWO ARRAYS rather than as jsonb, so the value it
// re-proposes is the value the column already holds, expressed in the only
// parameters the INSERT takes.
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

// readOneWalksPointsSQL is `readWalkPointsSQL` for ONE walk, and it is a
// second constant rather than a reuse because the two take different
// parameters — that one is keyed on the traveller alone and answers the whole
// log's tracks.
const readOneWalksPointsSQL = `SELECT
		(pt.value->>'lat')::double precision,
		(pt.value->>'lng')::double precision
	FROM walks w
	CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	WHERE w.traveller_id = $1::uuid AND w.id = $2
	ORDER BY pt.ord`

// WalkStore satisfies logbook.WalkStore over the same pool.
type WalkStore struct{ DB *sql.DB }

// PutWalk is N1's two controls and DEC-33's create, inside WithTravellerTx:
// one transaction, the traveller's advisory lock, and the version bump taken
// before the body runs.
//
// THE ANSWER IS RE-READ FROM THE ROW AND NOT ASSEMBLED FROM THE REQUEST, for
// PutTrip's reason — and here it does something the other stores' re-reads do
// not. A body of `{dismissed:true}` carries no track at all, so a response
// built from the input would answer `"points": []` while the database held
// three: the client would splice an empty track into its cached log and C2
// would draw nothing, against a row that is perfectly intact.
func (s WalkStore) PutWalk(ctx context.Context, travellerID string, w logbook.WalkWrite) (logbook.Walk, int64, error) {
	var walk logbook.Walk
	if w.ID == nil {
		return logbook.Walk{}, 0, logbook.InvalidFieldError{Field: "id", Why: "a write names its walk"}
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
			// THE ONE THAT MATTERS. A nil Points writes `false` here, so the
			// SET clause keeps `walks.points` and the day survives the flag.
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
}

// requireWritableWalk refuses a CREATE missing a NOT NULL field and names it.
//
// `points` IS REQUIRED ON A CREATE AND THERE IS NO SERVER-SIDE DEFAULT, which
// is the same answer `coordinates` gets on a place and for a stronger reason:
// C1 can pin at the city's centre because a position it did not choose is
// still a position, while an empty track is a claim that somebody walked
// nowhere. 0003 refuses it outright.
func requireWritableWalk(ctx context.Context, tx *sql.Tx, travellerID, id string, w logbook.WalkWrite) (walkBeforeWrite, error) {
	var before walkBeforeWrite
	err := tx.QueryRowContext(ctx, readWalkForWriteSQL, travellerID, id).
		Scan(&before.tripID, &before.cityID, &before.recordedOn, &before.distanceKm,
			(*float64Slice)(&before.lats), (*float64Slice)(&before.lngs))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		for _, missing := range []struct {
			absent bool
			field  string
			why    string
		}{
			{w.TripID == nil, "tripId", "a walk happens on a trip, and one that is not in " +
				"this log yet has no trip to leave alone"},
			{w.CityID == nil, "cityId", "a walk happens in a city, and one that is not in " +
				"this log yet has no city to leave alone"},
			{w.RecordedOn == nil, "recordedOn", "a walk is a recording of a day, and one " +
				"that is not in this log yet has no day to leave alone"},
			{w.DistanceKm == nil, "distanceKm", "a walk that is not in this log yet has no " +
				"distance to leave alone"},
			{w.Points == nil, "points", "a walk that is not in this log yet has no track to " +
				"leave alone, and there is nothing this build could invent — a track is " +
				"a recording of a day that has passed"},
		} {
			if missing.absent {
				return before, logbook.InvalidFieldError{Field: missing.field, Why: missing.why}
			}
		}
	case err != nil:
		return before, fmt.Errorf("postgres: reading the walk %s before writing it: %w", id, err)
	}
	return before, nil
}

// storedWalkName is the name as it goes to the column: trimmed, and NEVER the
// empty string — `ValidateWalk` has already refused that, and
// `walks_name_present_ck` is the guarantee under it.
//
// The client trims and its gate is the trimmed string, so a server storing the
// untrimmed one would put a different name in the log from the one the sheet
// approved.
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
//
// THE PAIRING IS BY POSITION AND THAT IS SAFE HERE BECAUSE BOTH SLICES ARE
// BUILT IN ONE LOOP. `unnest` over arrays of unequal length pads the shorter
// with NULLs, which `jsonb_build_object` would then write as a null `lat` —
// so the invariant is worth naming even though nothing can currently break it.
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
// different sentence, and it is a second function for that function's own
// reason: `walks_city_fk` is RESTRICT (DEC-57) and would answer a 500 with
// nothing on it, and what a client can act on is a sentence about ITS request.
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
//
// `recorded_on` IS SCANNED INTO A time.Time DIRECTLY AND NOT INTO A
// sql.NullTime (DEC-102). The column is NOT NULL, so the driver erroring on a
// NULL is the right failure — a `sql.NullTime` read unconditionally would let
// the emitter invent `0001-01-01T00:00:00.000Z`, which `DateTime.parse`
// accepts happily and every screen renders as a year-1 date.
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
//
// IT IS A Scanner AND NOT A pgtype IMPORT, and that distinction is the whole
// of it: `cmd/api/imports_test.go` forbids anything under `jackc/pgx` outside
// `cmd/api/main.go`, and R6 measured that pgx's stdlib Conn accepts a Go slice
// as an ARGUMENT through `driver.NamedValueChecker` — which is the outbound
// direction and imports nothing. The inbound direction has no such hook: a
// float8[] arrives as `[]byte` holding PostgreSQL's own array literal, so
// something has to read it.
//
// THE FORMAT IS `{1.5,2.5}` AND AN EMPTY ARRAY IS `{}`, which is all this
// parses. It is deliberately not a general array reader — there are no quoted
// elements to unescape and no NULLs to represent, because the only producer is
// the LATERAL above, over a jsonb array whose elements 0003 guarantees exist.
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
