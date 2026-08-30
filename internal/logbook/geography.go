// What a city write and a place write may contain, and the one contract in
// this API where "absent" and "empty" are two different destructive answers.
package logbook

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxNoteBytes bounds's two free-text fields — `places.plan` and
// `visits.note` — and it is this build's policy rather than schema.
const MaxNoteBytes = 4096

// countryCodePattern is the compiled regexp for the flattened country, and it
// is the same expression as `cities_country_code_ck`.
var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

// CityWrite is the body of `PUT /v1/cities/{id}`: T5's 'Add a city', which in
// the client is `createCity` and `setTripCities` and here is one request.
type CityWrite struct {
	ID         *string  `json:"id"`
	Name       *string  `json:"name"`
	Country    *Country `json:"country"`
	Centre     *LatLng  `json:"centre"`
	CoverAsset **string `json:"coverAsset"`
	AttachTo   *string  `json:"attachTo"`
}

// ValidateCity answers's first field that is wrong, and nothing about
// whether the ids it names exist.
func ValidateCity(c CityWrite) error {
	if c.ID == nil || !idPattern.MatchString(*c.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if c.Name != nil {
		if err := checkName(*c.Name, "name", "a city needs a name"); err != nil {
			return err
		}
	}
	if c.Country != nil {
		if !countryCodePattern.MatchString(c.Country.Code) {
			return InvalidFieldError{Field: "country",
				Why: fmt.Sprintf("%q is not an ISO-3166-1 alpha-2 code: two capitals",
					c.Country.Code)}
		}
		if strings.TrimSpace(c.Country.Name) == "" {
			return InvalidFieldError{Field: "country",
				Why: "a country code arrives with the name the geocoder gave it"}
		}
	}
	if c.Centre != nil {
		if err := checkLatLng(*c.Centre, "centre"); err != nil {
			return err
		}
	}
	if cover := Value(c.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	if c.AttachTo != nil && !idPattern.MatchString(*c.AttachTo) {
		return InvalidFieldError{Field: "attachTo",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	return nil
}

// PlaceWrite is the body of `PUT /v1/places/{id}`: C1's pin, and the only
// route in this API that carries a whole ordered child collection.
type PlaceWrite struct {
	ID          *string  `json:"id"`
	CityID      *string  `json:"cityId"`
	Name        *string  `json:"name"`
	Coordinates *LatLng  `json:"coordinates"`
	Visits      *[]Visit `json:"visits"`
	Plan        **string `json:"plan"`
	CoverAsset  **string `json:"coverAsset"`
}

// ValidatePlace answers's first field that is wrong.
func ValidatePlace(p PlaceWrite) error {
	if p.ID == nil || !idPattern.MatchString(*p.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if p.CityID != nil && !idPattern.MatchString(*p.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *p.CityID)}
	}
	if p.Name != nil {
		if err := checkName(*p.Name, "name", "a place needs a name"); err != nil {
			return err
		}
	}
	if p.Coordinates != nil {
		if err := checkLatLng(*p.Coordinates, "coordinates"); err != nil {
			return err
		}
	}
	if plan := Value(p.Plan); plan != nil && len(*plan) > MaxNoteBytes {
		return InvalidFieldError{Field: "plan",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*plan), MaxNoteBytes)}
	}
	if cover := Value(p.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	if p.Visits != nil {
		if err := checkVisits(*p.Visits, *p.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkVisits is the field the whole step is about, and it checks the SHAPE
// of the array rather than what writing it would destroy.
func checkVisits(visits []Visit, placeID string) error {
	seen := make(map[string]bool, len(visits))
	for _, visit := range visits {
		if !idPattern.MatchString(visit.ID) {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("%q is not a visit id", visit.ID)}
		}
		if seen[visit.ID] {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("%q appears twice, and an occasion happens once", visit.ID)}
		}
		seen[visit.ID] = true

		if !idPattern.MatchString(visit.TripID) {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the visit %s names %q, which is not a trip id",
					visit.ID, visit.TripID)}
		}
		if visit.PlaceID != "" && visit.PlaceID != placeID {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the visit %s names the place %q and this write is to %q",
					visit.ID, visit.PlaceID, placeID)}
		}
		if visit.Note != nil && len(*visit.Note) > MaxNoteBytes {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the note on %s is %d bytes, and this build takes at most %d",
					visit.ID, len(*visit.Note), MaxNoteBytes)}
		}
	}
	return nil
}

func checkName(name, field, why string) error {
	if strings.TrimSpace(name) == "" {
		return InvalidFieldError{Field: field, Why: why}
	}
	if len(name) > MaxNameBytes {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(name), MaxNameBytes)}
	}
	return nil
}

// checkLatLng is the Go half of `cities_centre_lat_ck` and `places_lat_ck`.
func checkLatLng(at LatLng, field string) error {
	if at.Lat < -90 || at.Lat > 90 {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("a latitude is between -90 and 90, and %v is not", at.Lat)}
	}
	if at.Lng < -180 || at.Lng > 180 {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("a longitude is between -180 and 180, and %v is not", at.Lng)}
	}
	return nil
}

// PhotoDisposition is D2's two branches, and it has no usable zero value on
// purpose.
type PhotoDisposition int

const (
	photosUnspecified PhotoDisposition = iota

	KeepPhotos

	DeletePhotos
)

func (d PhotoDisposition) String() string {
	switch d {
	case KeepPhotos:
		return "keep"
	case DeletePhotos:
		return "delete"
	default:
		return "unspecified"
	}
}

// ParsePhotoDisposition reads the query parameter, and an ABSENT one is
// refused by the same branch a misspelled one is.
func ParsePhotoDisposition(raw string) (PhotoDisposition, error) {
	switch raw {
	case "keep":
		return KeepPhotos, nil
	case "delete":
		return DeletePhotos, nil
	default:
		return photosUnspecified, InvalidFieldError{Field: "photos",
			Why: "removing a place asks what happens to the photographs filed there: " +
				"?photos=keep leaves them with their date and city, ?photos=delete " +
				"takes them and the notes written on them. There is no default"}
	}
}
