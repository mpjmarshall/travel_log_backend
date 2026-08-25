// C1's pin, D2's removal, and the ONE ordered child collection in this schema
// that something else references.
//
// THE VISITS WRITE IS AN UPSERT AND NEVER A DELETE-THEN-INSERT (PD-06), AND
// FOUR DOCUMENTS SAID OTHERWISE. 0001's own header mandates delete-then-insert
// for a reorder in capitals, `replaceTripCities` is named as the pattern to
// copy, a prior DBA pass wrote "the same strategy applies and the same proof
// covers it" about visits specifically, and v7.0's leg list called this "the
// delete-then-insert reorder". All four are wrong about THIS table and right
// about `trip_cities`, and the difference is one foreign key:
//
//	trip_cities  has NO referencing child.  Delete-then-insert is safe.
//	visits       has photos_visit_fk … ON DELETE SET NULL (visit_id).
//
// MEASURED ON THIS PROJECT'S OWN postgres:17.11, against the client's real
// fixture at `fushimi-inari` — DELETE 28 rows and re-INSERT the SAME 28 rows,
// byte for byte, inside ONE transaction:
//
//	visits at the place        28 -> 28    (no count moved)
//	photographs still filed    30 ->  0
//	photographs naming a place with no occasion   0 -> 30
//	the dangling-reference check                  0 ->  0
//
// A NO-OP RE-SEND OF AN UNCHANGED ARRAY DESTROYS THE FILING, silently, with
// `DELETE 28 / INSERT 0 28` as the only trace — and the client does exactly
// that whenever a screen re-saves a place it did not change. Re-inserting the
// same id does NOT restore the reference: the FK fired on the DELETE.
//
// THE OFFSET GOES UPWARD AND THAT IS NOT A PREFERENCE. Measured on the same
// database, against 0001's own constraints:
//
//	ordinal - 1000   ERROR: new row for relation "visits" violates check
//	                 constraint "visits_ordinal_ck"
//	1 - ordinal      ERROR: duplicate key value violates unique constraint
//	                 "visits_place_ordinal_uq"   (a UNIQUE index is checked
//	                 per ROW during a statement, even when the final state is
//	                 unique)
//	ordinal + 28     OK, and every one of the 30 photographs is still filed
//
// Making `visits_place_ordinal_uq` DEFERRABLE was declined for 0001's own
// stated reason: it moves the 422 to `tx.Commit()`.
//
// AND THE OFFSET IS DERIVED RATHER THAN THE CONSTANT THE PLAN NAMES — see
// offsetVisitOrdinalsSQL, which is where the arithmetic is.
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

// maxVisitsPerStatement bounds one multi-row INSERT, and the number is
// arithmetic rather than taste (PERF-MIN-8, DB-MIN-15).
//
// PostgreSQL's wire protocol counts bind parameters in an int16, so the
// ceiling is 65,535 per statement. This INSERT spends 2 on the traveller and
// the place — both constant across the whole array — and 5 per row: the visit
// id, its trip, its ordinal, its instant and its note. So the true ceiling is
// (65535 - 2) / 5 = 13,106 rows, and 5,000 leaves it a factor of two and a
// half of room while still making the fixture's largest place — 28 visits at
// `fushimi-inari` — a single statement.
//
// IT IS A BATCH SIZE AND NOT A CAP ON THE ARRAY. Nothing here refuses a long
// visits array; a place visited every week for ten years is 520 occasions and
// is a log somebody could really have. What is refused is the assumption that
// one statement always fits, which is the assumption that breaks silently.
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
// two questions readTripForWriteSQL answers for trips: whether this is a
// CREATE — which decides whether an absent `name`, `cityId` or `coordinates`
// is legal — and what the proposed INSERT tuple must carry so that four NOT
// NULL columns and two CHECKs are satisfied before the conflict is resolved.
const readPlaceForWriteSQL = `SELECT city_id, name, lat, lng FROM places
	WHERE traveller_id = $1::uuid AND id = $2`

const readOnePlaceSQL = `SELECT id, city_id, name, lat, lng, plan, cover_asset
	FROM places WHERE traveller_id = $1::uuid AND id = $2`

// readOnePlacesVisitsSQL is `ORDER BY ordinal, id` for DEC-26's reason: with a
// duplicate ordinal the plain form is non-deterministic, and emitting the
// visits in a different order silently rebinds a photograph to a different
// occasion. The tiebreak degrades that to stable rather than random.
//
// NEWEST FIRST IS ORDINAL 0, because the client reads `visits.first.at` as
// `lastVisited` and its own model comment says "Newest first. Empty means
// wishlist."
const readOnePlacesVisitsSQL = `SELECT id, place_id, trip_id, at, note FROM visits
	WHERE traveller_id = $1::uuid AND place_id = $2 ORDER BY ordinal, id`

// tripsPresentSQL and visitsHeldElsewhereSQL are ONE STATEMENT EACH FOR THE
// WHOLE ARRAY (PERF-MIN-8), where PutTrip does one round trip per city.
//
// That is irrelevant for a trip — five cities at worst in the fixture — and is
// not for a place: `fushimi-inari` already holds 28 visits and a real ten-year
// log would hold more, so a per-row existence check is 28 round trips inside
// the advisory lock, on every save of a place nobody changed.
//
// `= ANY($2)` TAKES A PLAIN `[]string` AND IMPORTS NOTHING. Measured at this
// commit against this module's own driver: `[]string`, `[]*string`, `[]int64`,
// `[]float64`, `[]bool`, `[]time.Time` and `[]sql.NullString` all reach
// PostgreSQL as arrays through `database/sql` alone. pgx's stdlib Conn
// implements `driver.NamedValueChecker`, so it takes a Go slice and converts
// it through its own type map — the CALL SITE imports no `pgtype` and
// cmd/api/imports_test.go's monopoly is untouched. See CLAUDE.md, where R4's
// sentence about this is corrected.
const tripsPresentSQL = `SELECT id FROM trips WHERE traveller_id = $1::uuid AND id = ANY($2)`

const visitsHeldElsewhereSQL = `SELECT id, place_id FROM visits
	WHERE traveller_id = $1::uuid AND id = ANY($2) AND place_id <> $3`

// occupiedDepartingVisitsSQL is the guard that keeps the pair coherent, and it
// is R6's own addition rather than something the plan names — see PutPlace.
const occupiedDepartingVisitsSQL = `SELECT v.id, count(*) FROM visits v
	JOIN photos p ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
	WHERE v.traveller_id = $1::uuid AND v.place_id = $2 AND v.id <> ALL($3)
	GROUP BY v.id ORDER BY v.id`

// offsetVisitOrdinalsSQL parks every stored ordinal above every ordinal the
// incoming array will use, in ONE statement, so the upsert that follows cannot
// collide with a row it has not reached yet.
//
// THE OFFSET IS DERIVED AND THE PLAN'S `+ 1000` IS A CONSTANT, and the
// difference is not style. `+ 1000` is correct for every place with fewer than
// a thousand visits and silently wrong above it: park {0..1000} at {1000..2000}
// and the row moving 0 -> 1000 collides with the row still sitting at 1000.
// `GREATEST($3, max(ordinal) + 1)` has no such number in it —
//
//	max(ordinal) + 1  puts the parked set entirely ABOVE the stored set, so
//	                  no per-row collision is possible in any order, which is
//	                  the property `1 - ordinal` does not have;
//	$3, the incoming length, puts it entirely above the ordinals the INSERT is
//	                  about to write, which is the property `+ 1000` loses on
//	                  a long array.
//
// THE SUBQUERY IS EVALUATED ONCE AND IT IS SAFE THAT IT READS THE TABLE IT IS
// UPDATING. It is uncorrelated, so the planner makes it an InitPlan; and even
// re-evaluated it would answer the same, because a statement's own changes are
// invisible to its own subqueries — the rows this UPDATE writes carry its
// command id and are filtered out. GREATEST ignores a NULL, so a place with no
// stored visits needs no branch: the WHERE matches nothing and the UPDATE
// touches nothing.
const offsetVisitOrdinalsSQL = `UPDATE visits SET ordinal = ordinal + GREATEST(
		$3::int,
		(SELECT max(ordinal) + 1 FROM visits WHERE traveller_id = $1::uuid AND place_id = $2))
	WHERE traveller_id = $1::uuid AND place_id = $2`

// deleteDepartedVisitsSQL removes only what the incoming array left out.
//
// IT IS NEVER REACHED WITH AN EMPTY ARRAY, and that is the whole of SAF-MAJ-4.
// `id <> ALL('{}')` is true of every row, so an empty incoming array makes
// this statement `DELETE FROM visits WHERE place_id = $2` — which is exactly
// the delete-then-insert this file exists to avoid, with the fix in place.
// `ValidatePlace` refuses `visits: []` with a 422 before a transaction is
// opened, and an ABSENT key never enters this function at all.
const deleteDepartedVisitsSQL = `DELETE FROM visits
	WHERE traveller_id = $1::uuid AND place_id = $2 AND id <> ALL($3)`

// PutPlace is C1's pin and every later write to a place, inside
// WithTravellerTx: one transaction, the traveller's advisory lock, and the
// version bump taken before the body runs.
//
// THE ANSWER IS RE-READ FROM THE ROWS AND NOT ASSEMBLED FROM THE REQUEST, for
// PutTrip's reason: a response built from the input is a response that agrees
// with the client about a write the database may have shaped differently — and
// here the ordinals are assigned by this function rather than sent, so the
// order that comes back is the order that is stored.
//
// ONE REFUSAL IN HERE IS NOT IN THE PLAN AND IS ARGUED RATHER THAN ASSUMED.
// The mandated shape ends "DELETE only the ids absent from the incoming
// array", and a visit deleted that way takes `photos.visit_id` with it through
// `photos_visit_fk ON DELETE SET NULL (visit_id)` — leaving a photograph that
// names a place with no occasion, which is a state the client's model has
// never expressed (measured across all 284 fixture photographs: 95 carry both,
// 189 carry neither, place-only 0, visit-only 0). That is SAF-MAJ-4's hazard
// at row granularity: `visits: []` is simply the n-row case of it. So a visits
// array that drops an occasion STILL HOLDING PHOTOGRAPHS is refused with a 422
// naming the field, and an occasion with none may go freely. Both refusals
// stand for their own reason — the empty array is refused even when nothing is
// filed there, because clearing a place's whole history is a destruction no
// sheet authorises. TRIGGER FOR REVISITING: a control that removes one
// occasion, at which point the sheet copy is written first and this follows
// it.
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

		// ABSENT MEANS LEAVE ALONE, AND THIS IS THE BRANCH THE WHOLE STEP IS
		// ABOUT (DEC-89). A nil Visits skips every statement below, so C1's
		// pin — a wishlist place with no visits at all — is correct by
		// construction and an accidental re-PUT of any place is harmless.
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

// writeVisits is the four phases, in the order that preserves the filing.
//
// ONE STATEMENT PER PHASE AND NOT ONE PER ROW, except the INSERT, which is one
// per BATCH — see maxVisitsPerStatement.
func writeVisits(ctx context.Context, tx *sql.Tx, travellerID, placeID string, visits []logbook.Visit) error {
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

// upsertVisits writes one batch as a single multi-row INSERT.
//
// `DO UPDATE SET place_id = EXCLUDED.place_id` IS DELIBERATE AND IS NOT WHAT
// GUARDS THE PLACE. refuseVisitsHeldElsewhere has already refused any id this
// traveller holds under another place, so on this line EXCLUDED.place_id is
// always the value the row already carries. It is named anyway because the
// statement's meaning is "these rows ARE this place's visits, in this order",
// and a SET clause that quietly omits a column is how `PUT /v1/trips/{id}`
// came to destroy an itinerary.
//
// `ordinal` IS THE ARRAY POSITION AND `firstOrdinal` IS THE BATCH'S OFFSET
// INTO IT, so batching cannot renumber the second batch from zero — which
// would collide with the first through visits_place_ordinal_uq and would only
// show up on an array longer than the batch size.
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

// requireCityForPlace is `requireCities` with the OTHER FIELD NAME, and the
// difference is the whole reason it is a second function.
//
// `places_city_fk` is RESTRICT (DEC-57) and would answer a 500 with nothing on
// it. What the client can act on is which of ITS OWN fields is wrong, and a
// place write's field is `cityId` where a trip write's is `cityIds` — so
// reusing the trip helper would tell a client to fix a key its request never
// carried.
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
	// The ORDER of the refusal is the order of the ARRAY rather than of the
	// answer, so the same body always names the same trip.
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
//
// `visits_pkey` IS (traveller_id, id) AND NOT (traveller_id, place_id, id), so
// an upsert naming a visit that belongs somewhere else would silently MOVE it
// — taking its `at`, its note and every photograph filed to it to a place the
// user never mentioned. Nothing in the client asks for that: M2.2's re-file is
// R7's, it moves a PHOTOGRAPH between pins, and it does it through its own
// route.
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
// through the one route that could put them out of step (DEC-83, PD-13).
//
// See PutPlace for the argument. In short: a dropped visit clears
// `photos.visit_id` and leaves `photos.place_id` standing, which is the
// half-filed state the whole log has never held, and the three standing guards
// are all blind to it — the reference is gone rather than dangling, there is
// still a place, and a pair check on the OTHER route sees two NULLs that
// agree.
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

// requireWritablePlace refuses a CREATE missing a NOT NULL field and names it.
//
// `coordinates` IS REQUIRED ON A CREATE AND THE CLIENT ALWAYS HAS ONE. C1's
// pin defaults to the city's own centre when the user has not moved it
// (`logbook.dart:390`: `coordinates ?? city.centre`), so "the client cannot
// supply it" is not a case — and `places.lat` and `places.lng` are NOT NULL,
// so a server-side default would be this build inventing a position and
// drawing a pin at it.
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

// ---------------------------------------------------- D2: THE TWO BRANCHES

// deletePlacesPhotosSQL and deletePlaceSQL ARE TWO LINES WHOSE ORDER SILENTLY
// INVERTS D2'S PROMISE, and that order is the whole of the delete branch.
//
// D2's own words on the destructive row are "all N, and the notes you wrote on
// them". Delete the PLACE first and `photos_place_fk … ON DELETE SET NULL
// (place_id)` clears `place_id` on every one of those photographs — so the
// DELETE that follows matches nothing, the photographs survive with the user
// having explicitly asked for them to go, and there is no error anywhere.
//
// ASSERT ON THE SURVIVING ROW COUNT, NEVER ON ERROR/NO-ERROR. Before DEC-66's
// column-list SET NULL the wrong order raised a NOT NULL violation instead, so
// a leg written against an error would have reddened for the wrong reason,
// which DEC-28 forbids.
const deletePlacesPhotosSQL = `DELETE FROM photos WHERE traveller_id = $1::uuid AND place_id = $2`

// deletePlaceSQL is the rest of D2, and everything else the sheet promises is
// the schema's rather than Go's:
//
//	"the visits go with the pin"          -> visits_place_fk  ON DELETE CASCADE
//	keep: "they lose the pin but keep
//	       their date and city"           -> photos_place_fk  ON DELETE SET NULL
//	                                         (place_id), and the visits
//	                                         cascading takes visit_id with them
//	                                         through photos_visit_fk
//	"the track stays with the day it was
//	 recorded either way"                 -> NOTHING. `walks` has no place_id
//	                                         at all, and that absence IS the
//	                                         promise.
//
// THE KEEP BRANCH IS THEREFORE ONE STATEMENT AND NO GO AT ALL, and it produces
// exactly `Photo.copyWith(clearPlace: true)`: both columns NULL, `taken_at`,
// `city_id` and `caption` untouched. A Go implementation that cleared only
// `place_id` would pass a pair-agreement check written on the other column.
const deletePlaceSQL = `DELETE FROM places WHERE traveller_id = $1::uuid AND id = $2`

// errNoSuchPlace is how a miss ROLLS THE VERSION BUMP BACK — the same device
// errNothingDeleted is for D3, and a separate sentinel because the two are
// caught in different functions and a shared one would be caught by the wrong
// caller.
var errNoSuchPlace = errors.New("postgres: that place was not in this log")

// RemovePlace is D2, and it answers THE WHOLE LOG.
//
// A MISS IS A SUCCESS AND MOVES NOTHING, exactly as it is on DeleteTrip: the
// client's `removePlace` answers true for an id the log does not hold, and a
// bump on a retried delete throws away the phone's whole cached document.
//
// THE ORDER OF THE TWO STATEMENTS IS THE FEATURE. See deletePlacesPhotosSQL.
func (s PlaceStore) RemovePlace(ctx context.Context, travellerID, placeID string, deletePhotos bool) (logbook.Snapshot, error) {
	var snap logbook.Snapshot

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if deletePhotos {
			// FIRST, AND THAT IS NOT A PREFERENCE. Reverse these two and the
			// photographs the user asked to delete survive, unpinned, with no
			// error and no count that moves except `places`.
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
		// Nothing was written — INCLUDING the photographs, because the whole
		// body rides one transaction's rollback. So a `?photos=delete` against
		// an id this log does not hold cannot take a photograph with it, which
		// is the failure a two-transaction implementation would have.
		return s.Read(ctx, travellerID, func(int64) bool { return true })
	case err != nil:
		return logbook.Snapshot{}, travellerError(err, travellerID)
	}
	snap.Version = version
	return snap, nil
}

// Read is here so a PlaceStore can answer the log a miss should see, and it is
// LogbookStore's own read rather than a second implementation of it.
func (s PlaceStore) Read(ctx context.Context, travellerID string, assemble func(int64) bool) (logbook.Snapshot, error) {
	return LogbookStore{DB: s.DB}.Read(ctx, travellerID, assemble)
}
