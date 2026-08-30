// N1's two walk writes, as a contract.
package logbook_test

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

func walkID(id string) *string { return &id }

// track builds n points at FULL float64 PRECISION, and that choice is the
// single thing every byte count below depends on.
func track(n int) []logbook.LatLng {
	random := rand.New(rand.NewSource(7))
	points := make([]logbook.LatLng, n)
	for i := range points {
		points[i] = logbook.LatLng{Lat: 34 + random.Float64(), Lng: 135 + random.Float64()}
	}
	return points
}

// roundedTrack is the same track at seven decimal places — about a
// centimetre, and far finer than anything C2 draws.
func roundedTrack(n int) []logbook.LatLng {
	points := track(n)
	for i, point := range points {
		points[i] = logbook.LatLng{
			Lat: math.Round(point.Lat*1e7) / 1e7,
			Lng: math.Round(point.Lng*1e7) / 1e7,
		}
	}
	return points
}

// the cap is 500 and the pair is the leg.
func TestValidateWalkRefuses501PointsByNameAndAccepts500(t *testing.T) {
	if logbook.MaxWalkPoints != 500 {
		t.Fatalf("MaxWalkPoints = %d. DEC-106 confirmed 500 and stopped it being "+
			"provisional; moving it is a ruling and not an edit",
			logbook.MaxWalkPoints)
	}

	tooMany := track(logbook.MaxWalkPoints + 1)
	err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Points: &tooMany})

	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("%d points = %v, want an InvalidFieldError", len(tooMany), err)
	}
	if invalid.Field != "points" {
		t.Errorf("the field = %q, want \"points\" — `http.MaxBytesReader` answers "+
			"ErrBodyTooLarge with NO field on it, which is why DEC-93 says the cap "+
			"lives here", invalid.Field)
	}

	exactly := track(logbook.MaxWalkPoints)
	if err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Points: &exactly}); err != nil {
		t.Errorf("%d points = %v, want nil. Without this half the leg above passes "+
			"against a cap of zero", len(exactly), err)
	}
}

// The cap is far inside the body ceiling while an uncapped six-hour walk
// is over it — measured through the shipped types.
func TestTheCapIsFarInsideTheBodyCeilingAndSixHoursIsNot(t *testing.T) {
	capped := walkBytes(t, track(logbook.MaxWalkPoints))
	t.Logf("%6d points = %9d B   (%.0fx inside the %d B ceiling)",
		logbook.MaxWalkPoints, capped, float64(httpx.MaxBodyBytes)/float64(capped),
		httpx.MaxBodyBytes)

	t.Logf("%6d points = %9d B", logbook.MaxWalkPoints+1, walkBytes(t, track(logbook.MaxWalkPoints+1)))

	const sixHoursAt1Hz = 21600
	overLong := walkBytes(t, track(sixHoursAt1Hz))
	t.Logf("%6d points = %9d B   (six hours at 1 Hz)", sixHoursAt1Hz, overLong)

	if int64(capped)*10 > httpx.MaxBodyBytes {
		t.Errorf("a capped track is %d B against a %d B ceiling — under one order of "+
			"margin, so the cap is no longer the comfortable number DEC-106 confirmed",
			capped, httpx.MaxBodyBytes)
	}
	if int64(overLong) <= httpx.MaxBodyBytes {
		t.Errorf("an UNCAPPED six-hour walk is %d B and fits inside the %d B ceiling, so "+
			"this leg's premise is gone: the cap exists because the user's LONGEST "+
			"walk was the one write that silently could not be saved",
			overLong, httpx.MaxBodyBytes)
	}
}

// The byte count of A TRACK is a claim about coordinate precision, which
// is why the input above is unrounded.
func TestTheByteCountOfATrackIsAClaimAboutCoordinatePrecision(t *testing.T) {
	const sixHoursAt1Hz = 21600
	unrounded := walkBytes(t, track(sixHoursAt1Hz))
	rounded := walkBytes(t, roundedTrack(sixHoursAt1Hz))
	t.Logf("%d points: full precision %d B (%.1f/pt), 7 dp %d B (%.1f/pt)",
		sixHoursAt1Hz, unrounded, float64(unrounded)/sixHoursAt1Hz,
		rounded, float64(rounded)/sixHoursAt1Hz)

	if rounded >= unrounded {
		t.Fatalf("rounding to 7 dp did not shorten the body (%d -> %d), so this leg's "+
			"premise is gone", unrounded, rounded)
	}
	if int64(rounded) > httpx.MaxBodyBytes {
		t.Errorf("the ROUNDED six-hour track is %d B and already exceeds the %d B "+
			"ceiling. That is a stronger world than the one measured here and the "+
			"cap still holds — but the sentence above is now wrong and should be "+
			"re-derived rather than left standing", rounded, httpx.MaxBodyBytes)
	}
	if int64(unrounded) <= httpx.MaxBodyBytes {
		t.Errorf("the UNROUNDED six-hour track is %d B and fits inside %d B, which is "+
			"not what DEC-93 measured (1,099,390 B). Re-derive the ruling's premise "+
			"before trusting any size argument in this file", unrounded, httpx.MaxBodyBytes)
	}
}

func walkBytes(t *testing.T, points []logbook.LatLng) int {
	t.Helper()
	raw, err := json.Marshal(logbook.Walk{
		ID: "w-busan", TripID: "autumn-crossing", CityID: "busan",
		DistanceKm: 6.4, Points: points,
	})
	if err != nil {
		t.Fatalf("marshalling %d points: %v", len(points), err)
	}
	return len(raw)
}

// an empty track is refused, and the other half says A one-point track is
// not.
func TestValidateWalkRefusesAnEmptyTrackAndNotAShortOne(t *testing.T) {
	empty := []logbook.LatLng{}
	err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Points: &empty})

	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("points: [] = %v, want an InvalidFieldError", err)
	}
	if invalid.Field != "points" {
		t.Errorf("the field = %q, want \"points\"", invalid.Field)
	}
	if !strings.Contains(invalid.Why, "OMIT") {
		t.Errorf("the reason does not say what to send instead: %q. A client that meant "+
			"'leave the track alone' has to be told the key is omitted, or it will "+
			"read the refusal as 'this walk cannot be written'", invalid.Why)
	}

	one := track(1)
	if err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Points: &one}); err != nil {
		t.Errorf("a one-point track = %v, want nil. Without this half the leg above "+
			"passes against a build that refuses every track", err)
	}
}

// An absent `points` key is not an empty one, which is the whole of
// saf-MAJ-6 at the validator.
func TestADismissedOnlyBodyPassesValidationWithNoTrackInIt(t *testing.T) {
	var body logbook.WalkWrite
	if err := json.Unmarshal([]byte(`{"dismissed":true}`), &body); err != nil {
		t.Fatalf("decoding N1's Discard: %v", err)
	}
	if body.Points != nil {
		t.Fatalf("`{\"dismissed\":true}` decoded to Points=%v, want nil — absent and "+
			"empty have to be different values or the rest of this contract is "+
			"decoration", *body.Points)
	}
	if body.Dismissed == nil || !*body.Dismissed {
		t.Fatalf("dismissed = %v, want true", body.Dismissed)
	}

	body.ID = walkID("w-busan")
	if err := logbook.ValidateWalk(body); err != nil {
		t.Errorf("N1's Discard = %v, want nil", err)
	}
}

// an empty name is refused, and it is the one place this api deliberately
// disagrees with itself.
func TestValidateWalkRefusesAnEmptyNameRatherThanClearingIt(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n"} {
		sent := &name
		err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Name: &sent})

		var invalid logbook.InvalidFieldError
		if !errors.As(err, &invalid) || invalid.Field != "name" {
			t.Errorf("name=%q gave %v, want an InvalidFieldError naming \"name\"", name, err)
		}
	}

	named := "Kibune river walk"
	sent := &named
	if err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-kibune"), Name: &sent}); err != nil {
		t.Errorf("a real name = %v, want nil", err)
	}
}

// A point outside the world is refused naming `points` and not `coordinates`.
func TestAPointOutsideTheWorldNamesThePointsFieldAndSaysWhichPoint(t *testing.T) {
	points := track(3)
	points[2].Lat = 91

	err := logbook.ValidateWalk(logbook.WalkWrite{ID: walkID("w-busan"), Points: &points})
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) {
		t.Fatalf("a latitude of 91 = %v, want an InvalidFieldError", err)
	}
	if invalid.Field != "points" {
		t.Errorf("the field = %q, want \"points\" — this body has no `coordinates` key",
			invalid.Field)
	}
	if !strings.Contains(invalid.Why, "point 2") {
		t.Errorf("the reason does not say which point: %q", invalid.Why)
	}
}
