// The three flags, one leg each, and the mint that must not happen.
//
// EVERY LEG HERE ASSERTS WHAT IS ABSENT **AND** THAT EVERYTHING ELSE IS STILL
// THERE. A filter that empties the document satisfies every absence assertion
// ever written, and this file's whole subject is a filter.
//
// WHERE THE ROW RULES ARE NOT. docs/PUBLIC-ENVELOPE.md §5 has three of them —
// which places, which of a place's visits, and which photographs and walks —
// and none of them is in this package. They are the STORE's, in SQL, because
// they are statements about which rows exist rather than about which keys
// survive, and a second implementation of them here would be a second thing to
// keep in agreement. What this package owns is the allowlist and the flags.
package logbook_test

import (
	"encoding/json"
	"testing"
	"time"

	"travellog/internal/logbook"
)

// aSharedTrip is one of everything the envelope can carry, with every
// flag-sensitive field POPULATED — so a leg that says "this is gone" is about
// a filter and never about a fixture that never had one.
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
// see. An assertion about the RESPONSE BODY cannot see a URL that was minted
// and dropped: presigning is offline arithmetic, so "minted and withheld" is
// indistinguishable from "never minted" in every log there is. The rule is
// about the mint.
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

	// AND EVERYTHING ELSE IS STILL THERE. A filter that empties the document
	// satisfies all four assertions above.
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
	// One photograph and one trip cover. This is the OTHER side of the leg
	// above: a mint counter that can only ever read zero is a counter that
	// proves nothing about the switch.
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

	// BOTH. A note on a visit and a note on a photograph are the same promise
	// to the user, and stripping one is the mutation this leg exists for.
	if env.Places[0].Days[0].Note != nil {
		t.Errorf("places[].days[].note = %q with shareNotes off", *env.Places[0].Days[0].Note)
	}
	if env.Photos[0].Caption != nil {
		t.Errorf("photos[].caption = %q with shareNotes off", *env.Photos[0].Caption)
	}

	// AND THE DAY ITSELF SURVIVES. Removing the visit would satisfy the first
	// assertion and would take the trip's own dates with it — the client reads
	// days.first.at as when the place was last visited.
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
	// ONE SWITCH GOVERNS BOTH (DEC-108). A place's pin is somewhere the
	// traveller chose to mark; a photograph's coordinate is where they STOOD,
	// to metres, with a timestamp beside it. H1 says "share coordinates" and
	// the user reading it means all of them.
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

	// THE SCALPEL. This is the assertion that proves the filter is not a
	// blanket: a city centre is coarse — it IS a city — and it is what a map
	// opens on when there are no pins to fit. Removing it leaves a share page
	// that cannot draw a map at all, which is a different product rather than
	// a more private one.
	if env.Cities[0].Centre.Lat == 0 || env.Cities[0].Centre.Lng == 0 {
		t.Errorf("cities[].centre = %+v — the one coordinate that survives this flag",
			env.Cities[0].Centre)
	}
	// And the walk is still published, with its distance. A track whose points
	// are withheld is still a day that happened.
	if env.Walks[0].DistanceKm != 6.4 {
		t.Errorf("walks[].distanceKm = %v, want 6.4", env.Walks[0].DistanceKm)
	}
}

// THE SIX KEYS, AND `traveller` IS NOT ONE OF THEM.
//
// The full structural walk is in internal/httpapi, over the real HTTP answer
// and against the golden. This is the top level only, and it is here because
// it is the difference from the PRIVATE document that is easiest to reintroduce
// by copying the emitter: a share link is a capability over ONE TRIP rather
// than over a log, and the owner's name is not part of a trip.
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
	// `logbook` IS NOT ONE OF THEM EITHER. The private read nests its five
	// lists under {"version":…,"logbook":{…}}; this is a different shape, so
	// nesting it under the same key would invite a reader to decode it with
	// the logbook codec and get a document whose `trips` list is missing.
	if _, held := got["logbook"]; held {
		t.Error("the public envelope nests under `logbook` — see docs/PUBLIC-ENVELOPE.md §3")
	}
	if env.Version != logbook.FormatVersion {
		t.Errorf("version = %d, want %d — the same number GET /v1/logbook stamps",
			env.Version, logbook.FormatVersion)
	}
}

// A MINT THAT FAILS IS THE WHOLE READ FAILING, not a photograph with an empty
// url. The client cannot tell a picture that will not load from one that was
// never shared, and an envelope carrying `"url":""` is a document that decodes
// and then draws nothing.
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
