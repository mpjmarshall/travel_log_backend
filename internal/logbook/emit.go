// The one emitter, and the two version numbers that are not the same number.
//
// THEY ARE EASY TO CONFUSE AND THEY ANSWER DIFFERENT QUESTIONS:
//
//	FormatVersion  is the WIRE's shape, stamped into the body as `version`
//	               (DEC-40). The client refuses a version it does not know, so
//	               moving it is a coordinated release and DEC-53's
//	               `X-Logbook-Format` is the negotiation.
//	EmitterVersion is the CODE's shape, and it never reaches the body at all —
//	               it is the first half of DEC-49's ETag. It moves when the
//	               emitted document changes in a way `logbook_version` cannot
//	               see, which is every deploy that touches this package.
//
// A change can move one, the other, or both. DEC-40's move to `"version": 2`
// is a change to what is emitted, so a later change of that kind moves
// EmitterVersion as well; a bug fix that changes no key still moves
// EmitterVersion and leaves FormatVersion alone.
//
// EMIT TAKES THE FORMAT VERSION AS A PARAMETER RATHER THAN READING THE
// CONSTANT, and today there is exactly one value it accepts. The parameter is
// what makes a second one possible without a second emitter — DEC-53's whole
// mechanism is that the server can say what it CAN emit, which is a list, and
// a function with the answer baked in cannot grow one. It refuses a version it
// cannot write rather than falling back to the one it can, because a fallback
// is DEC-40's refetch loop wearing a 200.
package logbook

import "errors"

// EmitterVersion is DEC-49's first half.
//
// BUMP IT BY HAND whenever this package changes what the document looks like.
// Without it a deploy that renames a key, adds a field or renders a date
// differently moves no data, so every phone holding a cached body gets 304 for
// ever and keeps serving the OLD SHAPE until somebody happens to write.
//
// IT IS 2 FROM R1, AND THAT INSTRUCTION IS WHY. It was 1, with the note "the
// shape is final at VS7 ... so the re-plan inherits it without a bump" — and
// then DEC-91 added `shared` to every emitted trip, which is precisely "adds a
// field". A phone holding a body cached under `W/"1-<n>"` must be told the
// shape moved even though its log did not, or H1 keeps reading a document with
// no `shared` in it and `Trip.isShared` is false on a trip that is shared.
//
// FormatVersion did NOT move with it: the key is additive with a default, and
// the client's own rule is that such a key needs no bump
// (lib/src/logbook/logbook_format.dart:14-18). The two numbers answer
// different questions and this is the change that shows it.
const EmitterVersion int64 = 2

// FormatVersion is DEC-40's `"version": 2`. The bump describes four fields
// whose MEANING changed — Photo.asset and the three coverAssets, each a bundle
// path before and an object id now (DEC-46) — and it costs nothing today
// because nothing has shipped against this backend.
const FormatVersion = 2

// ErrUnsupportedFormat is a request for a version this build cannot write.
// DEC-53 maps it to 406 with a header naming what it can.
var ErrUnsupportedFormat = errors.New("logbook: no emitter for that format version")

// Envelope is the client's own two keys, and `decodeLogbook` reads exactly
// this: a `version` it compares against its own constant, and a `logbook`
// object it hands to the generated codec.
type Envelope struct {
	Version int      `json:"version"`
	Logbook Document `json:"logbook"`
}

// Emit renders one document at the named format version.
//
// IT NORMALISES EVERY LIST, AND THAT IS THE WHOLE BODY OF THE FUNCTION FOR A
// REASON. A nil slice marshals to `null`, not `[]`, and the client's decoder
// reads `logbook.trips` as `as List<dynamic>` with no null branch — so four
// unimplemented lists left nil would not be "empty rather than absent", they
// would be the one shape that throws. Normalising here rather than at each of
// the five store queries means a query that returns no rows cannot get it
// wrong, and a sixth list added later inherits the rule by being in this
// function.
func Emit(formatVersion int, doc Document) (Envelope, error) {
	if formatVersion != FormatVersion {
		return Envelope{}, ErrUnsupportedFormat
	}

	doc.Trips = orEmpty(doc.Trips)
	doc.Cities = orEmpty(doc.Cities)
	doc.Places = orEmpty(doc.Places)
	doc.Photos = orEmpty(doc.Photos)
	doc.Walks = orEmpty(doc.Walks)
	for i := range doc.Trips {
		doc.Trips[i] = EmitTrip(doc.Trips[i])
	}
	for i := range doc.Places {
		doc.Places[i].Visits = orEmpty(doc.Places[i].Visits)
	}
	for i := range doc.Walks {
		doc.Walks[i].Points = orEmpty(doc.Walks[i].Points)
	}

	return Envelope{Version: formatVersion, Logbook: doc}, nil
}

// EmitTrip is the same normalisation Emit applies to a trip inside the
// document, for the ONE route that answers a bare entity: DEC-32's write
// response, which the phone splices into its cached log rather than
// re-fetching 85 KB.
//
// (That "85 KB" was the CLIENT's file on disk. Through this build the same log
// emits 95,586 bytes once DEC-46's object ids are in it — measured, see
// TestTheEmittedSizeIsLargerThanTheClientsFileAndSaysBySoMuch. The argument
// does not change; the number is bigger and grows with the photograph count.)
//
// IT EXISTS BECAUSE THE WRITE PATH DOES NOT GO THROUGH Emit AND THAT COST A
// DEFECT. Measured against the running server before the fix:
// `PUT /v1/trips/kyoto` with an empty cityIds answered
// `"cityIds":null`, which the client reads as `(json['cityIds'] as
// List<dynamic>)` — non-nullable — and throws on. The GET was correct the
// whole time, because Emit normalises; the two paths had one rule and one
// implementation of it.
//
// It takes no format version, unlike Emit. The write's answer is a splice into
// a document the client fetched at a version it has already negotiated, so
// there is no second shape to choose between. The day a trip looks different
// at two format versions, this grows the parameter Emit already has.
func EmitTrip(t Trip) Trip {
	t.CityIDs = orEmpty(t.CityIDs)
	return t
}

// Formats is what a 406 names: every version this build can write.
func Formats() []int { return []int{FormatVersion} }

func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
