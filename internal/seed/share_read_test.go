// The public envelope at fixture scale, against the client's own log.
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

// The plan's first named failing test, at the scale it was written against.
func TestTheSeededEnvelopeCarriesOnlyTheSharedTripsOwnPlaces(t *testing.T) {
	db, src := publishedBySeed(t)

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

// The nested rows, which the leg above passes while it leaks.
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

// The row rules for photographs, walks and cities, at fixture scale.
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

// The whole envelope, through the emitter, at fixture scale — which is
// where the measurement lives.
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
	if len(withoutPins.Photos) != fixturePhotosOnTrip || len(withoutPins.Places) != fixturePlacesOnTrip {
		t.Errorf("the coordinate switch took rows with it: %d photographs and %d places",
			len(withoutPins.Photos), len(withoutPins.Places))
	}
}

// countLats counts every `lat` anywhere in the marshalled document.
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
