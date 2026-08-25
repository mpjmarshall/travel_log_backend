// The two write contracts, and the three answers `visits` can carry.
//
// EVERY LEG HERE IS ABOUT A REFUSAL THAT NEEDS NO DATABASE. Existence is the
// store's — a check made out here is a check made against a database that can
// move underneath it — so what this file is about is shape: what a field may
// look like, and the one field where an EMPTY value and an ABSENT value are
// different requests with different blast radii.
package logbook_test

import (
	"context"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// `ptr` and `fieldOf` are validate_test.go's, in the same test package.

// === THE ONE THE STEP IS ABOUT ===

// ABSENT, EMPTY AND PRESENT ARE THREE ANSWERS AND ONLY ONE OF THEM IS A NO-OP
// (DEC-89, SAF-MAJ-4).
//
// The middle one is the whole finding. PD-06's upsert fix closes a no-op
// re-send of an UNCHANGED array and does not close this: the mandated shape
// ends "DELETE only the ids absent from the incoming array", and when the array
// is empty EVERY id is absent. Measured against the client's own fixture at
// `fushimi-inari`: 28 visits and 30 photographs across 3 trips, and the empty
// array leaves 0 and 0 with `place_id IS NOT NULL AND visit_id IS NULL` at 30.
// Whole-log it unfiles 95 photographs and destroys 5 visit notes with every
// table count except `visits` unchanged.
func TestValidationJudgesTheShapeOfVisitsAndNotWhatWritingItWouldDestroy(t *testing.T) {
	absent := logbook.PlaceWrite{ID: ptr("fushimi-inari")}
	if err := logbook.ValidatePlace(absent); err != nil {
		t.Errorf("a body with no `visits` key = %v, want no error. Absent means LEAVE "+
			"ALONE, which is what makes C1's pin — a wishlist place with no visits at "+
			"all — correct by construction", err)
	}

	// `visits: []` IS NOT REFUSED HERE, AND IT USED TO BE. Whether clearing
	// destroys anything is a fact about the place's current occasions, and this
	// function is handed an array and nothing else. Refusing it on shape
	// refused nine of the seventeen places in the client's own log — every
	// wishlist place, for which `Emit` writes exactly this — so the document
	// the server produced was one it would not accept back.
	//
	// The refusal lives in `postgres.writeVisits` now, one query from the
	// count. `internal/seed` holds the leg that proves both halves against a
	// real database; this one only proves the shape check has stopped
	// answering a question it cannot see.
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

	// AN EMPTY placeId IS THE ORDINARY CASE and must not be refused: the path
	// carries the place, so a client that lets it is not wrong.
	visits := []logbook.Visit{{ID: "v-1", TripID: "kyoto-in-may"}}
	if err := logbook.ValidatePlace(logbook.PlaceWrite{ID: &place, Visits: &visits}); err != nil {
		t.Errorf("a visit with no placeId = %v, want no error", err)
	}
}

// === D2's TWO BRANCHES AS A TYPE ===

// `?photos` HAS NO DEFAULT, AND THE ZERO VALUE IS WHAT ENFORCES IT.
//
// "A default is a silent answer to the question D2 makes the user answer on
// screen." A `bool` would have made `false` mean keep, so a caller that never
// answered would get one of the two branches and no error.
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

// THE ABSENT CASE AND THE MISSPELLED CASE ARE BYTE-IDENTICAL, deliberately: to
// a caller they are one condition — this route will not guess how far a
// deletion reaches — and two different sentences would suggest one of them has
// a safe answer.
func TestAMissingPhotosParameterAndAMisspeltOneSayTheSameThing(t *testing.T) {
	_, absent := logbook.ParsePhotoDisposition("")
	_, misspelt := logbook.ParsePhotoDisposition("keepp")
	if absent.Error() != misspelt.Error() {
		t.Errorf("absent says %q and misspelt says %q", absent, misspelt)
	}
}

// THE ZERO VALUE IS NOT REACHABLE FROM OUTSIDE THE PACKAGE, which is what
// makes "there is no default" a property of the type rather than a rule a
// handler remembers. What CAN be built outside is `PhotoDisposition(0)`, and
// Service.RemovePlace refuses it before the store is reached.
func TestTheServiceRefusesADispositionNobodyChose(t *testing.T) {
	var reached int
	svc := logbook.Service{Places: countingPlaces{reached: &reached}}

	_, err := svc.RemovePlace(t.Context(), "traveller", "tofuku-ji", logbook.PhotoDisposition(0))
	if field := fieldOf(t, err); field != "photos" {
		t.Errorf("the zero disposition = %v, want an invalid_field on photos", err)
	}
	if reached != 0 {
		t.Errorf("the store was reached %d times by a call that never answered the "+
			"question. Delete this guard and the zero value arrives as "+
			"`deletePhotos == false`, which is D2's KEEP branch — one of the two "+
			"answers, silently, on a call that chose neither", reached)
	}

	if _, err := svc.RemovePlace(t.Context(), "traveller", "tofuku-ji", logbook.DeletePhotos); err != nil {
		t.Fatalf("a real disposition = %v, want it through to the store", err)
	}
	if reached != 1 {
		t.Errorf("the store was reached %d times by a call that chose delete, want 1 — "+
			"without this the leg above passes against a Service that refuses "+
			"everything", reached)
	}
}

// === THE CITY ===

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

	// `country_code = 'JAPAN'` — five characters — INSERTED SUCCESSFULLY before
	// `cities_country_code_ck` existed, which is 0001's own recorded reason for
	// the check. This is the Go half naming the field first.
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
// else. It exists for one leg — "the store was not reached" — and a fuller
// twin would be a second implementation of D2 with nothing asserting about it.
type countingPlaces struct{ reached *int }

func (c countingPlaces) PutPlace(context.Context, string, logbook.PlaceWrite) (logbook.Place, int64, error) {
	*c.reached++
	return logbook.Place{}, 1, nil
}

func (c countingPlaces) RemovePlace(context.Context, string, string, bool) (logbook.Snapshot, error) {
	*c.reached++
	return logbook.Snapshot{Version: 1, Document: &logbook.Document{}}, nil
}
