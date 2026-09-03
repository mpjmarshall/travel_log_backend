// The two write contracts, and's three answers `visits` can carry.
package logbook_test

import (
	"context"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// absent, empty and present are three answers and only one of them is a no-op
func TestValidationJudgesTheShapeOfVisitsAndNotWhatWritingItWouldDestroy(t *testing.T) {
	absent := logbook.PlaceWrite{ID: ptr("fushimi-inari")}
	if err := logbook.ValidatePlace(absent); err != nil {
		t.Errorf("a body with no `visits` key = %v, want no error. Absent means LEAVE "+
			"ALONE, which is what makes C1's pin — a wishlist place with no visits at "+
			"all — correct by construction", err)
	}

	empty := logbook.PlaceWrite{ID: ptr("fushimi-inari"), Visits: &[]logbook.Visit{}}
	if err := logbook.ValidatePlace(empty); err != nil {
		t.Errorf("`visits: []` = %v, want no error FROM VALIDATION. The destruction it "+
			"may or may not perform is the store's question, and answering it here "+
			"refuses every wishlist place in the log", err)
	}
}

func TestAVisitsArrayIsCheckedRowByRow(t *testing.T) {
	place := "fushimi-inari"
	long := strings.Repeat("x", logbook.MaxNoteBytes+1)
	for _, tc := range []struct {
		name  string
		visit logbook.Visit
	}{
		{"an id that is not an id", logbook.Visit{ID: "Not An Id", TripID: "kyoto-in-may"}},
		{"no trip at all", logbook.Visit{ID: "v-1"}},
		{"a trip id that is not an id", logbook.Visit{ID: "v-1", TripID: "Kyoto In May"}},
		{"another place", logbook.Visit{ID: "v-1", PlaceID: "tofuku-ji", TripID: "kyoto-in-may"}},
		{"a note over the ceiling", logbook.Visit{ID: "v-1", TripID: "kyoto-in-may", Note: &long}},
	} {
		visits := []logbook.Visit{tc.visit}
		err := logbook.ValidatePlace(logbook.PlaceWrite{ID: &place, Visits: &visits})
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if got := fieldOf(t, err); got != "visits" {
			t.Errorf("%s named %q, want \"visits\" — the client sent an array and the "+
				"array is the field it can fix", tc.name, got)
		}
	}

	visits := []logbook.Visit{{ID: "v-1", TripID: "kyoto-in-may"}}
	if err := logbook.ValidatePlace(logbook.PlaceWrite{ID: &place, Visits: &visits}); err != nil {
		t.Errorf("a visit with no placeId = %v, want no error", err)
	}
}

// `?photos` has no default, and the zero value is what enforces it.
func TestThePhotoDispositionRefusesEverythingButTheTwoSpellings(t *testing.T) {
	for _, raw := range []string{"", "keepp", "KEEP", "Delete", "true", "0", "keep,delete"} {
		got, err := logbook.ParsePhotoDisposition(raw)
		if err == nil {
			t.Errorf("?photos=%q parsed as %v, want a refusal", raw, got)
			continue
		}
		if field := fieldOf(t, err); field != "photos" {
			t.Errorf("?photos=%q named %q, want \"photos\"", raw, field)
		}
	}
	for raw, want := range map[string]logbook.PhotoDisposition{
		"keep":   logbook.KeepPhotos,
		"delete": logbook.DeletePhotos,
	} {
		got, err := logbook.ParsePhotoDisposition(raw)
		if err != nil || got != want {
			t.Errorf("?photos=%q = %v, %v; want %v and no error", raw, got, err, want)
		}
	}
}

// The absent case and the misspelled case are byte-identical, deliberately.
func TestAMissingPhotosParameterAndAMisspeltOneSayTheSameThing(t *testing.T) {
	_, absent := logbook.ParsePhotoDisposition("")
	_, misspelt := logbook.ParsePhotoDisposition("keepp")
	if absent.Error() != misspelt.Error() {
		t.Errorf("absent says %q and misspelt says %q", absent, misspelt)
	}
}

// The zero value is not reachable from outside the package, and the rule that
// refuses it is what keeps it from arriving as one of the two real answers.
func TestADispositionNobodyChoseIsRefused(t *testing.T) {
	if field := fieldOf(t, logbook.CheckPhotoDisposition(logbook.PhotoDisposition(0))); field != "photos" {
		t.Errorf("the zero disposition = %v, want an invalid_field on photos — without "+
			"this the zero value reaches the store, where it is not DeletePhotos and so "+
			"takes D2's KEEP branch: one of the two answers, silently, on a call that "+
			"chose neither", logbook.CheckPhotoDisposition(logbook.PhotoDisposition(0)))
	}
	for _, chosen := range []logbook.PhotoDisposition{logbook.KeepPhotos, logbook.DeletePhotos} {
		if err := logbook.CheckPhotoDisposition(chosen); err != nil {
			t.Errorf("%v = %v, want it through — without this the leg above passes "+
				"against a rule that refuses everything", chosen, err)
		}
	}
}

func TestACityWriteIsCheckedFieldByField(t *testing.T) {
	for _, tc := range []struct {
		field string
		write logbook.CityWrite
	}{
		{"id", logbook.CityWrite{}},
		{"id", logbook.CityWrite{ID: ptr("Kyoto")}},
		{"name", logbook.CityWrite{ID: ptr("kyoto"), Name: ptr("   ")}},
		{"name", logbook.CityWrite{ID: ptr("kyoto"), Name: ptr(strings.Repeat("x", logbook.MaxNameBytes+1))}},
		{"country", logbook.CityWrite{ID: ptr("kyoto"), Country: &logbook.Country{Code: "JAPAN", Name: "Japan"}}},
		{"country", logbook.CityWrite{ID: ptr("kyoto"), Country: &logbook.Country{Code: "jp", Name: "Japan"}}},
		{"country", logbook.CityWrite{ID: ptr("kyoto"), Country: &logbook.Country{Code: "JP", Name: " "}}},
		{"centre", logbook.CityWrite{ID: ptr("kyoto"), Centre: &logbook.LatLng{Lat: 91}}},
		{"centre", logbook.CityWrite{ID: ptr("kyoto"), Centre: &logbook.LatLng{Lng: -181}}},
		{"coverAsset", logbook.CityWrite{ID: ptr("kyoto"), CoverAsset: ptr(ptr("not-a-digest"))}},
		{"attachTo", logbook.CityWrite{ID: ptr("kyoto"), AttachTo: ptr("Autumn Crossing")}},
	} {
		err := logbook.ValidateCity(tc.write)
		if err == nil {
			t.Errorf("%+v was accepted, want a refusal on %s", tc.write, tc.field)
			continue
		}
		if got := fieldOf(t, err); got != tc.field {
			t.Errorf("%+v named %q, want %q", tc.write, got, tc.field)
		}
	}

	if err := logbook.ValidateCity(logbook.CityWrite{
		ID: ptr("kyoto"), Name: ptr("Kyoto"),
		Country: &logbook.Country{Code: "JP", Name: "Japan"},
		Centre:  &logbook.LatLng{Lat: 35.01, Lng: 135.76},
	}); err != nil {
		t.Errorf("a whole valid city = %v, want no error — without this every leg above "+
			"passes against a validator that refuses everything", err)
	}
}

func TestAPlaceWriteIsCheckedFieldByField(t *testing.T) {
	for _, tc := range []struct {
		field string
		write logbook.PlaceWrite
	}{
		{"id", logbook.PlaceWrite{}},
		{"cityId", logbook.PlaceWrite{ID: ptr("tofuku-ji"), CityID: ptr("Kyoto")}},
		{"name", logbook.PlaceWrite{ID: ptr("tofuku-ji"), Name: ptr("")}},
		{"coordinates", logbook.PlaceWrite{ID: ptr("tofuku-ji"), Coordinates: &logbook.LatLng{Lat: -91}}},
		{"plan", logbook.PlaceWrite{ID: ptr("tofuku-ji"),
			Plan: ptr(ptr(strings.Repeat("x", logbook.MaxNoteBytes+1)))}},
		{"coverAsset", logbook.PlaceWrite{ID: ptr("tofuku-ji"), CoverAsset: ptr(ptr("nope"))}},
	} {
		err := logbook.ValidatePlace(tc.write)
		if err == nil {
			t.Errorf("%+v was accepted, want a refusal on %s", tc.write, tc.field)
			continue
		}
		if got := fieldOf(t, err); got != tc.field {
			t.Errorf("%+v named %q, want %q", tc.write, got, tc.field)
		}
	}

	if err := logbook.ValidatePlace(logbook.PlaceWrite{
		ID: ptr("tofuku-ji"), CityID: ptr("kyoto"), Name: ptr("Tofuku-ji"),
		Coordinates: &logbook.LatLng{Lat: 34.976, Lng: 135.774},
	}); err != nil {
		t.Errorf("C1's pin = %v, want no error", err)
	}
}

// countingPlaces is a PlaceStore that records being reached and does nothing
// else.
type countingPlaces struct{ reached *int }

func (c countingPlaces) PutPlace(context.Context, string, logbook.PlaceWrite) (logbook.Place, int64, error) {
	*c.reached++
	return logbook.Place{}, 1, nil
}

func (c countingPlaces) RemovePlace(context.Context, string, string, bool) (logbook.Snapshot, error) {
	*c.reached++
	return logbook.Snapshot{Version: 1, Document: &logbook.Document{}}, nil
}
