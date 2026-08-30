// M2's note, N1's 'Later' and M2.2's 'Change', as contracts.
package logbook_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func photoID(id string) *string { return &id }

// snoozeUntil is N1's own seven days, from a fixed instant rather than
// time.Now.
var snoozeUntil = time.Date(2027, time.October, 19, 0, 0, 0, 0, time.UTC)

// A caption-only put cannot unfile A photograph because there is nowhere to
// put the null.
func TestPhotoWriteHasNoSlotForAPlaceOrAnOccasion(t *testing.T) {
	forbidden := map[string]string{
		"PlaceID": "a photograph's pin is set by `POST /v1/photos/{id}/refile` and cleared " +
			"by `DELETE /v1/places/{id}`, and by nothing else",
		"VisitID": "the (place_id, visit_id) pair is coherent by a GO rule and not by the " +
			"schema (DEC-83), so every extra writer of it is another place it can be " +
			"written incoherently",
	}

	write := reflect.TypeOf(logbook.PhotoWrite{})
	for i := range write.NumField() {
		if why, banned := forbidden[write.Field(i).Name]; banned {
			t.Errorf("logbook.PhotoWrite has %s. %s.\n"+
				"    Adding it makes M2's 'Write a note' able to unfile a photograph, "+
				"which is a state NO count in this repository can see.",
				write.Field(i).Name, why)
		}
	}
}

// Every field that is there is a pointer, which is the other half of the
// contract.
func TestEveryFieldOnEveryWriteTypeIsAPointer(t *testing.T) {
	for _, write := range []any{
		logbook.PhotoWrite{}, logbook.WalkWrite{}, logbook.SnoozeWrite{},
		logbook.RefileWrite{}, logbook.PlaceWrite{}, logbook.CityWrite{},
		logbook.TripWrite{},
	} {
		kind := reflect.TypeOf(write)
		for i := range kind.NumField() {
			field := kind.Field(i)
			if field.Type.Kind() != reflect.Pointer {
				t.Errorf("%s.%s is a %s and not a pointer, so an ABSENT key and a ZERO "+
					"value are the same request (DEC-89). That is the shape that "+
					"answered 200 to a name-only trip write and left `trip_cities` "+
					"at zero rows", kind.Name(), field.Name, field.Type)
			}
		}
	}
}

// an empty or whitespace caption is null and never the empty string.
func TestStoredCaptionIsNullForEmptyAndUntouchedForAbsent(t *testing.T) {
	for _, tc := range []struct {
		body  string
		want  string // "" means the write stores NULL
		clear bool   // whether the field was SENT at all
	}{
		{`{"caption":"Last morning, and the lanes were empty for once"}`,
			"Last morning, and the lanes were empty for once", true},
		{`{"caption":"  trimmed  "}`, "trimmed", true},
		{`{"caption":""}`, "", true},
		{`{"caption":"   "}`, "", true},
		{`{"coordinates":{"lat":1,"lng":2}}`, "", false},
	} {
		var write logbook.PhotoWrite
		if err := json.Unmarshal([]byte(tc.body), &write); err != nil {
			t.Fatalf("decoding %s: %v", tc.body, err)
		}
		if sent := logbook.Sent(write.Caption); sent != tc.clear {
			t.Errorf("%s: Sent = %v, want %v — a caption that was never in the body "+
				"has no value to be wrong and must not be written at all",
				tc.body, sent, tc.clear)
		}
		got := logbook.StoredCaption(write.Caption)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: stored %q, want NULL", tc.body, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("%s: stored %v, want %q", tc.body, got, tc.want)
		}
	}
}

// `{"caption":null}` is indistinguishable from an absent key, which is
// measured rather than designed.
func TestASentNullCaptionIsHeardAsAnAbsentOne(t *testing.T) {
	var absent, explicit logbook.PhotoWrite
	if err := json.Unmarshal([]byte(`{"id":"ph-0"}`), &absent); err != nil {
		t.Fatalf("decoding an absent caption: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"id":"ph-0","caption":null}`), &explicit); err != nil {
		t.Fatalf("decoding a null caption: %v", err)
	}
	if logbook.Sent(absent.Caption) || logbook.Sent(explicit.Caption) {
		t.Fatalf("Sent(absent)=%v Sent(null)=%v — if a sent null has become "+
			"distinguishable, `{\"caption\":null}` now CLEARS a note and "+
			"docs/CLIENT-PREREQUISITES.md §R7.2 is wrong",
			logbook.Sent(absent.Caption), logbook.Sent(explicit.Caption))
	}

	var emptied logbook.PhotoWrite
	if err := json.Unmarshal([]byte(`{"id":"ph-0","caption":""}`), &emptied); err != nil {
		t.Fatalf("decoding an empty caption: %v", err)
	}
	if !logbook.Sent(emptied.Caption) {
		t.Error(`{"caption":""} was not heard at all, so there is now NO way to clear ` +
			`a note over the wire and M2's own control cannot be implemented`)
	}
}

// an absent `photoIds` and an empty one are different requests.
func TestValidateSnoozeTellsAnAbsentGroupFromAnEmptyOne(t *testing.T) {
	when := logbook.At(snoozeUntil)

	var absent logbook.SnoozeWrite
	if err := json.Unmarshal([]byte(`{"until":"2027-10-19T00:00:00.000Z"}`), &absent); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	var invalid logbook.InvalidFieldError
	if err := logbook.ValidateSnooze(absent); !errors.As(err, &invalid) || invalid.Field != "photoIds" {
		t.Errorf("a body with no photoIds = %v, want an InvalidFieldError naming photoIds", err)
	}

	empty := []string{}
	if err := logbook.ValidateSnooze(logbook.SnoozeWrite{PhotoIDs: &empty, Until: &when}); err != nil {
		t.Errorf("photoIds: [] = %v, want nil — a group that turned out to be empty is "+
			"a request this build answers 200 to, having written nothing", err)
	}
}

// `until` is required, because there is no un-snooze.
func TestValidateSnoozeRequiresTheDateItIsSnoozingTo(t *testing.T) {
	ids := []string{"ph-12"}
	var invalid logbook.InvalidFieldError
	if err := logbook.ValidateSnooze(logbook.SnoozeWrite{PhotoIDs: &ids}); !errors.As(err, &invalid) ||
		invalid.Field != "until" {
		t.Errorf("a snooze with no date = %v, want an InvalidFieldError naming until", err)
	}
}

// A repeated id is refused by name.
func TestValidateSnoozeRefusesARepeatedId(t *testing.T) {
	when := logbook.At(snoozeUntil)
	ids := []string{"ph-12", "ph-13", "ph-12"}

	var invalid logbook.InvalidFieldError
	err := logbook.ValidateSnooze(logbook.SnoozeWrite{PhotoIDs: &ids, Until: &when})
	if !errors.As(err, &invalid) || invalid.Field != "photoIds" {
		t.Errorf("a repeated id = %v, want an InvalidFieldError naming photoIds", err)
	}
	if !strings.Contains(invalid.Why, "ph-12") {
		t.Errorf("the reason does not say which id: %q", invalid.Why)
	}
}

// a re-file that names no occasion is refused before any statement runs, and
// the refusal is the service's.
func TestARefileThatNamesNoOccasionIsRefusedBeforeAnyStatementRuns(t *testing.T) {
	store := &countingRefiles{}
	service := logbook.Service{Photos: store}

	place := "nishiki"
	var invalid logbook.InvalidFieldError
	_, err := service.RefilePhoto(t.Context(), "traveller", "ph-45",
		logbook.RefileWrite{PlaceID: &place})
	if !errors.As(err, &invalid) || invalid.Field != "visitId" {
		t.Errorf("a re-file with no visitId = %v, want an InvalidFieldError naming visitId", err)
	}
	if store.calls != 0 {
		t.Errorf("the store was reached %d times. The server does not CHOOSE an "+
			"occasion — a place can be visited more than once on one trip, and an "+
			"unordered SELECT files the photograph to whichever row the planner "+
			"returned", store.calls)
	}

	visit := "v-nishiki-0"
	if _, err := service.RefilePhoto(t.Context(), "traveller", "ph-45",
		logbook.RefileWrite{VisitID: &visit}); !errors.As(err, &invalid) || invalid.Field != "placeId" {
		t.Errorf("a re-file with no placeId = %v, want an InvalidFieldError naming placeId", err)
	}

	if _, err := service.RefilePhoto(t.Context(), "traveller", "ph-45",
		logbook.RefileWrite{PlaceID: &place, VisitID: &visit}); err != nil {
		t.Fatalf("a complete re-file: %v", err)
	}
	if store.calls != 1 {
		t.Errorf("the store was reached %d times on a complete body, want 1", store.calls)
	}
}

// countingRefiles is the smallest PhotoStore that can answer "was the store
// reached", which is the only question the leg above asks of it.
type countingRefiles struct {
	calls int
	last  logbook.RefileWrite
}

func (c *countingRefiles) PutPhoto(_ context.Context, _ string, _ logbook.PhotoWrite) (logbook.Photo, int64, error) {
	return logbook.Photo{}, 0, nil
}

func (c *countingRefiles) DeletePhoto(_ context.Context, _, _ string) (int64, error) { return 0, nil }

func (c *countingRefiles) SnoozePhotos(_ context.Context, _ string, _ logbook.SnoozeWrite) ([]logbook.Photo, int64, error) {
	return nil, 0, nil
}

func (c *countingRefiles) RefilePhoto(_ context.Context, _, photoID string, w logbook.RefileWrite) (logbook.PhotoRefiled, error) {
	c.calls++
	c.last = w
	return logbook.PhotoRefiled{Photo: logbook.Photo{ID: photoID}}, nil
}

// The service passes the occasion the client named straight through.
func TestTheServicePassesTheOccasionTheClientNamedStraightThrough(t *testing.T) {
	store := &countingRefiles{}
	service := logbook.Service{Photos: store}

	place, visit := "nishiki", "v-nishiki-3"
	at := logbook.At(snoozeUntil)
	if _, err := service.RefilePhoto(t.Context(), "traveller", "ph-45", logbook.RefileWrite{
		PlaceID: &place, VisitID: &visit, VisitAt: &at,
	}); err != nil {
		t.Fatalf("a complete re-file: %v", err)
	}
	if store.last.VisitID == nil || *store.last.VisitID != visit {
		t.Errorf("the store was handed visitId %v, want %q", store.last.VisitID, visit)
	}
	if store.last.PlaceID == nil || *store.last.PlaceID != place {
		t.Errorf("the store was handed placeId %v, want %q", store.last.PlaceID, place)
	}
	if store.last.VisitAt == nil || !store.last.VisitAt.Time().Equal(at.Time()) {
		t.Errorf("the store was handed visitAt %v, want %v", store.last.VisitAt, at)
	}
}

// A malformed id is refused by the validator and A missing one is not.
func TestValidateRefileChecksShapeAndSaysNothingAboutAbsence(t *testing.T) {
	bad := "Fushimi Inari"
	var invalid logbook.InvalidFieldError
	if err := logbook.ValidateRefile(logbook.RefileWrite{PlaceID: &bad}); !errors.As(err, &invalid) ||
		invalid.Field != "placeId" {
		t.Errorf("a placeId that is not an id = %v, want an InvalidFieldError naming placeId", err)
	}
	if err := logbook.ValidateRefile(logbook.RefileWrite{VisitID: &bad}); !errors.As(err, &invalid) ||
		invalid.Field != "visitId" {
		t.Errorf("a visitId that is not an id = %v, want an InvalidFieldError naming visitId", err)
	}
	if err := logbook.ValidateRefile(logbook.RefileWrite{}); err != nil {
		t.Errorf("an EMPTY re-file = %v, want nil from the validator — absence is the "+
			"Service's refusal and putting it here too would be two spellings of "+
			"one rule", err)
	}
}

func TestValidatePhotoNamesTheFirstFieldThatIsWrong(t *testing.T) {
	long := strings.Repeat("x", logbook.MaxCaptionBytes+1)
	negative := -1
	outside := logbook.LatLng{Lat: 0, Lng: 181}

	for _, tc := range []struct {
		name  string
		write logbook.PhotoWrite
		field string
	}{
		{"no id", logbook.PhotoWrite{}, "id"},
		{"an id that is not one", logbook.PhotoWrite{ID: photoID("Fushimi Inari")}, "id"},
		{"a tripId that is not one",
			logbook.PhotoWrite{ID: photoID("ph-0"), TripID: photoID("Autumn Crossing")}, "tripId"},
		{"a cityId that is not one",
			logbook.PhotoWrite{ID: photoID("ph-0"), CityID: photoID("Seoul")}, "cityId"},
		{"an asset that is not a digest",
			logbook.PhotoWrite{ID: photoID("ph-0"), Asset: photoID("assets/imagery/hero-mountain.png")}, "asset"},
		{"a caption over the ceiling",
			logbook.PhotoWrite{ID: photoID("ph-0"), Caption: ptr(ptr(long))}, "caption"},
		{"a longitude outside the world",
			logbook.PhotoWrite{ID: photoID("ph-0"), Coordinates: ptr(ptr(outside))}, "coordinates"},
		{"a negative accuracy",
			logbook.PhotoWrite{ID: photoID("ph-0"), AccuracyMetres: ptr(ptr(negative))}, "accuracyMetres"},
	} {
		var invalid logbook.InvalidFieldError
		err := logbook.ValidatePhoto(tc.write)
		if !errors.As(err, &invalid) {
			t.Errorf("%s = %v, want an InvalidFieldError", tc.name, err)
			continue
		}
		if invalid.Field != tc.field {
			t.Errorf("%s named %q, want %q", tc.name, invalid.Field, tc.field)
		}
	}

	var note logbook.PhotoWrite
	if err := json.Unmarshal([]byte(`{"id":"ph-0","caption":"a new note"}`), &note); err != nil {
		t.Fatalf("decoding M2's note: %v", err)
	}
	if err := logbook.ValidatePhoto(note); err != nil {
		t.Errorf("M2's 'Write a note' = %v, want nil", err)
	}
}
