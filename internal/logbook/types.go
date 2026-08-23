// The wire shape of the log, and the one place a date becomes a string.
//
// THESE STRUCTS ARE THE CLIENT'S OWN DOCUMENT, FIELD FOR FIELD AND KEY FOR
// KEY. `internal/logbook/testdata/client_sample_log.json` is the 85,422-byte
// log the Flutter app encoded before its fixture was deleted (DEC-75), and
// emit_test.go decodes it INTO these types and emits it back, asserting the
// two documents are equal value for value. That is what makes the shape a
// measurement rather than a transcription.
//
// NOTHING HERE IS `omitempty`, AND THAT IS THE RULE RATHER THAN AN OVERSIGHT.
// The client's codec reads a missing key and a null key as the same thing in
// some places and not in others — `trips[].summary` is `as String?` and
// tolerates both, `traveller` is a whole object it null-checks, and
// `logbook.trips` is `as List<dynamic>` with no null branch at all. A key that
// disappears when a value is empty is a shape that changes under the client,
// so every key is always present and an absent value is `null`.
//
// AND A NIL SLICE MARSHALS TO `null`, NOT `[]`. That is the specific trap
// behind "the four unimplemented lists are EMPTY rather than ABSENT": four
// `nil` fields would emit `"cities": null`, which is not an absent key and is
// not an empty list either — it is the one shape the client's
// `as List<dynamic>` throws on. Emit normalises; TestEveryListIsEmptyRatherThanNull
// is the leg.
//
// WHAT THE CLIENT DOES WITH NUMBERS, MEASURED RATHER THAN GUESSED:
// `city.g.dart:18` reads a coordinate as `(json['lat'] as num).toDouble()` and
// `distanceKm` the same way, so a whole-numbered latitude emitted by Go as
// `35` rather than `35.0` decodes correctly. A bare `as double` would not have,
// and it is worth having checked before relying on encoding/json's shortest
// round-tripping form.
package logbook

import (
	"fmt"
	"strings"
	"time"
)

// Document is the six keys, in the order the client's own encoder writes them.
type Document struct {
	Trips     []Trip     `json:"trips"`
	Cities    []City     `json:"cities"`
	Places    []Place    `json:"places"`
	Photos    []Photo    `json:"photos"`
	Walks     []Walk     `json:"walks"`
	Traveller *Traveller `json:"traveller"`
}

// Traveller is a pointer everywhere it appears, because the client casts
// `json['name'] as String` — non-nullable — so `{}` throws where `null` is
// read as "a log nobody has named yet".
type Traveller struct {
	Name string `json:"name"`
}

// Trip carries the four sharing fields OUT and the write does not take them
// back IN (SF6). The client has a dedicated `copyWithShare` precisely because
// the sharing group is written alone; see tripInput in the handlers.
type Trip struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	CityIDs          []string `json:"cityIds"`
	Start            *Instant `json:"start"`
	End              *Instant `json:"end"`
	Summary          *string  `json:"summary"`
	CoverAsset       *string  `json:"coverAsset"`
	ShareLinkID      *string  `json:"shareLinkId"`
	SharePhotos      bool     `json:"sharePhotos"`
	ShareNotes       bool     `json:"shareNotes"`
	ShareCoordinates bool     `json:"shareCoordinates"`
}

type City struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Country    Country `json:"country"`
	Centre     LatLng  `json:"centre"`
	CoverAsset *string `json:"coverAsset"`
}

// Country is DEC-59's two flattened columns wearing the client's nested shape.
// There is no countries table and the client has no country input anywhere.
type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Place struct {
	ID          string  `json:"id"`
	CityID      string  `json:"cityId"`
	Name        string  `json:"name"`
	Coordinates LatLng  `json:"coordinates"`
	Visits      []Visit `json:"visits"`
	Plan        *string `json:"plan"`
	CoverAsset  *string `json:"coverAsset"`
}

// Visit is nested inside its place on the wire and is its own table in
// storage. `at` is timestamptz (DEC-68) because the wire carries a real time
// of day and a date column truncates it silently.
type Visit struct {
	ID      string  `json:"id"`
	PlaceID string  `json:"placeId"`
	TripID  string  `json:"tripId"`
	At      Instant `json:"at"`
	Note    *string `json:"note"`
}

type Photo struct {
	ID             string   `json:"id"`
	TripID         string   `json:"tripId"`
	CityID         string   `json:"cityId"`
	TakenAt        Instant  `json:"takenAt"`
	Asset          string   `json:"asset"`
	PlaceID        *string  `json:"placeId"`
	VisitID        *string  `json:"visitId"`
	Caption        *string  `json:"caption"`
	Coordinates    *LatLng  `json:"coordinates"`
	AccuracyMetres *int     `json:"accuracyMetres"`
	FiledLater     *Instant `json:"filedLater"`
}

type Walk struct {
	ID         string   `json:"id"`
	TripID     string   `json:"tripId"`
	CityID     string   `json:"cityId"`
	RecordedOn Instant  `json:"recordedOn"`
	DistanceKm float64  `json:"distanceKm"`
	Points     []LatLng `json:"points"`
	Name       *string  `json:"name"`
	Dismissed  bool     `json:"dismissed"`
}

// instantLayout is what the client's own encoder produces, and the three
// zeroes are the whole reason this type exists.
//
// Dart's `DateTime.toIso8601String()` writes millisecond precision
// unconditionally — `2027-12-06T07:05:00.000Z` — and Go's time.Time marshals
// as RFC 3339 with trailing zeroes REMOVED, which would be
// `2027-12-06T07:05:00Z`. Both parse; only one is byte-identical to what the
// client sent, and DEC-68 asks for exactly that per date-bearing field.
//
// The trailing Z is appended rather than written as `Z07:00`, because the
// value is converted to UTC first and a layout offset would render `+00:00` on
// some inputs and `Z` on others.
const instantLayout = "2006-01-02T15:04:05.000"

// Instant is a time.Time that renders the way the client's encoder does.
//
// EVERY DATE-BEARING FIELD IN THE LOG USES IT, INCLUDING THE THREE STORED AS
// `date`. DEC-68 keeps trips.started_on, trips.ended_on and walks.recorded_on
// as date columns because those genuinely are midnight UTC on the wire — and
// says in the same breath that THE EMITTER MUST RE-RENDER THEM as
// `T00:00:00.000Z`, or `DateTime.parse` hands the client a non-UTC local time
// for those three fields and a UTC one for every other date in the log.
type Instant time.Time

func (i Instant) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(i).UTC().Format(instantLayout) + `Z"`), nil
}

func (i *Instant) UnmarshalJSON(b []byte) error {
	text := strings.Trim(string(b), `"`)
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return fmt.Errorf("logbook: %q is not an RFC 3339 instant: %w", text, err)
	}
	*i = Instant(parsed.UTC())
	return nil
}

// Time is the value back out, always in UTC.
func (i Instant) Time() time.Time { return time.Time(i).UTC() }

// At wraps a time.Time for the emitter.
func At(t time.Time) Instant { return Instant(t.UTC()) }
