// The public read's half of the storage contract: a token digest resolved
// without a traveller, and THE THREE ROW RULES (PD-07,
// docs/PUBLIC-ENVELOPE.md §5).
//
// IT IS ITS OWN FILE FOR THE REASON share_store.go IS ONE. Every statement
// here is reached by a request that carried NO CREDENTIAL, so "which rows can
// a stranger see" is a question somebody can answer by reading one file — and
// it stops being one the moment a public read lands beside the private one in
// logbook_store.go.
//
// THE ROW RULES ARE HERE AND NOT IN internal/logbook, AND THAT IS THE FINDING
// RATHER THAN A LAYERING PREFERENCE. PD-07's leak is not a key that should
// have been dropped: every key in the leaking document is on the allowlist.
// It is a place accumulating visits across trips BY DESIGN — which is what
// makes 'Third visit' and P1's year rows possible — so a place correctly
// published because it is on the shared trip drags every other trip's history
// in with it. Measured on the client's own fixture: `fushimi-inari` holds 28
// visits across 4 trips, exactly ONE of them on `autumn-crossing`, and one of
// the other 27 carries a note. `shareNotes` is true on that trip, so the note
// filter does not save it. A filter written where the KEYS are filtered cannot
// see this; a WHERE clause can.
//
// WHAT THIS FILE DELIBERATELY DOES NOT READ, none of which is on the
// allowlist: `places.plan`, `places.cover_asset`, `cities.cover_asset`,
// `walks.name`, `photos.visit_id`, `photos.filed_later`, and the traveller's
// name. A column that is never selected is a column that cannot be published
// by an emitter that grew a field.
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
//
// A THIRD TYPE OVER THE SAME POOL, on ShareStore's precedent: all of them are
// `struct{ DB *sql.DB }`, so it costs nothing at wiring time, and what it buys
// is that the interface a handler is given says what that handler can reach.
type ShareReadStore struct{ DB *sql.DB }

// shareLinkByHashSQL has NO `revoked_at IS NULL` AND NO TRAVELLER, and both
// absences are decisions.
//
// NO TRAVELLER, because there is none: the request arrives with no credential
// at all and the traveller comes OUT of this row. `share_links_token_key` is a
// GLOBAL unique index on the digest (0004), which is what makes that one
// indexed lookup rather than a scan.
//
// NO REVOCATION FILTER, because THE LOOKUP IS THE ONLY BRANCH (PD-12). Adding
// `AND revoked_at IS NULL` here would be correct, would answer the same 404,
// and would make the equal-work requirement unarrangeable: the caller could no
// longer tell the two cases apart in order to treat them the same, and a
// handler that resolved a trip for one and not the other would be
// byte-identical and still an oracle for "this token was once real".
//
// IT COMPARES A DIGEST AND NOT A TOKEN (DEC-85). The plaintext exists in this
// process for the length of one hash call and is written nowhere; a dump holds
// no capability, live or revoked.
const shareLinkByHashSQL = `SELECT traveller_id::text, trip_id, revoked_at IS NOT NULL
	FROM share_links WHERE token_hash = $1`

// publicTripSQL reads the trip and the three switches together, because the
// switches decide what the rest of the read may publish.
//
// `shared` IS NOT SELECTED. DEC-91's boolean exists so the OWNER's device can
// find its way back to 'Stop sharing'; a stranger reading the trip is already
// holding the answer.
const publicTripSQL = `SELECT id, name, started_on, ended_on, summary, cover_asset,
		share_photos, share_notes, share_coordinates
	FROM trips WHERE traveller_id = $1::uuid AND id = $2`

// publicCitiesSQL is ORDERED BY `trip_cities.ordinal` AND NOT BY id.
//
// Travel order is load-bearing on the wire and `ordinal` is the only thing
// that carries it. The private read orders cities by id on purpose — two reads
// with no write between them have to be byte-identical or the ETag is a claim
// the server cannot keep — and puts travel order back through the trip's own
// `cityIds`. Here there is one trip, so the list IS the itinerary.
const publicCitiesSQL = `SELECT c.id, c.name, c.country_code, c.country_name,
		c.centre_lat, c.centre_lng
	FROM trip_cities tc
	JOIN cities c ON (c.traveller_id, c.id) = (tc.traveller_id, tc.city_id)
	WHERE tc.traveller_id = $1::uuid AND tc.trip_id = $2
	ORDER BY tc.ordinal`

// publicPlacesSQL is RULE 1: the distinct places having a visit ON THE SHARED
// TRIP.
//
// NOT "every place in the trip's cities", which is the rule that looks right
// and publishes a wishlist. Measured on the client's own fixture: 5 places
// have an `autumn-crossing` visit and 8 sit in `autumn-crossing`'s five
// cities, so the city-scoped rule publishes THREE PINS THE TRIP NEVER WENT TO
// — places somebody wrote down because they might go one day.
//
// AN EXISTS RATHER THAN A JOIN, because a place with four visits on the trip
// is one place. A `JOIN visits` would publish it four times and a DISTINCT
// would then be load-bearing without saying so.
const publicPlacesSQL = `SELECT p.id, p.city_id, p.name, p.lat, p.lng
	FROM places p
	WHERE p.traveller_id = $1::uuid
	  AND EXISTS (SELECT 1 FROM visits v
	              WHERE v.traveller_id = p.traveller_id
	                AND v.place_id = p.id
	                AND v.trip_id = $2)
	ORDER BY p.id`

// publicVisitsSQL is RULE 2, and it is the one PD-07 is about: only the
// published place's visits whose `trip_id` is the shared trip.
//
// THE ORDER IS THE PRIVATE DOCUMENT'S, `ordinal`, AND THAT IS DELIBERATE. The
// client reads the first entry's `at` as when the place was last visited, so
// the order is a fact the reader acts on rather than presentation — and
// re-deriving it here from `at DESC` would be a SECOND rule about the same
// thing, disagreeing with the private read the day two visits share an
// instant. The fixture has exactly that: four occasions at ONE instant.
const publicVisitsSQL = `SELECT place_id, at, note
	FROM visits
	WHERE traveller_id = $1::uuid AND trip_id = $2
	ORDER BY place_id, ordinal, id`

// publicPhotosSQL is RULE 3. `visit_id`, `filed_later` and `trip_id` are not
// selected: none is on the allowlist.
const publicPhotosSQL = `SELECT id, city_id, place_id, taken_at, asset, caption,
		lat, lng, accuracy_metres
	FROM photos
	WHERE traveller_id = $1::uuid AND trip_id = $2
	ORDER BY id`

// publicWalksSQL is RULE 3 for walks, plus the one thing that is not a trip
// id: `NOT dismissed`.
//
// N1's 'Discard' is an action the owner took to be rid of a track. Publishing
// it through a link would publish the thing they got rid of — which is the
// same argument D2's sheet makes when it promises the track survives both
// branches, read the other way round.
const publicWalksSQL = `SELECT id, city_id, recorded_on, distance_km
	FROM walks
	WHERE traveller_id = $1::uuid AND trip_id = $2 AND NOT dismissed
	ORDER BY id`

// publicWalkPointsSQL is readWalkPointsSQL scoped to one trip, and it carries
// the SAME `NOT dismissed` as the walks query above.
//
// THE REPEATED PREDICATE IS NOT REDUNDANT: without it the points of a
// discarded track are read out of the database and then dropped in Go because
// no walk claims them, which is a shape somebody later "optimises" into a
// leak. What is never selected cannot be published.
const publicWalkPointsSQL = `SELECT w.id,
		(pt.value->>'lat')::double precision,
		(pt.value->>'lng')::double precision
	FROM walks w
	CROSS JOIN LATERAL jsonb_array_elements(w.points) WITH ORDINALITY AS pt(value, ord)
	WHERE w.traveller_id = $1::uuid AND w.trip_id = $2 AND NOT w.dismissed
	ORDER BY w.id, pt.ord`

// ShareLink resolves a digest, revoked or not. See shareLinkByHashSQL.
func (s ShareReadStore) ShareLink(ctx context.Context, tokenHash []byte) (logbook.ShareLink, error) {
	var link logbook.ShareLink
	err := s.DB.QueryRowContext(ctx, shareLinkByHashSQL, tokenHash).
		Scan(&link.TravellerID, &link.TripID, &link.Revoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// THE DIGEST IS NOT IN THE ERROR. It is not the capability — the
		// plaintext is — but it is a value that identifies one, and this error
		// reaches a log line.
		return logbook.ShareLink{}, logbook.ErrNoShare
	case err != nil:
		return logbook.ShareLink{}, fmt.Errorf("postgres: resolving a share link: %w", err)
	}
	return link, nil
}

// PublicLog reads the six things the envelope may carry, inside ONE
// repeatable-read snapshot.
//
// THE SNAPSHOT IS DEC-06's AND THE ARGUMENT IS THE PRIVATE READ'S. Six
// statements under READ COMMITTED each see a newer database than the last, so
// a write landing mid-read is in the photographs and not in the places — and a
// public page is a document somebody screenshots and sends on. There is no
// version and no ETag here, so nothing self-corrects later either.
//
// AN UNKNOWN TRIP IS ErrNoTrip, and it is reachable: `share_links_trip_fk` is
// ON DELETE CASCADE, so D3 takes the link with the trip — but a link resolved
// a moment before that commit is a token in a handler's hand naming a trip
// that has gone.
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

// readPublicCities answers the cities AND the id list, from one query, because
// they are the same fact in the same order and reading them twice is how two
// orders come to disagree.
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
		// `at` is NOT NULL and is scanned as such (DEC-102): a NULL scanned
		// through sql.NullTime with .Valid unchecked becomes the zero time,
		// which renders as a year-1 date that DateTime.parse accepts happily.
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
