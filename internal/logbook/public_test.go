// The three flags, one leg each, and the mint that must not happen.
package logbook_test

import (
	"encoding/json"
	"testing"
	"time"

	"travellog/internal/logbook"
)

// aSharedTrip is one of everything the envelope can carry, with every
// flag-sensitive field POPULATED.
func aSharedTrip() logbook.PublicSource {
	at := logbook.At(time.Date(2027, 10, 2, 9, 30, 0, 0, time.UTC))
	note := "the torii went on for ever"
	caption := "the last light on the ridge"
	summary := "two weeks, five cities"
	cover := "aa" + "00000000000000000000000000000000000000000000000000000000000000"
	accuracy := 12
	placeID := "fushimi-inari"

	return logbook.PublicSource{
		Trip: logbook.Trip{
			ID: "autumn-crossing", Name: "Autumn crossing",
			CityIDs:     []string{"kyoto", "busan"},
			Start:       &at,
			End:         &at,
			Summary:     &summary,
			CoverAsset:  &cover,
			SharePhotos: true, ShareNotes: true, ShareCoordinates: true,
		},
		Cities: []logbook.City{{
			ID: "kyoto", Name: "Kyoto",
			Country: logbook.Country{Code: "JP", Name: "Japan"},
			Centre:  logbook.LatLng{Lat: 35.0116, Lng: 135.7681},
		}},
		Places: []logbook.Place{{
			ID: placeID, CityID: "kyoto", Name: "Fushimi Inari",
			Coordinates: logbook.LatLng{Lat: 34.9671, Lng: 135.7727},
			Visits: []logbook.Visit{{
				ID: "v-0", PlaceID: placeID, TripID: "autumn-crossing", At: at, Note: &note,
			}},
		}},
		Photos: []logbook.Photo{{
			ID: "ph-0", TripID: "autumn-crossing", CityID: "kyoto",
			TakenAt: at, Asset: "bb" + "00000000000000000000000000000000000000000000000000000000000000",
			PlaceID: &placeID, Caption: &caption,
			Coordinates: &logbook.LatLng{Lat: 34.9671, Lng: 135.7727}, AccuracyMetres: &accuracy,
		}},
		Walks: []logbook.Walk{{
			ID: "w-0", TripID: "autumn-crossing", CityID: "kyoto",
			RecordedOn: at, DistanceKm: 6.4,
			Points: []logbook.LatLng{{Lat: 34.9, Lng: 135.7}, {Lat: 34.91, Lng: 135.71}},
		}},
	}
}

// countingMint is what turns "must not be minted" into something a test can
// see.
type countingMint struct{ calls int }

func (c *countingMint) mint(objectID string) (string, error) {
	c.calls++
	return "https://bucket.example/" + objectID + "?X-Amz-Expires=900", nil
}

func TestSharePhotosOffMintsNothingAtAll(t *testing.T) {
	src := aSharedTrip()
	src.Trip.SharePhotos = false
	minted := &countingMint{}

	env, err := logbook.EmitPublic(src, minted.mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}

	if minted.calls != 0 {
		t.Errorf("sharePhotos is off and %d signed URLs were minted.\n"+
			"    A presigned URL is a LIVE CAPABILITY from the moment it is signed,\n"+
			"    whether or not it reaches a response body. Not minted and withheld —\n"+
			"    not minted at all.", minted.calls)
	}
	if env.Photos == nil {
		t.Errorf("photos is null, want an empty array — `null` is neither an absent key " +
			"nor an empty list, and it is the one shape a List<dynamic> cast throws on")
	}
	if len(env.Photos) != 0 {
		t.Errorf("photos carries %d entries with sharePhotos off", len(env.Photos))
	}
	if env.Trip.CoverURL != nil {
		t.Errorf("trip.coverUrl = %v with sharePhotos off, want null", *env.Trip.CoverURL)
	}

	if len(env.Cities) != 1 || len(env.Places) != 1 || len(env.Walks) != 1 {
		t.Fatalf("the rest of the document did not survive: %d cities, %d places, %d walks",
			len(env.Cities), len(env.Places), len(env.Walks))
	}
	if env.Places[0].Days[0].Note == nil {
		t.Errorf("the note went with the photographs — shareNotes is a different switch")
	}
	if env.Places[0].Coordinates == nil {
		t.Errorf("the pin went with the photographs — shareCoordinates is a different switch")
	}
	if len(env.Walks[0].Points) != 2 {
		t.Errorf("the track went with the photographs — shareCoordinates is a different switch")
	}
}

func TestSharePhotosOnMintsEveryPictureExactlyOnce(t *testing.T) {
	minted := &countingMint{}
	env, err := logbook.EmitPublic(aSharedTrip(), minted.mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}
	if minted.calls != 2 {
		t.Errorf("minted %d URLs, want 2 — one photograph and the trip's cover", minted.calls)
	}
	if env.Trip.CoverURL == nil {
		t.Fatal("trip.coverUrl is null on a trip that has a cover and shares its photographs")
	}
	if env.Photos[0].URL == "" {
		t.Error("the photograph carries no url — the public reader cannot mint one")
	}
}

func TestShareNotesOffStripsAVisitsNoteAndAPhotographsCaption(t *testing.T) {
	src := aSharedTrip()
	src.Trip.ShareNotes = false

	env, err := logbook.EmitPublic(src, (&countingMint{}).mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}

	if env.Places[0].Days[0].Note != nil {
		t.Errorf("places[].days[].note = %q with shareNotes off", *env.Places[0].Days[0].Note)
	}
	if env.Photos[0].Caption != nil {
		t.Errorf("photos[].caption = %q with shareNotes off", *env.Photos[0].Caption)
	}

	if len(env.Places[0].Days) != 1 {
		t.Errorf("the day went with the note: %d days, want 1", len(env.Places[0].Days))
	}
	if len(env.Photos) != 1 {
		t.Errorf("the photograph went with the caption: %d photos, want 1", len(env.Photos))
	}
	if env.Places[0].Coordinates == nil || len(env.Walks[0].Points) != 2 {
		t.Errorf("shareNotes reached the coordinates, which belong to a different switch")
	}
}

func TestShareCoordinatesOffStripsFourThingsAndKeepsTheCityCentre(t *testing.T) {
	src := aSharedTrip()
	src.Trip.ShareCoordinates = false

	env, err := logbook.EmitPublic(src, (&countingMint{}).mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}

	if env.Places[0].Coordinates != nil {
		t.Errorf("places[].coordinates survived shareCoordinates being off: %+v",
			*env.Places[0].Coordinates)
	}
	if env.Photos[0].Coordinates != nil {
		t.Errorf("photos[].coordinates survived: %+v", *env.Photos[0].Coordinates)
	}
	if env.Photos[0].AccuracyMetres != nil {
		t.Errorf("photos[].accuracyMetres survived: %d — it is half of the same fact",
			*env.Photos[0].AccuracyMetres)
	}
	if len(env.Walks[0].Points) != 0 {
		t.Errorf("walks[].points survived with %d points", len(env.Walks[0].Points))
	}
	if env.Walks[0].Points == nil {
		t.Error("walks[].points is null, want [] — an empty array and not a missing key, " +
			"and not null")
	}

	if env.Cities[0].Centre.Lat == 0 || env.Cities[0].Centre.Lng == 0 {
		t.Errorf("cities[].centre = %+v — the one coordinate that survives this flag",
			env.Cities[0].Centre)
	}
	if env.Walks[0].DistanceKm != 6.4 {
		t.Errorf("walks[].distanceKm = %v, want 6.4", env.Walks[0].DistanceKm)
	}
}

// the six keys, and `traveller` is not one of them.
func TestThePublicEnvelopeHasSixTopLevelKeysAndNoTraveller(t *testing.T) {
	env, err := logbook.EmitPublic(aSharedTrip(), (&countingMint{}).mint)
	if err != nil {
		t.Fatalf("EmitPublic: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]bool{
		"version": true, "trip": true, "cities": true,
		"places": true, "photos": true, "walks": true,
	}
	for key := range got {
		if !want[key] {
			t.Errorf("the public envelope carries %q, which is not on the allowlist", key)
		}
	}
	for key := range want {
		if _, held := got[key]; !held {
			t.Errorf("the public envelope is missing %q", key)
		}
	}
	if _, held := got["logbook"]; held {
		t.Error("the public envelope nests under `logbook` — see docs/PUBLIC-ENVELOPE.md §3")
	}
	if env.Version != logbook.FormatVersion {
		t.Errorf("version = %d, want %d — the same number GET /v1/logbook stamps",
			env.Version, logbook.FormatVersion)
	}
}

// A mint that fails is the whole read failing, not a photograph with an empty
// url.
func TestAMintThatFailsFailsTheRead(t *testing.T) {
	_, err := logbook.EmitPublic(aSharedTrip(), func(string) (string, error) {
		return "", errBucketDown
	})
	if err == nil {
		t.Fatal("EmitPublic answered an envelope while the bucket was refusing to sign")
	}
}

var errBucketDown = errUnreachable{}

type errUnreachable struct{}

func (errUnreachable) Error() string { return "the bucket is unreachable" }
