// The wire shape of the log, and the one place a date becomes a string.
package logbook

import (
	"fmt"
	"strings"
	"time"
)

// Document is the six keys, in the order the client's own encoder writes
// them.
type Document struct {
	Trips     []Trip     `json:"trips"`
	Cities    []City     `json:"cities"`
	Places    []Place    `json:"places"`
	Photos    []Photo    `json:"photos"`
	Walks     []Walk     `json:"walks"`
	Traveller *Traveller `json:"traveller"`
}

// Traveller is a pointer everywhere it appears.
type Traveller struct {
	Name string `json:"name"`
}

// Trip carries the four sharing fields OUT and the write does not take them
// back IN.
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
	Shared           bool     `json:"shared"`
}

type City struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Country    Country `json:"country"`
	Centre     LatLng  `json:"centre"`
	CoverAsset *string `json:"coverAsset"`
}

// Country is's two flattened columns wearing the client the nested shape.
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
// storage.
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

// instantLayout is what the client's own encoder produces, and's three
// zeroes are the whole reason this type exists.
const instantLayout = "2006-01-02T15:04:05.000"

// Instant is a time.Time that renders the way the client the encoder does.
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
