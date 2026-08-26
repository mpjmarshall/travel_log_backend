// THE PUBLIC ENVELOPE AT FIXTURE SCALE, against the client's own log.
//
// WHY THIS FILE EXISTS BESIDE internal/postgres/share_read_test.go, which
// tests the same store. That file's fixture is three places and two trips,
// built to hold the three leaking shapes; every number in it was chosen by
// whoever wrote it. THIS one is the log the client actually encoded, and the
// numbers were MEASURED rather than chosen:
//
//	fushimi-inari       28 visits across 4 trips, exactly 1 on the shared one
//	                    — and 1 of the other 27 carries a NOTE, on a trip whose
//	                    shareNotes is true, so the note filter does not save it
//	autumn-crossing     5 places have a visit on it; 8 places sit in its five
//	                    cities, so the city-scoped rule publishes 3 pins the
//	                    trip never went to, one of them a wishlist
//	                    (`tofuku-ji`, zero visits)
//	                    96 photographs, 31 of them carrying a coordinate
//
// THE 28-VERSUS-1 IS THE WHOLE ARGUMENT. A place accumulates visits across
// trips by design — it is what makes 'Third visit' and P1's year rows possible
// — so a place published because of ONE visit drags twenty-seven others in
// with it, with their dates and their notes. Every key in that document is on
// the allowlist and a key-set walk passes.
//
// THE SEEDED LOG'S ONLY LIVE LINK IS `kyoto-9f2a`, WHICH IS NOT A TOKEN THIS
// SERVER WOULD MINT: ten characters with a hyphen, against
// `^[a-z0-9]{12,64}$`. It is what the client's own encoder wrote and what
// `make seed` hashes into `share_links`, and it is the reason the public read
// does not validate the token it is handed — see internal/httpapi's
// publicShare.
package seed_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"travellog/internal/logbook"
	"travellog/internal/media"
	"travellog/internal/postgres"
)

const (
	sharedTrip  = "autumn-crossing"
	sharedToken = "kyoto-9f2a"

	// MEASURED ON THE CLIENT'S OWN FIXTURE, and every one is asserted before
	// it is relied on. A number that quietly stopped holding would make the
	// leg below pass while proving nothing.
	fixtureFushimiVisits    = 28
	fixtureFushimiOnTrip    = 1
	fixturePlacesOnTrip     = 5
	fixturePlacesInCities   = 8
	fixturePhotosOnTrip     = 96
	fixturePhotoCoordinates = 31
	fixtureCitiesOnTrip     = 5
)

// publishedBySeed reads the seeded log the way `GET /l/{token}` does: resolve
// the digest, then read the rows.
func publishedBySeed(t *testing.T) (*sql.DB, logbook.PublicSource) {
	t.Helper()
	db, _, _ := loaded(t)
	store := postgres.ShareReadStore{DB: db}

	link, err := store.ShareLink(context.Background(), logbook.HashShareToken(sharedToken))
	if err != nil {
		t.Fatalf("ShareLink(%q): %v — the seeded log holds exactly one live link and "+
			"this is its token", sharedToken, err)
	}
	if link.TripID != sharedTrip {
		t.Fatalf("the seeded link resolves to %q, want %q", link.TripID, sharedTrip)
	}
	if link.Revoked {
		t.Fatal("the seeded link is revoked")
	}

	src, err := store.PublicLog(context.Background(), link.TravellerID, link.TripID)
	if err != nil {
		t.Fatalf("PublicLog: %v", err)
	}
	return db, src
}

// THE PLAN'S FIRST NAMED FAILING TEST, at the scale it was written against.
func TestTheSeededEnvelopeCarriesOnlyTheSharedTripsOwnPlaces(t *testing.T) {
	db, src := publishedBySeed(t)

	// THE FIXTURE, ASSERTED FIRST.
	if got := rows(t, db, `SELECT count(*) FROM visits WHERE place_id='tofuku-ji'`); got != 0 {
		t.Fatalf("tofuku-ji has %d visits; this leg needs a wishlist place", got)
	}
	if got := rows(t, db,
		`SELECT count(DISTINCT place_id) FROM visits WHERE trip_id=$1`, sharedTrip); got != fixturePlacesOnTrip {
		t.Fatalf("%d places have a visit on %s, want %d", got, sharedTrip, fixturePlacesOnTrip)
	}
	if got := rows(t, db, `SELECT count(*) FROM places WHERE city_id IN
		(SELECT city_id FROM trip_cities WHERE trip_id=$1)`, sharedTrip); got != fixturePlacesInCities {
		t.Fatalf("%d places sit in %s's cities, want %d — this leg needs the two counts "+
			"to differ, or the city-scoped rule and the visit-scoped one agree",
			got, sharedTrip, fixturePlacesInCities)
	}

	ids := map[string]bool{}
	for _, place := range src.Places {
		ids[place.ID] = true
	}
	if ids["tofuku-ji"] {
		t.Error("the public envelope carries tofuku-ji, a WISHLIST place — somewhere " +
			"the traveller has never been, published by a link shared for a trip. " +
			"Every key on it is on the allowlist; the row is the leak.")
	}
	if len(src.Places) != fixturePlacesOnTrip {
		t.Errorf("the envelope carries %d places, want %d — the trip's five cities hold "+
			"%d, so three of them are pins this trip never went to",
			len(src.Places), fixturePlacesOnTrip, fixturePlacesInCities)
	}
	if len(src.Places) == 0 {
		t.Fatal("no places at all — a filter that removes everything satisfies the " +
			"assertions above and is not what the sheet promises")
	}
}

// THE PLAN'S SECOND NAMED FAILING TEST: the nested rows the first one cannot
// see (PD-07).
func TestASeededPublishedPlaceCarriesOnlyTheSharedTripsVisits(t *testing.T) {
	db, src := publishedBySeed(t)

	all := rows(t, db, `SELECT count(*) FROM visits WHERE place_id='fushimi-inari'`)
	mine := rows(t, db,
		`SELECT count(*) FROM visits WHERE place_id='fushimi-inari' AND trip_id=$1`, sharedTrip)
	noted := rows(t, db, `SELECT count(*) FROM visits
		WHERE place_id='fushimi-inari' AND trip_id<>$1 AND note IS NOT NULL`, sharedTrip)
	if all != fixtureFushimiVisits || mine != fixtureFushimiOnTrip {
		t.Fatalf("fushimi-inari has %d visits, %d on the shared trip; this leg needs "+
			"many-across-trips and few-on-this-one (%d and %d)",
			all, mine, fixtureFushimiVisits, fixtureFushimiOnTrip)
	}
	if noted == 0 {
		t.Fatalf("no visit of fushimi-inari on ANOTHER trip carries a note, so the " +
			"leak this leg is about would be invisible even under the defect — and " +
			"shareNotes is true on the shared trip, so the note filter does not save it")
	}

	var published *logbook.Place
	for i := range src.Places {
		if src.Places[i].ID == "fushimi-inari" {
			published = &src.Places[i]
		}
	}
	if published == nil {
		t.Fatal("fushimi-inari is not published at all; this leg needs the place that IS")
	}
	if len(published.Visits) != mine {
		t.Errorf("fushimi-inari published with %d visits, want %d — a link shared for "+
			"one trip is publishing %d visits belonging to three others, with their "+
			"dates and their notes", len(published.Visits), mine, all-mine)
	}
	for _, visit := range published.Visits {
		if visit.TripID != sharedTrip {
			t.Errorf("published visit %s belongs to %s", visit.ID, visit.TripID)
		}
	}
}

// THE ROW RULES FOR PHOTOGRAPHS, WALKS AND CITIES, at fixture scale.
func TestTheSeededEnvelopeCarriesOneTripsPhotographsWalksAndCities(t *testing.T) {
	db, src := publishedBySeed(t)

	if got := rows(t, db, `SELECT count(*) FROM photos WHERE trip_id=$1`, sharedTrip); got != fixturePhotosOnTrip {
		t.Fatalf("%d photographs on %s, want %d", got, sharedTrip, fixturePhotosOnTrip)
	}
	if len(src.Photos) != fixturePhotosOnTrip {
		t.Errorf("the envelope carries %d photographs, want %d of %d in the whole log",
			len(src.Photos), fixturePhotosOnTrip, rows(t, db, `SELECT count(*) FROM photos`))
	}
	for _, photo := range src.Photos {
		if photo.TripID != sharedTrip {
			t.Errorf("photo %s belongs to %s", photo.ID, photo.TripID)
		}
	}

	if len(src.Cities) != fixtureCitiesOnTrip {
		t.Errorf("the envelope carries %d cities, want %d — the trip's own itinerary, "+
			"in trip_cities.ordinal order, out of %d in the log",
			len(src.Cities), fixtureCitiesOnTrip, rows(t, db, `SELECT count(*) FROM cities`))
	}
	// TRAVEL ORDER, AGAINST THE ITINERARY ROW AND NOT AGAINST A SORT. The
	// client's own encoder wrote kyoto first and the store's private read
	// orders cities by id, so the two disagree the moment a trip visits a city
	// whose id sorts earlier.
	for i, city := range src.Cities {
		want := rows(t, db, `SELECT count(*) FROM trip_cities
			WHERE trip_id=$1 AND city_id=$2 AND ordinal=$3`, sharedTrip, city.ID, i)
		if want != 1 {
			t.Errorf("cities[%d] is %s, which is not the city at ordinal %d", i, city.ID, i)
		}
	}

	for _, walk := range src.Walks {
		if walk.TripID != sharedTrip {
			t.Errorf("walk %s belongs to %s", walk.ID, walk.TripID)
		}
		if walk.Dismissed {
			t.Errorf("walk %s is dismissed and is published anyway", walk.ID)
		}
	}
	if len(src.Walks) == 0 {
		t.Error("the envelope carries no walks at all, so nothing here is measured")
	}
}

// AND THE WHOLE ENVELOPE, THROUGH THE EMITTER, AT FIXTURE SCALE — which is
// where DEC-108's measurement lives.
//
// 31 OF 96 PHOTOGRAPHS CARRY A COORDINATE, so a photograph's coordinate is a
// MOVEMENT TRACE — where the traveller stood, to metres, with a timestamp
// beside it — rather than a set of places they chose to pin. DEC-108 ruled
// that ONE switch governs both, because H1 says 'share coordinates' and the
// user reading it means all of them; a control that silently governs less than
// its label says is the same defect as D2's subtitle promising more than the
// model could keep. This leg is that ruling at the scale the measurement was
// taken.
func TestTheSeededEnvelopeHonoursOneSwitchForPinsAndTraces(t *testing.T) {
	db, src := publishedBySeed(t)

	if got := rows(t, db, `SELECT count(*) FROM photos WHERE trip_id=$1 AND lat IS NOT NULL`,
		sharedTrip); got != fixturePhotoCoordinates {
		t.Fatalf("%d of the trip's photographs carry a coordinate, want %d — the "+
			"measurement DEC-108 was decided on", got, fixturePhotoCoordinates)
	}

	objects := media.NewMemory()
	mint := func(objectID string) (string, error) {
		return objects.PresignGet(context.Background(),
			media.Key{Traveller: travellerUUID, Object: objectID}, media.Public)
	}

	// WITH THE SWITCH ON. The seeded trip's own value is false — the client's
	// default, because a pin on your accommodation has to be turned on every
	// time — so this leg turns it on rather than assuming it.
	on := src
	on.Trip.ShareCoordinates = true
	withPins, err := logbook.EmitPublic(on, mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}
	if got := countLats(t, withPins); got != fixtureCitiesOnTrip+fixturePlacesOnTrip+
		fixturePhotoCoordinates+countWalkPoints(withPins) {
		t.Errorf("with the switch ON the document carries %d coordinates; the fixture "+
			"holds %d city centres, %d pins, %d photograph traces and %d track points",
			got, fixtureCitiesOnTrip, fixturePlacesOnTrip, fixturePhotoCoordinates,
			countWalkPoints(withPins))
	}

	// WITH THE SWITCH OFF: the only coordinates left are the five city
	// centres. That is the scalpel — a city centre is coarse, it IS a city,
	// and it is what a map opens on when there are no pins to fit.
	off := src
	off.Trip.ShareCoordinates = false
	withoutPins, err := logbook.EmitPublic(off, mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}
	if got := countLats(t, withoutPins); got != fixtureCitiesOnTrip {
		t.Errorf("with the switch OFF the document carries %d coordinates, want %d — "+
			"the five city centres and nothing else. Every other one is somewhere a "+
			"person stood.", got, fixtureCitiesOnTrip)
	}
	// AND THE DOCUMENT IS OTHERWISE WHOLE. A filter that emptied it satisfies
	// the line above.
	if len(withoutPins.Photos) != fixturePhotosOnTrip || len(withoutPins.Places) != fixturePlacesOnTrip {
		t.Errorf("the coordinate switch took rows with it: %d photographs and %d places",
			len(withoutPins.Photos), len(withoutPins.Places))
	}
}

// countLats counts every `lat` anywhere in the marshalled document. It is a
// COUNT and not a path claim on purpose — the path claim is
// internal/httpapi's structural walk, and this is the arithmetic that walk
// cannot do at fixture scale.
func countLats(t *testing.T, env logbook.Public) int {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return lats(doc)
}

func lats(value any) int {
	switch v := value.(type) {
	case map[string]any:
		found := 0
		for key, child := range v {
			if key == "lat" && child != nil {
				found++
			}
			found += lats(child)
		}
		return found
	case []any:
		found := 0
		for _, child := range v {
			found += lats(child)
		}
		return found
	}
	return 0
}

func countWalkPoints(env logbook.Public) int {
	total := 0
	for _, walk := range env.Walks {
		total += len(walk.Points)
	}
	return total
}
