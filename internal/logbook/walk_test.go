// N1's two walk writes, as a contract. No database.
//
// WHAT IS HERE IS THE SHAPE OF A REQUEST AND NOTHING ELSE. Whether a walk's
// points SURVIVE a `{dismissed:true}` body is a fact about a statement, so it
// is in internal/postgres and internal/seed and is not repeated here — R5's
// rule, paid for: "a leg over a twin cannot guard a statement the twin does
// not execute".
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
//
// encoding/json writes a float in its shortest round-tripping form, so how
// long a point is on the wire is decided entirely by how many significant
// digits the value carries. A device's location plugin hands out a `double`
// and the client's `LatLng` stores it unrounded, so the honest input is an
// unrounded one — measured, 50.9 bytes a point, against 36.8 for the same
// track rounded to seven decimal places. TestTheByteCountOfATrackIsAClaim
// AboutCoordinatePrecision holds both numbers, because the difference is large
// enough to move a conclusion.
func track(n int) []logbook.LatLng {
	// A FIXED SEED AND NOT time.Now, so a failure message is reproducible —
	// the reason internal/seed pins its traveller uuid.
	random := rand.New(rand.NewSource(7))
	points := make([]logbook.LatLng, n)
	for i := range points {
		points[i] = logbook.LatLng{Lat: 34 + random.Float64(), Lng: 135 + random.Float64()}
	}
	return points
}

// roundedTrack is the same track at seven decimal places — about a centimetre,
// and far finer than anything C2 draws.
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

// THE CAP IS 500 AND THE PAIR IS THE LEG (DEC-93, DEC-106).
//
// A CAP ASSERTED ONLY FROM ABOVE PASSES AGAINST A CAP OF ZERO, so the leg that
// refuses 501 says in the same breath that 500 is accepted. Raise the constant
// to 21,600 and the refusal half reddens while the acceptance half does not —
// which is the mutation the plan names, and it only means something because
// both halves are here.
func TestValidateWalkRefuses501PointsByNameAndAccepts500(t *testing.T) {
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

// AND THE CAP IS FAR INSIDE THE BODY CEILING WHILE AN UNCAPPED SIX-HOUR WALK
// IS OVER IT — MEASURED THROUGH THE SHIPPED TYPES, WHICH IS THE ONLY
// MEASUREMENT THAT ANSWERS THE QUESTION.
//
// This is the leg MaxWalkPoints' own comment quotes, so those numbers cannot
// go stale without something going red.
//
// ONE FIGURE IN DEC-106 DOES NOT SURVIVE BEING RECOMPUTED AND THE CONCLUSION
// IS UNTOUCHED. The ruling says a 500-point track is "roughly 26 KB, TWO
// ORDERS OF MAGNITUDE inside the 1 MiB body ceiling". The size is right —
// 25,629 B here — and the multiplier is not: 1,048,576 / 25,629 is 41, which
// is between one order and two. So this leg asserts the ratio it can actually
// measure and LOGS it, rather than carrying a word that reads like a
// measurement and is not one.
func TestTheCapIsFarInsideTheBodyCeilingAndSixHoursIsNot(t *testing.T) {
	capped := walkBytes(t, track(logbook.MaxWalkPoints))
	t.Logf("%6d points = %9d B   (%.0fx inside the %d B ceiling)",
		logbook.MaxWalkPoints, capped, float64(httpx.MaxBodyBytes)/float64(capped),
		httpx.MaxBodyBytes)

	// 21,600 IS THE NUMBER THE RULING TURNS ON: a six-hour walk at 1 Hz, which
	// is a walk somebody really takes.
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

// AND THE BYTE COUNT OF A TRACK IS A CLAIM ABOUT COORDINATE PRECISION, WHICH
// IS WHY THE INPUT ABOVE IS UNROUNDED.
//
// MEASURED AT THIS COMMIT, 21,600 points:
//
//	full float64 precision   1,099,622 B   OVER the 1,048,576 B ceiling
//	seven decimal places       794,666 B   INSIDE it
//
// The first reproduces DEC-93's own 1,099,390 B to within the walk's scalar
// fields, so the ruling was measured on unrounded coordinates — which is what
// a location plugin actually hands out. The second is the same 21,600 fixes
// rounded to about a centimetre, and it FITS.
//
// THAT DOES NOT REOPEN THE CAP, AND SAYING SO IS THE POINT OF WRITING IT DOWN.
// The body ceiling is one of DEC-93's two arguments and not the load-bearing
// one: the other is that tracks, not photographs, are the whole-log read's
// real growth term, and that they compress at 4.4-6.2x against 15.6-17.5x for
// the rest of the document. What this leg stops is somebody re-deriving "it
// fits" from a rounded fixture and concluding the cap is unnecessary.
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

// AN EMPTY TRACK IS REFUSED, AND THE OTHER HALF SAYS A ONE-POINT TRACK IS NOT.
//
// BOTH HALVES ARE IN ONE LEG ON PURPOSE, which is DEC-109's lesson applied
// before it could be re-learned: from the refusing side, a guard that cannot
// tell two cases apart looks identical to a correct one. R6 shipped exactly
// that on `visits: []` and it took a ruling to narrow.
//
// THE ANSWER IS DIFFERENT HERE AND THE REASON IS IN walk.go: there is no
// wishlist walk. `walks.points` is NOT NULL and 0003 bounds it below, so no
// stored walk has an empty track, `Emit` cannot produce one, and every empty
// array reaching this route IS the destruction rather than a shape the model
// holds. The half of that claim only a database can check —
// `count(*) FROM walks WHERE jsonb_array_length(points) = 0` — is asserted in
// internal/seed against the client's own log.
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

// AND AN ABSENT `points` KEY IS NOT AN EMPTY ONE, WHICH IS THE WHOLE OF
// SAF-MAJ-6 AT THE VALIDATOR.
//
// The body is the one N1's Discard actually sends. `Points` is a POINTER to a
// slice precisely so this case exists to be tested: a bare `[]LatLng` makes
// absent and empty the same value, and the empty one destroys a recording of a
// day that cannot be re-recorded.
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

// AN EMPTY NAME IS REFUSED, AND IT IS THE ONE PLACE THIS API DELIBERATELY
// DISAGREES WITH ITSELF.
//
// `setPhotoCaption` clears on empty and `setWalkName` refuses on empty, and
// the client states the reason: `Walk.needsNaming` is `name == null &&
// !dismissed`, so an empty name puts the row straight back on N1 with no way
// to tell it from a walk nobody has ever named. N1 already has the control for
// that and it is called 'Discard'.
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

// A POINT OUTSIDE THE WORLD IS REFUSED NAMING `points` AND NOT `coordinates`.
//
// The body has no `coordinates` key, so forwarding checkLatLng's own field
// would tell a client to fix a key its request never carried —
// `requireCityForPlace` makes the same move for the same reason. The index is
// in the message because a client with 500 points needs to know which one.
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
