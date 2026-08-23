package logbook_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func day(y int, m time.Month, d int) *logbook.Instant {
	i := logbook.At(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
	return &i
}

func text(s string) *string { return &s }

func validTrip() logbook.TripWrite {
	return logbook.TripWrite{
		ID:      "autumn-crossing",
		Name:    "Autumn crossing",
		CityIDs: []string{"kyoto", "matsumoto"},
		Start:   day(2027, time.September, 17),
		End:     day(2027, time.October, 2),
		Summary: text("Down the length of Japan"),
	}
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v (%T), want a logbook.InvalidFieldError", err, err)
	}
	return invalid.Field
}

func TestAWholeValidTripIsAccepted(t *testing.T) {
	if err := logbook.ValidateTrip(validTrip()); err != nil {
		t.Errorf("ValidateTrip(a valid trip) = %v, want nil", err)
	}
}

// The dateless trip is not an edge case: T4's "Add dates" is a control the
// user may never press, and T3 creates a trip with no cities at all before T5
// adds one.
func TestATripWithNoDatesAndNoCitiesIsAccepted(t *testing.T) {
	trip := validTrip()
	trip.Start, trip.End, trip.CityIDs, trip.Summary = nil, nil, nil, nil
	if err := logbook.ValidateTrip(trip); err != nil {
		t.Errorf("ValidateTrip(a trip with nothing optional) = %v, want nil", err)
	}
}

func TestEachRefusalNamesTheFieldTheClientCanShow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*logbook.TripWrite)
		field  string
	}{
		{"an empty id", func(w *logbook.TripWrite) { w.ID = "" }, "id"},
		{"an id with a capital in it", func(w *logbook.TripWrite) { w.ID = "Kyoto" }, "id"},
		{"an id with a slash in it", func(w *logbook.TripWrite) { w.ID = "kyoto/2027" }, "id"},
		{"an id over 64 characters", func(w *logbook.TripWrite) { w.ID = strings.Repeat("a", 65) }, "id"},
		{"an empty name", func(w *logbook.TripWrite) { w.Name = "" }, "name"},
		{"a name of nothing but spaces", func(w *logbook.TripWrite) { w.Name = "   " }, "name"},
		{"a name past the ceiling", func(w *logbook.TripWrite) { w.Name = strings.Repeat("a", logbook.MaxNameBytes+1) }, "name"},
		{"a summary past the ceiling", func(w *logbook.TripWrite) { w.Summary = text(strings.Repeat("a", logbook.MaxSummaryBytes+1)) }, "summary"},
		{"a city id that is not a slug", func(w *logbook.TripWrite) { w.CityIDs = []string{"kyoto", "MATSUMOTO"} }, "cityIds"},
		{"the same city twice", func(w *logbook.TripWrite) { w.CityIDs = []string{"kyoto", "kyoto"} }, "cityIds"},
		{"a cover asset that is not a content hash", func(w *logbook.TripWrite) { w.CoverAsset = text("assets/imagery/hero-mountain.png") }, "coverAsset"},
		{"a cover asset in the wrong case", func(w *logbook.TripWrite) { w.CoverAsset = text(strings.Repeat("A", 64)) }, "coverAsset"},
		{"a trip that ends before it starts", func(w *logbook.TripWrite) { w.End = day(2027, time.September, 16) }, "end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trip := validTrip()
			tc.mutate(&trip)
			err := logbook.ValidateTrip(trip)
			if err == nil {
				t.Fatalf("ValidateTrip(%s) = nil, want a refusal", tc.name)
			}
			if got := fieldOf(t, err); got != tc.field {
				t.Errorf("field = %q, want %q", got, tc.field)
			}
		})
	}
}

// The DUPLICATE leg above is not fastidiousness, and this says why in a leg:
// trip_cities' primary key is (traveller_id, trip_id, city_id), so a repeated
// city is a constraint violation on the second INSERT of a delete-then-insert
// — which reaches the client as a 500 with nothing to act on rather than as a
// named field.
func TestATripEndingOnTheDayItStartsIsAccepted(t *testing.T) {
	trip := validTrip()
	trip.End = trip.Start
	if err := logbook.ValidateTrip(trip); err != nil {
		t.Errorf("ValidateTrip(a one-day trip) = %v, want nil — the schema's own "+
			"check is `ended_on >= started_on`", err)
	}
}

func TestAnEndWithNoStartIsAccepted(t *testing.T) {
	trip := validTrip()
	trip.Start = nil
	if err := logbook.ValidateTrip(trip); err != nil {
		t.Errorf("ValidateTrip(an end and no start) = %v, want nil — "+
			"trips_dates_ordered_ck tolerates either being NULL", err)
	}
}

// THE FOUR SHARING FIELDS HAVE NOWHERE TO LAND, and that is the strongest form
// SF6 can take: not a rule the write remembers to apply, but a type with no
// slot for them. DEC-13 keeps DisallowUnknownFields off, so a client sending
// them is not refused — it is simply not heard.
func TestTheWriteTypeCannotCarryTheSharingFields(t *testing.T) {
	const body = `{"id":"kyoto","name":"Kyoto","cityIds":["kyoto"],
		"shareLinkId":"kyoto-9f2a","sharePhotos":true,"shareNotes":true,"shareCoordinates":true}`

	var got logbook.TripWrite
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding a body carrying the sharing fields: %v", err)
	}

	round, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	for _, key := range []string{"shareLinkId", "sharePhotos", "shareNotes", "shareCoordinates"} {
		if strings.Contains(string(round), key) {
			t.Errorf("TripWrite carries %q; the sharing group is written alone, which is "+
				"why the client has a dedicated copyWithShare", key)
		}
	}
	if got.ID != "kyoto" || got.Name != "Kyoto" {
		t.Errorf("the seven fields it DOES own did not survive: %+v", got)
	}
}
