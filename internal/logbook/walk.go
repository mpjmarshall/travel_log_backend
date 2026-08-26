// What a walk write may contain, and the ONE field in this API that cannot be
// re-recorded if it is lost.
//
// A WALK IS ITS OWN FILE AND NOT geography.go's THIRD ENTITY. That file holds
// a city and a place together because their writes are one contract —
// `PlaceWrite.CityID` names a city, `CityWrite.AttachTo` names a trip, and the
// refusals reference each other. A walk references neither. It has no
// `place_id` AT ALL, and that absence is D2's "the track stays with the day it
// was recorded either way": `removePlace` cannot touch a walk because there is
// nothing on the row for it to reach. Putting the two writes in one file would
// suggest a relationship the schema deliberately does not have.
//
// EVERY FIELD IS A POINTER, BECAUSE ABSENT MEANS LEAVE ALONE (DEC-89), and on
// `points` that ruling is the difference between a flag being set and a day
// being destroyed.
//
//	MEASURED by the safety lens against the client's own fixture on
//	postgres:17.11, running the whole-state convention on N1's Discard:
//
//	  UPDATE walks SET dismissed=true, points='[]'::jsonb WHERE id='w-busan'
//	  -> UPDATE 1, no constraint raised
//	  jsonb_array_length(points)  3 -> 0
//
//	The row survives, the read returns it, `dismissed` is correct, and C2
//	draws nothing. `walks_points_array_ck CHECK (jsonb_typeof(points) =
//	'array')` does not stop it, because AN EMPTY ARRAY IS AN ARRAY.
//
// SO THE LOWER BOUND IS A SCHEMA CONSTRAINT AND NOT ONLY A GO RULE.
// Migration 0003's `walks_points_present_ck` refuses an empty array outright
// (SAF-MAJ-6, PD-21), and what is here is the 422 that names the field —
// DEC-58's precedent, the same division `assetPattern` and
// `photos_asset_sha256_ck` already have.
//
// AND `points: []` IS REFUSED ON SHAPE HERE, WHICH IS NOT WHAT DEC-109 DID TO
// `visits: []` ONE ROUTE OVER. The narrowing there existed because an empty
// visits array is a request the model can legitimately hold: nine of the
// client's seventeen places are wishlist places, `EmitPlace` writes
// `"visits": []` for every one of them, and the server was refusing a document
// it had itself produced. There is no wishlist walk. `walks.points` is NOT
// NULL and 0003 bounds it below, so no stored walk has ever had an empty
// track, `Emit` cannot produce one, and there is no state this refusal is
// wrong about. Refuse the destruction, not the shape — and here every empty
// array IS the destruction. THE LEG CARRIES BOTH HALVES: the refusal, and the
// count of walks in the seeded log carrying zero points, which is 0.
package logbook

import (
	"errors"
	"fmt"
)

// MaxWalkPoints is DEC-93 as confirmed by DEC-106: five hundred, and it stops
// being provisional.
//
// IT IS ENFORCED HERE AND NOT BY http.MaxBytesReader, WHICH IS THE WHOLE OF
// THE RULING'S SECOND SENTENCE. `httpx.MaxBodyBytes` answers
// `ErrBodyTooLarge`, which carries no field name at all, so a client whose
// track is too long is told "your request is too big" about a body it cannot
// see the shape of. A 422 naming `points` is a client that knows to decimate.
//
// MEASURED THROUGH THE SHIPPED TYPES AT THIS COMMIT, on UNROUNDED coordinates
// — which is what a location plugin hands out, and the single thing the byte
// count is most sensitive to (walk_test.go holds both this leg and the one
// that shows the sensitivity):
//
//	   500 points     25,629 B   — 41x inside httpx.MaxBodyBytes
//	21,600 points  1,099,622 B   — a six-hour walk at 1 Hz, and OVER 1 MiB
//
// The second reproduces DEC-93's own 1,099,390 B to within the walk's scalar
// fields, so the ruling was measured the same way. ONE FIGURE OF DEC-106's
// DOES NOT SURVIVE RECOMPUTING AND THE CONCLUSION IS UNTOUCHED: it calls
// 26 KB "TWO ORDERS OF MAGNITUDE inside" the ceiling, and 1,048,576 / 25,629
// is 41 — between one order and two. The number that matters is the other
// one: uncapped, the user's LONGEST walk is the one write that silently
// cannot be saved. The client decimating before it sends is a CLIENT-PREREQUISITES item
// (§10) and not something this constant can do: the server refusing an
// over-long track is not the same as the phone knowing to shorten one, and a
// user whose walk is refused has lost a recording of a day.
const MaxWalkPoints = 500

// WalkWrite is the body of `PUT /v1/walks/{id}`: N1's 'Name it', N1's
// 'Discard', and the create a client-minted key makes possible (DEC-33).
//
// `Points` IS A POINTER TO A SLICE AND THE INDIRECTION IS THE WHOLE FEATURE,
// exactly as it is on `PlaceWrite.Visits`. A bare `[]LatLng` makes absent and
// empty the same value, and here the two mean "leave the recording alone" and
// "destroy the recording".
//
// `Name` IS A DOUBLE POINTER AND `Dismissed` IS A SINGLE ONE, and which is
// which is read off the schema rather than chosen. `walks.name` is nullable,
// so it carries the three states TripWrite's `**T` fields do — with the same
// measured caveat that the third is reachable from Go and not over the wire.
// `walks.dismissed` is NOT NULL DEFAULT false, so there is no sent-as-null
// state for it to hold.
//
// AN EMPTY NAME IS REFUSED AND IS NOT A WAY TO CLEAR ONE, and that is
// deliberately NOT what a photograph's caption does. The client says why in
// terms: `Walk.needsNaming` is `name == null && !dismissed`, so storing `”`
// or null would put the row straight back on N1 with no way to tell it from a
// walk nobody has ever named — and N1 already carries the control for "I am
// not naming this". It is called 'Discard', it is `dismissWalk`, and it is
// permanent on purpose.
type WalkWrite struct {
	ID         *string   `json:"id"`
	TripID     *string   `json:"tripId"`
	CityID     *string   `json:"cityId"`
	RecordedOn *Instant  `json:"recordedOn"`
	DistanceKm *float64  `json:"distanceKm"`
	Points     *[]LatLng `json:"points"`
	Name       **string  `json:"name"`
	Dismissed  *bool     `json:"dismissed"`
}

// ValidateWalk answers the first field that is wrong, and nothing about
// whether the ids it names exist.
//
// EXISTENCE IS THE STORE'S, under the traveller's advisory lock, for the
// reason ValidateTrip gives: a check made out here is a check made against a
// database that can move underneath it. And "a walk needs a trip" is not here
// either — absent is legal on an UPDATE and impossible on a CREATE, and only
// the store knows which it is holding.
func ValidateWalk(w WalkWrite) error {
	if w.ID == nil || !idPattern.MatchString(*w.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if w.TripID != nil && !idPattern.MatchString(*w.TripID) {
		return InvalidFieldError{Field: "tripId",
			Why: fmt.Sprintf("%q is not an id", *w.TripID)}
	}
	if w.CityID != nil && !idPattern.MatchString(*w.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *w.CityID)}
	}
	if w.DistanceKm != nil && *w.DistanceKm < 0 {
		// The Go half of `walks_distance_km_ck`. The CHECK is still what
		// enforces it; this is what names the field first.
		return InvalidFieldError{Field: "distanceKm",
			Why: fmt.Sprintf("a distance is not negative, and %v is", *w.DistanceKm)}
	}
	if name := Value(w.Name); name != nil {
		if err := checkName(*name, "name", "a walk's name is what takes it off N1, "+
			"and an empty one is not a way to clear it — N1's control for that is "+
			"'Discard', which is `dismissed`"); err != nil {
			return err
		}
	}
	if w.Points != nil {
		if err := checkPoints(*w.Points); err != nil {
			return err
		}
	}
	return nil
}

// checkPoints is DEC-106's cap and 0003's lower bound, in that order, and the
// pair is what makes the leg over it falsifiable.
//
// A CAP ASSERTED ONLY FROM ABOVE PASSES AGAINST A CAP OF ZERO. So the leg that
// refuses 501 asserts in the same breath that 500 is accepted, and the leg
// that refuses `[]` asserts that a one-point track is not refused.
func checkPoints(points []LatLng) error {
	if len(points) == 0 {
		return InvalidFieldError{Field: "points",
			Why: "an empty track is a request to destroy a recording of a day that has " +
				"passed, and nothing can re-record it. No control in the client asks " +
				"for that — N1's 'Discard' sets `dismissed` and keeps the track. OMIT " +
				"the key to leave the points alone"}
	}
	if len(points) > MaxWalkPoints {
		return InvalidFieldError{Field: "points",
			Why: fmt.Sprintf("%d points, and this build takes at most %d — decimate the "+
				"track before sending it; C2 draws a polyline and does not need every fix",
				len(points), MaxWalkPoints)}
	}
	for i, point := range points {
		// THE FIELD IS `points` AND NOT `coordinates`, WHICH IS WHY THIS DOES
		// NOT SIMPLY FORWARD checkLatLng's ERROR. `requireCityForPlace` makes
		// the same move for the same reason: a client can only act on which of
		// ITS OWN fields is wrong, and this body has no `coordinates` key.
		var invalid InvalidFieldError
		if err := checkLatLng(point, "points"); errors.As(err, &invalid) {
			return InvalidFieldError{Field: "points",
				Why: fmt.Sprintf("point %d: %s", i, invalid.Why)}
		} else if err != nil {
			return err
		}
	}
	return nil
}
