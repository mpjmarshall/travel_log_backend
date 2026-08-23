// Package seed generates a large, realistic logbook and bulk-loads it into
// PostgreSQL, so the schema can be exercised at a size where the planner
// behaves the way it will in production.
//
// IT IS A DEVELOPER COMMAND AND MUST NEVER RUN IN PRODUCTION OR AT BOOT.
// Nothing in cmd/api imports this package; the only entry point is cmd/seed,
// which takes its DSN as an explicit flag rather than from the environment so
// that an ambient DATABASE_URL cannot aim it at a real database by accident.
//
// WHY IT EXISTS, WHICH IS NOT "IT LOOKS REALISTIC". The catalog leg in
// internal/postgres proves every foreign key's child columns lead some index —
// a structural claim, true at any size. It proves nothing about whether the
// planner ever CHOOSES one, and at fixture scale it correctly declines nearly
// all of them: a full trip cascade over 284 photographs used exactly one.
// Volume is the instrument that makes DEC-63/DEC-70's index question
// answerable at all.
//
// IT IS NOT A SECOND FIXTURE FOR TESTS. Tests build their own small worlds,
// with the rows the leg is about and nothing else; a shared 50,000-row world
// makes every assertion a statement about the generator instead. The only
// tests that touch this package are the ones about the generator itself.
package seed

import "time"

// Epoch is the generator's "today", and it is a constant rather than
// time.Now() for the same reason the client pins its clock to 2027-10-12: a
// generator whose output depends on the day it ran cannot be used to reproduce
// a measurement.
var Epoch = time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)

// DefaultPhotos is the size the human ruled, and everything else is derived
// from it — see Counts.
const DefaultPhotos = 50_000

// MinPhotos is the floor below which the edge-case guarantees stop fitting:
// the generator reserves rows for a place visited three times in one day, a
// wishlist place, an unfiled photograph and the rest, and below this there are
// not enough rows to reserve.
const MinPhotos = 40

// DefaultSeed is fixed so that two runs with no flags produce byte-identical
// databases. -seed varies it.
const DefaultSeed = 20260823

// DefaultTravellers is three rather than one, and the reason is the
// measurement rather than realism. Every index in 0001 leads with
// traveller_id; with a single traveller that column is a constant, the planner
// sees one distinct value, and any conclusion drawn about a composite index is
// a conclusion about a degenerate case.
const DefaultTravellers = 3

// DefaultDigests is a few dozen distinct content hashes across every
// photograph in the database, because that is what the real library looks
// like: the client fixture reuses TWO digests across 284 photographs. It is
// the whole argument for content-addressed storage (DEC-38), and a generator
// handing out 50,000 distinct digests would quietly delete it.
const DefaultDigests = 24

// Options is the knob. Photos is the size; the other four are the shape.
type Options struct {
	Photos     int
	Travellers int
	Digests    int
	Seed       uint64
}

// DefaultOptions is what cmd/seed runs with when no flag is given.
func DefaultOptions() Options {
	return Options{
		Photos:     DefaultPhotos,
		Travellers: DefaultTravellers,
		Digests:    DefaultDigests,
		Seed:       DefaultSeed,
	}
}

// Counts is the derived size of every table, and the derivation is the ratio
// the human ruled — 50,000 photographs to 200 trips, 400 cities, 600 places,
// 2,000 visits, 400 walks — expressed as multiples of the trip count so that
// it re-derives at any size rather than being five numbers to keep in step.
//
// The floors are what keep the edge-case guarantees reachable at a small size:
// a run with four trips still has a trip with no cities, a city with no
// places and a place visited three times in one day.
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

// Traveller mirrors the travellers table. Name is nullable and at least one
// generated traveller has none.
type Traveller struct {
	ID             string
	Email          string
	PassphraseHash string
	Name           *string
	LogbookVersion int64
	CreatedAt      time.Time
}

// MediaObject mirrors media_objects. ID is the sha256 hex the CHECK demands,
// and the bucket key is (TravellerID, ID) — DEC-38.
type MediaObject struct {
	TravellerID string
	ID          string
	ByteSize    int64
	ContentType string
	CreatedAt   time.Time
	UploadedAt  *time.Time
}

// City mirrors cities. Country is flattened into two columns (DEC-59).
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

// Trip mirrors trips. Both dates may be absent — T4's "Add dates" is a control
// the user may never press — and some generated trips have neither.
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

// TripCity mirrors trip_cities (DEC-64). Ordinal is travel order and is
// load-bearing on read.
type TripCity struct {
	TravellerID string
	TripID      string
	CityID      string
	Ordinal     int
}

// Place mirrors places. A place with no visits is a wishlist place, and that
// is what D3 promises survives a trip deletion.
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

// Visit mirrors visits. At is timestamptz and not date (DEC-68), and the
// generator relies on that: it puts three visits of one place on one trip
// inside a single day, which a date column cannot tell apart.
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

// Photo mirrors photos. PlaceID and VisitID are both nullable and both are
// null on an unfiled photograph.
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

// Walk mirrors walks. Points is a jsonb ARRAY, rendered as text by
// pointsJSON — this package may not import encoding/json, which
// internal/httpx's monopoly sweep confines to internal/httpx/json.go.
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

// ShareLink mirrors share_links (DEC-67): history is kept, and
// share_links_one_live allows at most one unrevoked row per trip.
type ShareLink struct {
	TravellerID string
	TripID      string
	Token       string
	CreatedAt   time.Time
	RevokedAt   *time.Time
}

// Session mirrors sessions. TokenHash is exactly 32 bytes, which the CHECK
// says in bytes.
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
