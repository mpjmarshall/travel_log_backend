// The rules a write must satisfy that need a fact from storage to decide,
// called under the traveller's lock. Shape rules live in validate.go.
package logbook

import (
	"fmt"
	"strings"
)

// CheckWriteID refuses a write body carrying no id. It is unreachable over
// HTTP: a handler fills the id from the path before any store sees the body.
func CheckWriteID(id *string) error {
	if id == nil {
		return InvalidFieldError{Field: "id",
			Why: "a write needs the id it is writing to, and this body carries none"}
	}
	return nil
}

// missingField is one required-on-create column and what to say when a create
// arrives without it.
type missingField struct {
	absent bool
	field  string
	why    string
}

func firstMissing(missing []missingField) error {
	for _, m := range missing {
		if m.absent {
			return InvalidFieldError{Field: m.field, Why: m.why}
		}
	}
	return nil
}

// CheckCityWritable refuses a create that is missing a not NULL column, and
// names the field rather than letting a constraint answer.
func CheckCityWritable(isCreate bool, w CityWrite) error {
	if !isCreate {
		return nil
	}
	return firstMissing([]missingField{
		{w.Name == nil, "name", "a city that is not in this log yet has no name to leave alone"},
		{w.Country == nil, "country", "a city that is not in this log yet has no country to " +
			"leave alone, and country is derived from the city rather than typed"},
		{w.Centre == nil, "centre", "a city that is not in this log yet has no centre to " +
			"leave alone, and C1 pins a new place at it"},
	})
}

// CheckPlaceWritable refuses a create that is missing a not NULL column.
func CheckPlaceWritable(isCreate bool, w PlaceWrite) error {
	if !isCreate {
		return nil
	}
	return firstMissing([]missingField{
		{w.CityID == nil, "cityId", "a place belongs to a city, and one that is not in " +
			"this log yet has no city to leave alone"},
		{w.Name == nil, "name", "a place that is not in this log yet has no name to leave alone"},
		{w.Coordinates == nil, "coordinates", "a place that is not in this log yet has no " +
			"coordinates to leave alone — C1 pins at the city's centre when the user has " +
			"not moved it"},
	})
}

// CheckWalkWritable refuses a create that is missing a not NULL column.
func CheckWalkWritable(isCreate bool, w WalkWrite) error {
	if !isCreate {
		return nil
	}
	return firstMissing([]missingField{
		{w.TripID == nil, "tripId", "a walk happens on a trip, and one that is not in " +
			"this log yet has no trip to leave alone"},
		{w.CityID == nil, "cityId", "a walk happens in a city, and one that is not in " +
			"this log yet has no city to leave alone"},
		{w.RecordedOn == nil, "recordedOn", "a walk is a recording of a day, and one that " +
			"is not in this log yet has no day to leave alone"},
		{w.DistanceKm == nil, "distanceKm", "a walk that is not in this log yet has no " +
			"distance to leave alone"},
		{w.Points == nil, "points", "a walk that is not in this log yet has no track to " +
			"leave alone, and there is nothing this build could invent — a track is a " +
			"recording of a day that has passed"},
	})
}

// CheckPhotoWritable refuses a create that is missing a not NULL column.
func CheckPhotoWritable(isCreate bool, w PhotoWrite) error {
	if !isCreate {
		return nil
	}
	return firstMissing([]missingField{
		{w.TripID == nil, "tripId", "a photograph is taken on a trip, and one that is not " +
			"in this log yet has no trip to leave alone"},
		{w.CityID == nil, "cityId", "a photograph is taken in a city, and one that is not " +
			"in this log yet has no city to leave alone"},
		{w.TakenAt == nil, "takenAt", "a photograph is taken at a moment — it is what M1 " +
			"and L1 group by — and one that is not in this log yet has none to leave alone"},
		{w.Asset == nil, "asset", "a photograph IS its asset, and one that is not in this " +
			"log yet has none to leave alone"},
	})
}

// TripDates is what a trip's dates are before a write, so the rule that orders
// them can be decided without a database.
type TripDates struct {
	Start, End *Instant
}

// CheckTripWritable refuses a create with no name, and refuses any write that
// would leave the trip ending before it starts.
func CheckTripWritable(isCreate bool, before TripDates, w TripWrite) error {
	if isCreate && w.Name == nil {
		return InvalidFieldError{Field: "name",
			Why: "a trip that is not in this log yet has no name to leave alone"}
	}

	start, end := before.Start, before.End
	if Sent(w.Start) {
		start = Value(w.Start)
	}
	if Sent(w.End) {
		end = Value(w.End)
	}
	if start != nil && end != nil && end.Time().Before(start.Time()) {
		return InvalidFieldError{Field: "end", Why: "a trip cannot end before it starts"}
	}
	return nil
}

// CheckTravellerName trims and bounds U1's pencil, answering the name to store.
func CheckTravellerName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", InvalidFieldError{Field: "name",
			Why: "a traveller needs a name, and an empty one is not a way to clear it"}
	}
	if len(trimmed) > MaxNameBytes {
		return "", InvalidFieldError{Field: "name",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(trimmed), MaxNameBytes)}
	}
	return trimmed, nil
}

// CheckSnoozeWritable refuses a group with no moment, which is a filing that
// never ends.
func CheckSnoozeWritable(w SnoozeWrite) error {
	if w.PhotoIDs == nil || w.Until == nil {
		return InvalidFieldError{Field: "photoIds", Why: "a snooze names a group and a date"}
	}
	return nil
}

// CheckRefileWritable refuses half a pair: the pin and the occasion are
// coherent by this rule and not by the schema.
func CheckRefileWritable(w RefileWrite) error {
	if w.PlaceID == nil || w.VisitID == nil {
		return InvalidFieldError{Field: "visitId", Why: "a re-file names both the pin and the occasion"}
	}
	return nil
}

// CheckRefilePlace refuses a move between cities, which is a claim about where
// somebody was rather than a correction to a filing.
func CheckRefilePlace(placeID, placeCity, photoCity string) error {
	if placeCity == photoCity {
		return nil
	}
	return InvalidFieldError{Field: "placeId",
		Why: fmt.Sprintf("%q is in %s and the photograph was taken in %s — M2.2 lists the "+
			"pins in the photograph's OWN city, and moving one between cities is a claim "+
			"about where somebody was", placeID, placeCity, photoCity)}
}

// CheckNewOccasionHasAMoment refuses opening an occasion with no moment.
func CheckNewOccasionHasAMoment(visitID string, at *Instant) error {
	if at != nil {
		return nil
	}
	return InvalidFieldError{Field: "visitAt",
		Why: fmt.Sprintf("%q is not an occasion in this log, so this re-file is opening a "+
			"new one — and an occasion happens at a moment. The client already holds it: "+
			"`refilePhoto` mints the visit at the photograph's own `takenAt`", visitID)}
}

// CheckOccasionBelongsHere refuses an occasion that is another place's or
// another trip's, both of which file the photograph somewhere nobody named.
func CheckOccasionBelongsHere(visitID, heldPlace, wantPlace, heldTrip, wantTrip string) error {
	if heldPlace != wantPlace {
		return InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("the occasion %s belongs to %s and this re-file names %s. A "+
				"visit id is unique across the whole log, so filing to another place's "+
				"occasion would put the photograph somewhere nobody mentioned",
				visitID, heldPlace, wantPlace)}
	}
	if heldTrip != wantTrip {
		return InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("the occasion %s is on %s and the photograph was taken on %s. "+
				"A photograph filed to another trip's occasion lands in the wrong year row "+
				"on P1 and in that trip's cascade", visitID, heldTrip, wantTrip)}
	}
	return nil
}

// CheckClearingVisits refuses an empty visits array at a place that holds
// occasions, which unfiles every photograph filed to them.
func CheckClearingVisits(occasions int) error {
	if occasions == 0 {
		return nil
	}
	return InvalidFieldError{Field: "visits",
		Why: fmt.Sprintf("an empty visits array is a request to clear all %d occasions at "+
			"this place, which unfiles every photograph filed to them — no control in the "+
			"client asks for that, so this build refuses it. OMIT the key to leave the "+
			"visits alone", occasions)}
}
