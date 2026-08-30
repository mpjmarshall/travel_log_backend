package logbook_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

func day(y int, m time.Month, d int) **logbook.Instant {
	i := logbook.At(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
	p := &i
	return &p
}

func text(s string) **string {
	p := &s
	return &p
}

// ptr is the one-line helper's pointer contract costs every caller.
func ptr[T any](v T) *T { return &v }

func validTrip() logbook.TripWrite {
	return logbook.TripWrite{
		ID:      ptr("autumn-crossing"),
		Name:    ptr("Autumn crossing"),
		CityIDs: ptr([]string{"kyoto", "matsumoto"}),
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

// The dateless trip is not an edge case.
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
		{"an empty id", func(w *logbook.TripWrite) { w.ID = ptr("") }, "id"},
		{"an id with a capital in it", func(w *logbook.TripWrite) { w.ID = ptr("Kyoto") }, "id"},
		{"an id with a slash in it", func(w *logbook.TripWrite) { w.ID = ptr("kyoto/2027") }, "id"},
		{"an id over 64 characters", func(w *logbook.TripWrite) { w.ID = ptr(strings.Repeat("a", 65)) }, "id"},
		{"an empty name", func(w *logbook.TripWrite) { w.Name = ptr("") }, "name"},
		{"a name of nothing but spaces", func(w *logbook.TripWrite) { w.Name = ptr("   ") }, "name"},
		{"a name past the ceiling", func(w *logbook.TripWrite) { w.Name = ptr(strings.Repeat("a", logbook.MaxNameBytes+1)) }, "name"},
		{"a summary past the ceiling", func(w *logbook.TripWrite) { w.Summary = text(strings.Repeat("a", logbook.MaxSummaryBytes+1)) }, "summary"},
		{"a city id that is not a slug", func(w *logbook.TripWrite) { w.CityIDs = ptr([]string{"kyoto", "MATSUMOTO"}) }, "cityIds"},
		{"the same city twice", func(w *logbook.TripWrite) { w.CityIDs = ptr([]string{"kyoto", "kyoto"}) }, "cityIds"},
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

// The duplicate leg above is not fastidiousness, and this says why in a leg.
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

// The four sharing fields have nowhere to land, and that is the strongest
// form SF6 can take.
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
	if got.ID == nil || *got.ID != "kyoto" || got.Name == nil || *got.Name != "Kyoto" {
		t.Errorf("the seven fields it DOES own did not survive: %+v", got)
	}
}

func TestTheAllowlistTakesTwoTypesAndRefusesTheRest(t *testing.T) {
	for _, ok := range logbook.AllowedContentTypes() {
		if !logbook.ContentTypeAllowed(ok) {
			t.Errorf("the allowlist refuses %q, which is in its own list", ok)
		}
	}

	for _, no := range []string{
		"image/heic",
		"text/html; <script>",
		"text/html",
		"image/svg+xml",
		"image/png ",
		" image/png",
		"IMAGE/PNG",
		"image/pngx",
		"ximage/png",
		"image/jpeg\nimage/png",
		"",
	} {
		if logbook.ContentTypeAllowed(no) {
			t.Errorf("the allowlist accepts %q", no)
		}
	}
}

// The bound is a refusal to mint and it names the field.
func TestValidateMediaBeginRefusesTheFirstWrongFieldByName(t *testing.T) {
	const max = int64(26214400)
	digest := strings.Repeat("a", 64)

	str := func(s string) *string { return &s }
	num := func(n int64) *int64 { return &n }

	good := logbook.MediaBegin{SHA256: str(digest), ByteSize: num(10), ContentType: str("image/png")}
	if err := logbook.ValidateMediaBegin(good, max); err != nil {
		t.Fatalf("a well-formed begin was refused: %v — without this half every "+
			"case below is satisfied by a validator that rejects everything", err)
	}

	for _, c := range []struct {
		name  string
		body  logbook.MediaBegin
		field string
	}{
		{"no sha256 at all", logbook.MediaBegin{ByteSize: num(10), ContentType: str("image/png")}, "sha256"},
		{"an uppercase digest", logbook.MediaBegin{SHA256: str(strings.ToUpper(digest)), ByteSize: num(10), ContentType: str("image/png")}, "sha256"},
		{"a short digest", logbook.MediaBegin{SHA256: str("abc"), ByteSize: num(10), ContentType: str("image/png")}, "sha256"},
		{"no contentType at all", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(10)}, "contentType"},
		{"text/html", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(10), ContentType: str("text/html")}, "contentType"},
		{"image/heic", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(10), ContentType: str("image/heic")}, "contentType"},
		{"no byteSize at all", logbook.MediaBegin{SHA256: str(digest), ContentType: str("image/png")}, "byteSize"},
		{"zero bytes", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(0), ContentType: str("image/png")}, "byteSize"},
		{"negative bytes", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(-1), ContentType: str("image/png")}, "byteSize"},
		{"one byte over the bound", logbook.MediaBegin{SHA256: str(digest), ByteSize: num(max + 1), ContentType: str("image/png")}, "byteSize"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := logbook.ValidateMediaBegin(c.body, max)
			if err == nil {
				t.Fatalf("accepted %s", c.name)
			}
			var invalid logbook.InvalidFieldError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want an InvalidFieldError so the 422 can name a field", err)
			}
			if invalid.Field != c.field {
				t.Errorf("field = %q, want %q (%v)", invalid.Field, c.field, err)
			}
		})
	}

	atTheBound := logbook.MediaBegin{SHA256: str(digest), ByteSize: num(max), ContentType: str("image/png")}
	if err := logbook.ValidateMediaBegin(atTheBound, max); err != nil {
		t.Errorf("a photograph of exactly MEDIA_MAX_BYTES was refused: %v", err)
	}
}

func TestValidateMediaMintBoundsTheListAndNamesTheField(t *testing.T) {
	digest := strings.Repeat("a", 64)
	list := func(ids ...string) *[]string { return &ids }

	if err := logbook.ValidateMediaMint(logbook.MediaMint{IDs: list(digest)}); err != nil {
		t.Fatalf("a one-id mint was refused: %v", err)
	}

	many := make([]string, logbook.MaxMintIDs)
	for i := range many {
		many[i] = digest
	}
	if err := logbook.ValidateMediaMint(logbook.MediaMint{IDs: &many}); err != nil {
		t.Errorf("a mint of exactly MaxMintIDs was refused: %v", err)
	}
	over := append(many, digest)

	for _, c := range []struct {
		name string
		body logbook.MediaMint
	}{
		{"no ids key at all", logbook.MediaMint{}},
		{"an empty list", logbook.MediaMint{IDs: list()}},
		{"one id over the bound", logbook.MediaMint{IDs: &over}},
		{"an id that is not a digest", logbook.MediaMint{IDs: list(digest, "kyoto")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := logbook.ValidateMediaMint(c.body)
			if err == nil {
				t.Fatalf("accepted %s", c.name)
			}
			var invalid logbook.InvalidFieldError
			if !errors.As(err, &invalid) || invalid.Field != "ids" {
				t.Errorf("error = %v, want an InvalidFieldError naming `ids`", err)
			}
		})
	}
}
