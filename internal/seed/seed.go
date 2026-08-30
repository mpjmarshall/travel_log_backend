// Package seed generates a large, realistic logbook and bulk-loads it into
// PostgreSQL.
package seed

import "time"

// Epoch is the generator's "today", and it is a constant rather than
// time.Now for the same reason the client pins its clock to 2027-10-12.
var Epoch = time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)

// DefaultPhotos is the size the human ruled, and everything else is derived
// from it — see Counts.
const DefaultPhotos = 50_000

// MinPhotos is the floor below which the edge-case guarantees stop fitting.
const MinPhotos = 40

// DefaultSeed is fixed so that two runs with no flags produce byte-identical
// databases.
const DefaultSeed = 20260823

// DefaultTravellers is three rather than one, and the reason is the
// measurement rather than realism.
const DefaultTravellers = 3

// DefaultDigests supplies distinct content hashes for every photograph in the
// fixture.
const DefaultDigests = 24

// Options is the knob.
type Options struct {
	Photos     int
	Travellers int
	Digests    int
	Seed       uint64
}

// DefaultOptions is what the seed command will run with when no flag is
// given.
func DefaultOptions() Options {
	return Options{
		Photos:     DefaultPhotos,
		Travellers: DefaultTravellers,
		Digests:    DefaultDigests,
		Seed:       DefaultSeed,
	}
}

// Counts is the derived size of every table.
type Counts struct {
	Photos int
	Trips  int
	Cities int
	Places int
	Visits int
	Walks  int
}

// CountsFor derives every table's size from the photograph count.
func CountsFor(photos int) Counts {
	trips := photos / 250
	if trips < 4 {
		trips = 4
	}
	return Counts{
		Photos: photos,
		Trips:  trips,
		Cities: 2 * trips,
		Places: 3 * trips,
		Visits: 10 * trips,
		Walks:  2 * trips,
	}
}

// Traveller mirrors the travellers table.
type Traveller struct {
	ID             string
	Email          string
	PassphraseHash string
	Name           *string
	LogbookVersion int64
	CreatedAt      time.Time
}

// MediaObject mirrors media_objects.
type MediaObject struct {
	TravellerID string
	ID          string
	ByteSize    int64
	ContentType string
	CreatedAt   time.Time
	UploadedAt  *time.Time
}

// City mirrors cities.
type City struct {
	TravellerID string
	ID          string
	Name        string
	CountryCode string
	CountryName string
	CentreLat   float64
	CentreLng   float64
	CoverAsset  *string
	GeocoderRef *string
	CreatedAt   time.Time
}

// Trip mirrors trips.
type Trip struct {
	TravellerID      string
	ID               string
	Name             string
	StartedOn        *time.Time
	EndedOn          *time.Time
	Summary          *string
	CoverAsset       *string
	SharePhotos      bool
	ShareNotes       bool
	ShareCoordinates bool
	CreatedAt        time.Time
}

// TripCity mirrors trip_cities.
type TripCity struct {
	TravellerID string
	TripID      string
	CityID      string
	Ordinal     int
}

// Place mirrors places.
type Place struct {
	TravellerID string
	ID          string
	CityID      string
	Name        string
	Lat         float64
	Lng         float64
	Plan        *string
	CoverAsset  *string
	CreatedAt   time.Time
}

// Visit mirrors visits.
type Visit struct {
	TravellerID string
	ID          string
	PlaceID     string
	TripID      string
	Ordinal     int
	At          time.Time
	Note        *string
	CreatedAt   time.Time
}

// Photo mirrors photos.
type Photo struct {
	TravellerID    string
	ID             string
	TripID         string
	CityID         string
	PlaceID        *string
	VisitID        *string
	TakenAt        time.Time
	Asset          string
	Caption        *string
	Lat            *float64
	Lng            *float64
	AccuracyMetres *int
	FiledLater     *time.Time
	CreatedAt      time.Time
}

// Walk mirrors walks.
type Walk struct {
	TravellerID string
	ID          string
	TripID      string
	CityID      string
	RecordedOn  time.Time
	DistanceKm  float64
	Points      string
	Name        *string
	Dismissed   bool
	CreatedAt   time.Time
}

// ShareLink mirrors share_links: history is kept, and share_links_one_live
// allows at most one unrevoked row per trip.
type ShareLink struct {
	TravellerID string
	TripID      string
	TokenHash   []byte
	CreatedAt   time.Time
	RevokedAt   *time.Time
}

// Session mirrors sessions.
type Session struct {
	ID          string
	TravellerID string
	TokenHash   []byte
	CreatedAt   time.Time
	LastUsedAt  time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// Dataset is one whole database's worth of rows, in dependency order: every
// slice may reference only the slices above it.
type Dataset struct {
	Travellers   []Traveller
	MediaObjects []MediaObject
	Cities       []City
	Trips        []Trip
	TripCities   []TripCity
	Places       []Place
	Visits       []Visit
	Photos       []Photo
	Walks        []Walk
	ShareLinks   []ShareLink
	Sessions     []Session
}
