// What a photograph write may contain, and's two columns that are not on
// it.
package logbook

import (
	"fmt"
	"strings"
)

// MaxCaptionBytes bounds `photos.caption`.
const MaxCaptionBytes = 4096

// PhotoWrite is the body of `PUT /v1/photos/{id}`: M2's 'Write a note', and
// the create a client-minted key makes possible.
type PhotoWrite struct {
	ID             *string   `json:"id"`
	TripID         *string   `json:"tripId"`
	CityID         *string   `json:"cityId"`
	TakenAt        *Instant  `json:"takenAt"`
	Asset          *string   `json:"asset"`
	Caption        **string  `json:"caption"`
	Coordinates    **LatLng  `json:"coordinates"`
	AccuracyMetres **int     `json:"accuracyMetres"`
	FiledLater     **Instant `json:"filedLater"`
}

// ValidatePhoto answers's first field that is wrong, and nothing about
// whether the ids it names exist.
func ValidatePhoto(p PhotoWrite) error {
	if p.ID == nil || !idPattern.MatchString(*p.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if p.TripID != nil && !idPattern.MatchString(*p.TripID) {
		return InvalidFieldError{Field: "tripId",
			Why: fmt.Sprintf("%q is not an id", *p.TripID)}
	}
	if p.CityID != nil && !idPattern.MatchString(*p.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *p.CityID)}
	}
	if p.Asset != nil && !assetPattern.MatchString(*p.Asset) {
		return InvalidFieldError{Field: "asset",
			Why: "a photograph's asset is a media object id: 64 lowercase hex characters"}
	}
	if caption := Value(p.Caption); caption != nil && len(*caption) > MaxCaptionBytes {
		return InvalidFieldError{Field: "caption",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d",
				len(*caption), MaxCaptionBytes)}
	}
	if at := Value(p.Coordinates); at != nil {
		if err := checkLatLng(*at, "coordinates"); err != nil {
			return err
		}
	}
	if metres := Value(p.AccuracyMetres); metres != nil && *metres < 0 {
		return InvalidFieldError{Field: "accuracyMetres",
			Why: fmt.Sprintf("an accuracy is not negative, and %d is", *metres)}
	}
	return nil
}

// StoredCaption is the caption as it will be written: trimmed, and NULL
// Than the empty string.
func StoredCaption(sent **string) *string {
	value := Value(sent)
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// SnoozeWrite is the body of `POST /v1/photos/snooze`: N1's 'Later', and the
// FIRST route in this API that takes a COLLECTION.
type SnoozeWrite struct {
	PhotoIDs *[]string `json:"photoIds"`
	Until    *Instant  `json:"until"`
}

// ValidateSnooze answers's first field that is wrong.
func ValidateSnooze(s SnoozeWrite) error {
	if s.PhotoIDs == nil {
		return InvalidFieldError{Field: "photoIds",
			Why: "a snooze names the group it is snoozing; one row on N1 stands for " +
				"every unfiled photograph from one city on one trip"}
	}
	if s.Until == nil {
		return InvalidFieldError{Field: "until",
			Why: "'Later' is a date, and there is no control that takes a snooze back off"}
	}
	seen := make(map[string]bool, len(*s.PhotoIDs))
	for _, id := range *s.PhotoIDs {
		if !idPattern.MatchString(id) {
			return InvalidFieldError{Field: "photoIds",
				Why: fmt.Sprintf("%q is not an id", id)}
		}
		if seen[id] {
			return InvalidFieldError{Field: "photoIds",
				Why: fmt.Sprintf("%q appears twice, and a photograph is snoozed once", id)}
		}
		seen[id] = true
	}
	return nil
}

// RefileWrite is the body of `POST /v1/photos/{id}/refile`: M2.2's 'Change',
// Moves a photograph's pin and its occasion together.
type RefileWrite struct {
	PlaceID *string  `json:"placeId"`
	VisitID *string  `json:"visitId"`
	VisitAt *Instant `json:"visitAt"`
}

// ValidateRefile answers the SHAPE of the ids it was given and says nothing
// about whether they were given at all.
func ValidateRefile(r RefileWrite) error {
	if r.PlaceID != nil && !idPattern.MatchString(*r.PlaceID) {
		return InvalidFieldError{Field: "placeId",
			Why: fmt.Sprintf("%q is not an id", *r.PlaceID)}
	}
	if r.VisitID != nil && !idPattern.MatchString(*r.VisitID) {
		return InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("%q is not an id", *r.VisitID)}
	}
	return nil
}

// PhotoRefiled is what a refile answers, and it carries both shapes because
// the route has both — the device `CityWritten` uses, for its reason.
type PhotoRefiled struct {
	Photo    Photo
	Document *Document
	Version  int64
}
