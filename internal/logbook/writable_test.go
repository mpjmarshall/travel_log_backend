// The create rules, decided from a fact the store supplies and the write body.
package logbook_test

import (
	"strings"
	"testing"

	"travellog/internal/logbook"
)

func TestACityCreatedWithoutARequiredFieldNamesIt(t *testing.T) {
	whole := logbook.CityWrite{
		Name:    ptr("Kyoto"),
		Country: &logbook.Country{Code: "JP", Name: "Japan"},
		Centre:  &logbook.LatLng{Lat: 35.01, Lng: 135.76},
	}

	if err := logbook.CheckCityWritable(true, whole); err != nil {
		t.Fatalf("a create carrying every required field: %v", err)
	}

	for _, leg := range []struct {
		field string
		strip func(*logbook.CityWrite)
	}{
		{"name", func(w *logbook.CityWrite) { w.Name = nil }},
		{"country", func(w *logbook.CityWrite) { w.Country = nil }},
		{"centre", func(w *logbook.CityWrite) { w.Centre = nil }},
	} {
		t.Run(leg.field, func(t *testing.T) {
			w := whole
			leg.strip(&w)
			var invalid logbook.InvalidFieldError
			err := logbook.CheckCityWritable(true, w)
			if !asField(err, &invalid) || invalid.Field != leg.field {
				t.Errorf("a create without %s = %v, want an invalid_field naming it",
					leg.field, err)
			}
		})
	}
}

func TestAnUpdateLeavesEveryUnsentFieldAlone(t *testing.T) {
	if err := logbook.CheckCityWritable(false, logbook.CityWrite{}); err != nil {
		t.Errorf("an update carrying nothing but an id = %v, want no error — absent "+
			"means leave alone, and a rename sends only a name", err)
	}
}

func asField(err error, into *logbook.InvalidFieldError) bool {
	for err != nil {
		if got, ok := err.(logbook.InvalidFieldError); ok {
			*into = got
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func TestAPlaceAWalkAndAPhotoCreatedWithoutTheirColumnsNameThem(t *testing.T) {
	for _, leg := range []struct {
		entity string
		field  string
		check  func() error
	}{
		{"place", "cityId", func() error {
			return logbook.CheckPlaceWritable(true, logbook.PlaceWrite{
				Name: ptr("Fushimi"), Coordinates: &logbook.LatLng{Lat: 1, Lng: 2}})
		}},
		{"place", "name", func() error {
			return logbook.CheckPlaceWritable(true, logbook.PlaceWrite{
				CityID: ptr("kyoto"), Coordinates: &logbook.LatLng{Lat: 1, Lng: 2}})
		}},
		{"place", "coordinates", func() error {
			return logbook.CheckPlaceWritable(true, logbook.PlaceWrite{
				CityID: ptr("kyoto"), Name: ptr("Fushimi")})
		}},
		{"walk", "tripId", func() error {
			return logbook.CheckWalkWritable(true, logbook.WalkWrite{})
		}},
		{"walk", "points", func() error {
			return logbook.CheckWalkWritable(true, logbook.WalkWrite{
				TripID: ptr("t"), CityID: ptr("c"),
				RecordedOn: *day(2027, 5, 12), DistanceKm: ptr(3.5)})
		}},
		{"photo", "tripId", func() error {
			return logbook.CheckPhotoWritable(true, logbook.PhotoWrite{})
		}},
		{"photo", "asset", func() error {
			return logbook.CheckPhotoWritable(true, logbook.PhotoWrite{
				TripID: ptr("t"), CityID: ptr("c"), TakenAt: *day(2027, 5, 12)})
		}},
	} {
		t.Run(leg.entity+"/"+leg.field, func(t *testing.T) {
			var invalid logbook.InvalidFieldError
			err := leg.check()
			if !asField(err, &invalid) || invalid.Field != leg.field {
				t.Errorf("a %s create without %s = %v, want an invalid_field naming it",
					leg.entity, leg.field, err)
			}
		})
	}
}

// The partial write is why the rule needs the before-dates: only the sent half
// is overridden, and the stored half decides whether the order still holds.
func TestATripCannotEndBeforeItStarts(t *testing.T) {
	may, april := day(2027, 5, 12), day(2027, 4, 1)

	if err := logbook.CheckTripWritable(true, logbook.TripDates{}, logbook.TripWrite{}); err == nil {
		t.Error("a trip create with no name was accepted, want an invalid_field on name")
	}

	whole := logbook.TripWrite{Name: ptr("Kyoto"), Start: may, End: april}
	var invalid logbook.InvalidFieldError
	if err := logbook.CheckTripWritable(true, logbook.TripDates{}, whole); !asField(err, &invalid) || invalid.Field != "end" {
		t.Errorf("a create ending before it starts = %v, want an invalid_field on end", err)
	}

	onlyEnd := logbook.TripWrite{End: april}
	stored := logbook.TripDates{Start: *may}
	if err := logbook.CheckTripWritable(false, stored, onlyEnd); !asField(err, &invalid) || invalid.Field != "end" {
		t.Errorf("moving only the end before the stored start = %v, want an invalid_field "+
			"on end — the constraint compares the columns after the write", err)
	}

	good := logbook.TripWrite{End: may}
	if err := logbook.CheckTripWritable(false, logbook.TripDates{Start: *april}, good); err != nil {
		t.Errorf("moving the end after the stored start = %v, want no error", err)
	}
}

func TestATravellerNameIsTrimmedAndBounded(t *testing.T) {
	got, err := logbook.CheckTravellerName("  Matt  ")
	if err != nil || got != "Matt" {
		t.Errorf("CheckTravellerName(%q) = %q, %v; want \"Matt\", nil", "  Matt  ", got, err)
	}
	if _, err := logbook.CheckTravellerName("   "); namedField(err) != "name" {
		t.Errorf("an all-space name = %v, want an invalid_field on name — an empty one is "+
			"not a way to clear it", err)
	}
	long := make([]byte, logbook.MaxNameBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := logbook.CheckTravellerName(string(long)); namedField(err) != "name" {
		t.Errorf("a name over the ceiling = %v, want an invalid_field on name", err)
	}
}

func TestASnoozeAndARefileNeedBothHalves(t *testing.T) {
	if got := namedField(logbook.CheckSnoozeWritable(logbook.SnoozeWrite{PhotoIDs: &[]string{"p"}})); got != "photoIds" {
		t.Errorf("a snooze with no moment named %q, want \"photoIds\"", got)
	}
	if got := namedField(logbook.CheckSnoozeWritable(logbook.SnoozeWrite{Until: *day(2027, 5, 12)})); got != "photoIds" {
		t.Errorf("a snooze with no group named %q, want \"photoIds\"", got)
	}
	if err := logbook.CheckSnoozeWritable(logbook.SnoozeWrite{
		PhotoIDs: &[]string{"p"}, Until: *day(2027, 5, 12)}); err != nil {
		t.Errorf("a whole snooze = %v, want no error", err)
	}
	if got := namedField(logbook.CheckRefileWritable(logbook.RefileWrite{PlaceID: ptr("p")})); got != "visitId" {
		t.Errorf("a re-file naming only a place named %q, want \"visitId\"", got)
	}
}

func TestAPhotographDoesNotMoveBetweenCitiesOrOntoAnotherPlacesOccasion(t *testing.T) {
	if got := namedField(logbook.CheckRefilePlace("gamcheon", "busan", "kyoto")); got != "placeId" {
		t.Errorf("re-filing across cities named %q, want \"placeId\" — moving one between "+
			"cities is a claim about where somebody was", got)
	}
	if err := logbook.CheckRefilePlace("gamcheon", "busan", "busan"); err != nil {
		t.Errorf("re-filing within one city = %v, want no error", err)
	}

	if got := namedField(logbook.CheckNewOccasionHasAMoment("v-new", nil)); got != "visitAt" {
		t.Errorf("opening an occasion with no moment named %q, want \"visitAt\"", got)
	}
	if err := logbook.CheckNewOccasionHasAMoment("v-new", *day(2027, 5, 12)); err != nil {
		t.Errorf("opening an occasion at a moment = %v, want no error", err)
	}

	if got := namedField(logbook.CheckOccasionBelongsHere("v", "bukchon", "gamcheon", "t", "t")); got != "visitId" {
		t.Errorf("another place's occasion named %q, want \"visitId\"", got)
	}
	if got := namedField(logbook.CheckOccasionBelongsHere("v", "gamcheon", "gamcheon", "autumn", "kyoto")); got != "visitId" {
		t.Errorf("another trip's occasion named %q, want \"visitId\"", got)
	}
	if err := logbook.CheckOccasionBelongsHere("v", "gamcheon", "gamcheon", "t", "t"); err != nil {
		t.Errorf("the photograph's own place and trip = %v, want no error", err)
	}
}

func TestClearingAPlaceThatHoldsOccasionsIsRefusedAndAnEmptyOneMayGo(t *testing.T) {
	err := logbook.CheckClearingVisits(3)
	var invalid logbook.InvalidFieldError
	if !asField(err, &invalid) || invalid.Field != "visits" {
		t.Fatalf("clearing a place holding three occasions = %v, want invalid_field on visits", err)
	}
	if !strings.Contains(invalid.Why, "all 3 occasions") {
		t.Errorf("the reason is %q and does not carry the count", invalid.Why)
	}
	if err := logbook.CheckClearingVisits(0); err != nil {
		t.Errorf("clearing a place holding nothing = %v, want no error", err)
	}
}

func namedField(err error) string {
	var invalid logbook.InvalidFieldError
	if !asField(err, &invalid) {
		return ""
	}
	return invalid.Field
}
