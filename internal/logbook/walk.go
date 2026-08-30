// What a walk write may contain, and the one field in this API that cannot be
// re-recorded if it is lost.
package logbook

import (
	"errors"
	"fmt"
)

// MaxWalkPoints is as confirmed by: five hundred, and it stops being
// provisional.
const MaxWalkPoints = 500

// WalkWrite is the body of `PUT /v1/walks/{id}`: N1's 'Name it', N1's
// 'Discard', and the create a client-minted key makes possible.
type WalkWrite struct {
	ID         *string   `json:"id"`
	TripID     *string   `json:"tripId"`
	CityID     *string   `json:"cityId"`
	RecordedOn *Instant  `json:"recordedOn"`
	DistanceKm *float64  `json:"distanceKm"`
	Points     *[]LatLng `json:"points"`
	Name       **string  `json:"name"`
	Dismissed  *bool     `json:"dismissed"`
}

// ValidateWalk answers's first field that is wrong, and nothing about
// whether the ids it names exist.
func ValidateWalk(w WalkWrite) error {
	if w.ID == nil || !idPattern.MatchString(*w.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if w.TripID != nil && !idPattern.MatchString(*w.TripID) {
		return InvalidFieldError{Field: "tripId",
			Why: fmt.Sprintf("%q is not an id", *w.TripID)}
	}
	if w.CityID != nil && !idPattern.MatchString(*w.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *w.CityID)}
	}
	if w.DistanceKm != nil && *w.DistanceKm < 0 {
		return InvalidFieldError{Field: "distanceKm",
			Why: fmt.Sprintf("a distance is not negative, and %v is", *w.DistanceKm)}
	}
	if name := Value(w.Name); name != nil {
		if err := checkName(*name, "name", "a walk's name is what takes it off N1, "+
			"and an empty one is not a way to clear it — N1's control for that is "+
			"'Discard', which is `dismissed`"); err != nil {
			return err
		}
	}
	if w.Points != nil {
		if err := checkPoints(*w.Points); err != nil {
			return err
		}
	}
	return nil
}

// checkPoints is the cap and 0003's lower bound, in that order, and the pair
// is what makes the leg over it falsifiable.
func checkPoints(points []LatLng) error {
	if len(points) == 0 {
		return InvalidFieldError{Field: "points",
			Why: "an empty track is a request to destroy a recording of a day that has " +
				"passed, and nothing can re-record it. No control in the client asks " +
				"for that — N1's 'Discard' sets `dismissed` and keeps the track. OMIT " +
				"the key to leave the points alone"}
	}
	if len(points) > MaxWalkPoints {
		return InvalidFieldError{Field: "points",
			Why: fmt.Sprintf("%d points, and this build takes at most %d — decimate the "+
				"track before sending it; C2 draws a polyline and does not need every fix",
				len(points), MaxWalkPoints)}
	}
	for i, point := range points {
		var invalid InvalidFieldError
		if err := checkLatLng(point, "points"); errors.As(err, &invalid) {
			return InvalidFieldError{Field: "points",
				Why: fmt.Sprintf("point %d: %s", i, invalid.Why)}
		} else if err != nil {
			return err
		}
	}
	return nil
}
