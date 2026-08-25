// M2's note, N1's 'Later' and M2.2's 'Change', as contracts. No database.
//
// WHAT IS HERE IS THE SHAPE OF A REQUEST AND THE SHAPE OF THE TYPE. Whether a
// caption-only PUT leaves a photograph's filing alone is a fact about a
// STATEMENT, so it is in internal/postgres and internal/seed — except for the
// one half of it that is a fact about the TYPE, which is here and is the
// strongest form the claim takes: `PhotoWrite` has nowhere to put a place.
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

// snoozeUntil is N1's own seven days, from a FIXED instant rather than
// time.Now — the clock is pinned everywhere else in this project and a leg
// whose input moves is a leg that can fail on a Tuesday.
var snoozeUntil = time.Date(2027, time.October, 19, 0, 0, 0, 0, time.UTC)

// ==================================================== THE COLUMNS THAT ARE NOT ON IT

// A CAPTION-ONLY PUT CANNOT UNFILE A PHOTOGRAPH BECAUSE THERE IS NOWHERE TO
// PUT THE NULL (DEC-89, SAF-MAJ-5).
//
// This is the type-level half of the step's worst defect, and it is asserted
// on the STRUCT rather than on a value: a leg that decoded `{"caption":"x"}`
// and checked that nothing else was set would pass just as well against a type
// that HAS the fields and happened not to receive them.
//
// The measured defect it forecloses: `ph-0` carried
// `place_id=bukchon, visit_id=v-bukchon-0`; the whole-state form of M2's note
// wrote both to NULL alongside the caption; and all three of this plan's
// standing guards stayed green, because the reference is GONE rather than
// dangling, there is no place left to be occasion-less, and two NULLs agree.
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

// AND EVERY FIELD THAT IS THERE IS A POINTER, WHICH IS THE OTHER HALF OF THE
// CONTRACT (DEC-89).
//
// A bare value makes absent and zero the same request. On `TripWrite.CityIDs`
// that shipped and destroyed an itinerary on every rename, measured against a
// running server; here it would clear a caption on every write that named a
// coordinate.
//
// IT WALKS BOTH WRITE TYPES IN THIS FILE AND THE WALK ONE TOO, because the
// rule is the convention and not a property of one struct — and a step adding
// a fourth write type inherits it by being in this loop.
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

// ==================================================== THE CAPTION

// AN EMPTY OR WHITESPACE CAPTION IS NULL AND NEVER THE EMPTY STRING.
//
// The client's rule, copied rather than invented: M2's two note blocks are
// both guarded by `caption != null`, so `caption = ”` is an empty box with no
// way back out of it. `photos_caption_present_ck` is the guarantee and this is
// what stops it reaching the client as a 500.
//
// THE FOURTH CASE IS THE ONE THAT MATTERS AND IT IS NOT ABOUT EMPTINESS: a
// caption that was NOT SENT must not be read as a clear, or every write that
// names a coordinate wipes the note.
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

// AND `{"caption":null}` IS INDISTINGUISHABLE FROM AN ABSENT KEY, WHICH IS
// MEASURED RATHER THAN DESIGNED.
//
// encoding/json's `indirect` breaks at the outermost SETTABLE pointer when the
// literal is null, so a `**string` field is set to nil by both. The
// consequence is a client prerequisite: M2's cleared note has to send
// `{"caption":""}`, because `{"caption":null}` is heard as "leave it alone".
// TripWrite's own comment records the same probe for `summary`; this leg is
// what makes it falsifiable on the field a real control actually clears.
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

// ==================================================== THE SNOOZE

// AN ABSENT `photoIds` AND AN EMPTY ONE ARE DIFFERENT REQUESTS.
//
// Both write nothing, and they are told apart so the first is a 422 naming the
// field — a body that never named a group — and the second is the 200 the
// client's own "returns false without writing when the group is empty"
// describes. Collapsing them would make a malformed request look like an
// ordinary empty one.
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

// AND `until` IS REQUIRED, BECAUSE THERE IS NO UN-SNOOZE.
func TestValidateSnoozeRequiresTheDateItIsSnoozingTo(t *testing.T) {
	ids := []string{"ph-12"}
	var invalid logbook.InvalidFieldError
	if err := logbook.ValidateSnooze(logbook.SnoozeWrite{PhotoIDs: &ids}); !errors.As(err, &invalid) ||
		invalid.Field != "until" {
		t.Errorf("a snooze with no date = %v, want an InvalidFieldError naming until", err)
	}
}

// A REPEATED ID IS REFUSED BY NAME.
//
// It is not fastidiousness: the update is `id = ANY($2)`, so a repeat is
// silently harmless there — and the answer is built from the rows that were
// written, so a client pairing its request against the answer by INDEX would
// pair them wrongly. `checkCityIDs` and `checkVisits` refuse a repeat for the
// same class of reason.
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

// ==================================================== THE REFILE

// A RE-FILE THAT NAMES NO OCCASION IS REFUSED BEFORE ANY STATEMENT RUNS, AND
// THE REFUSAL IS THE SERVICE'S (PD-05).
//
// THE MUTATION THIS LEG IS FOR: delete `Service.RefilePhoto`'s `VisitID == nil`
// branch and a nil reaches the store, whose parameterised `visit_id = $n` is
// then NULL — so the photograph is written naming a PLACE WITH NO OCCASION.
// That is the half-record state the client's model has never expressed:
// measured across all 284 fixture photographs, 95 carry both columns, 189
// carry neither, place-only 0, visit-only 0.
//
// IT ASSERTS THE STORE WAS NOT REACHED, and that is not decoration: a refusal
// taken AFTER the update would satisfy a status assertion perfectly.
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

	// AND A RE-FILE THAT NAMES NO PLACE IS REFUSED TOO. M2.2 lists the pins in
	// the photograph's own city and there is no 'nowhere' among them.
	visit := "v-nishiki-0"
	if _, err := service.RefilePhoto(t.Context(), "traveller", "ph-45",
		logbook.RefileWrite{VisitID: &visit}); !errors.As(err, &invalid) || invalid.Field != "placeId" {
		t.Errorf("a re-file with no placeId = %v, want an InvalidFieldError naming placeId", err)
	}

	// THE POSITIVE CONTROL, and it is what makes the two zeroes above mean
	// something: a complete body DOES reach the store.
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

// AND THE SERVICE PASSES THE CLIENT'S CHOICE THROUGH UNCHANGED.
//
// The whole route is "validate rather than re-derive", so the one thing this
// layer must not do is substitute an id of its own. A mutation that rewrote
// `VisitID` on the way past would be invisible to every leg that only checks
// the refusals.
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

// A MALFORMED ID IS REFUSED BY THE VALIDATOR AND A MISSING ONE IS NOT.
//
// The split is the one ValidateCity already makes with "a city needs a name":
// this function is about the SHAPE of what it was given. Whether an occasion
// was named at all is the Service's, above, because that is where the server's
// authority to choose is refused.
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

// ==================================================== THE PHOTO WRITE ITSELF

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

	// THE POSITIVE CONTROL: M2's own body, which is a caption and nothing
	// else. Without it every row above passes against a validator that
	// refuses everything.
	var note logbook.PhotoWrite
	if err := json.Unmarshal([]byte(`{"id":"ph-0","caption":"a new note"}`), &note); err != nil {
		t.Fatalf("decoding M2's note: %v", err)
	}
	if err := logbook.ValidatePhoto(note); err != nil {
		t.Errorf("M2's 'Write a note' = %v, want nil", err)
	}
}
