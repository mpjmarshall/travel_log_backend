// What a trip write may contain, and the compiled regexps that decide it
// (spec L23: "Perform input validation using compiled `regexp` patterns" —
// compiled once at package scope, never inside a handler).
//
// THE WRITE OWNS SEVEN FIELDS AND THE TYPE IS HOW IT OWNS THEM. SF6 says the
// four sharing fields — shareLinkId, sharePhotos, shareNotes,
// shareCoordinates — are never written here, and TripWrite has no slot for
// them, so that is a fact about the type rather than a rule the upsert has to
// remember. DEC-13 keeps DisallowUnknownFields OFF, so a client that sends
// them is not refused: it is simply not heard, which is the right answer to a
// key this route does not own.
//
// TWO OF THE CEILINGS ARE THIS BUILD'S OWN POLICY AND ARE MARKED AS SUCH, in
// the same sense auth.MinPassphraseBytes is. Nothing in the schema bounds a
// trip's name or its summary — both are `text` — so without them a
// one-megabyte name is storable and then re-emitted on every read of the whole
// log, for ever. They are constants so that changing one is one constant and
// one leg.
//
// THE DUPLICATE-CITY CHECK IS NOT FASTIDIOUSNESS. trip_cities' primary key is
// (traveller_id, trip_id, city_id), so a repeated id violates it on the second
// INSERT of the delete-then-insert — and reaches the client as a 500 with
// nothing to act on rather than as a named field.
//
// AND EVERY FIELD IS A POINTER, BECAUSE ABSENT MEANS LEAVE ALONE (DEC-89).
// That is the whole of the wire contract this file declares; the statement
// that honours it is in internal/postgres. See TripWrite below for what it
// cost to learn.
package logbook

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxNameBytes and MaxSummaryBytes are policy, not schema.
const (
	MaxNameBytes    = 200
	MaxSummaryBytes = 4096
)

// idPattern is DEC-02's rule, verified against all 43 ids in the client's own
// log: permissive, because the ids are the CLIENT's and must round-trip. The
// generator's twelve-character alphabet is a property of the generator and was
// never a constraint on the log.
var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// assetPattern is DEC-38's content address: lowercase hex, sixty-four
// characters. It is the same expression as media_objects_id_sha256_ck, and it
// is here as well as there because a foreign-key violation reaching the client
// is a 500 with no field on it.
var assetPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// InvalidFieldError is what DEC-12's one additive key, `field`, is built from.
type InvalidFieldError struct{ Field, Why string }

func (e InvalidFieldError) Error() string { return "logbook: " + e.Field + ": " + e.Why }

// TripWrite is the body of PUT /v1/trips/{id}, and EVERY FIELD IS A POINTER
// BECAUSE ABSENT MEANS LEAVE ALONE (DEC-89).
//
// IT USED TO BE A WHOLE-STATE UPSERT AND THAT ANSWERED 200 WHILE DESTROYING AN
// ITINERARY. Measured against a real build at 89fc93f, and again from outside
// the test binary at R1 entry: PUT /v1/trips/autumn with three cities and both
// dates leaves trip_cities at kyoto:0/osaka:1/seoul:2; a body of `{id, name}`
// — which is EXACTLY what T4's pencil sends, because renameTrip owns the name
// and nothing else — then answers 200 with `"cityIds":[],"start":null,
// "end":null` and `SELECT count(*) FROM trip_cities` -> 0.
//
// THE AUTHOR HAD ALREADY REASONED THIS OUT ONE LINE ABOVE THE STATEMENT, for
// three columns: share_photos, share_notes and share_coordinates are left out
// of the SET clause because "naming them in EXCLUDED-form would silently reset
// a group this route does not own". Five columns on the same statement got the
// other answer, because `CityIDs []string` made absence and emptiness the same
// value and `checkCityIDs(nil)` loops zero times.
//
// THE DOUBLE POINTERS ARE NOT DECORATION, AND WHAT THEY BUY IS SMALLER THAN IT
// LOOKS. A `*T` field already distinguishes sent from absent for a
// non-nullable field. `**T` is what lets a NULLABLE field carry three states —
// absent, sent-as-null, sent-with-a-value — in Go. ON THE WIRE IT CARRIES TWO,
// AND THAT IS MEASURED RATHER THAN ASSUMED: encoding/json's `indirect` breaks
// at the outermost SETTABLE pointer when the literal is null, so
// `{"summary":null}` sets the `**string` field itself to nil and is
// indistinguishable from omitting the key. Probed on go1.26 against this
// module's own directive:
//
//	{"name":"n"}                   -> summary=nil
//	{"name":"n","summary":null}    -> summary=nil
//	{"name":"n","summary":"s"}     -> summary=value s
//
// So today a client cannot CLEAR a summary, a cover or a date over HTTP, and
// nothing in the client can ask to: T4's pencil renames, T4's Add dates sets,
// and there is no summary or cover editor anywhere in it. The third state is
// reachable from Go — the seed and the store tests use it — and the day a
// control needs to clear a field over the wire, the answer is an explicit
// sentinel in the body and not a change to this type. Recorded here rather
// than discovered by whoever writes that control.
type TripWrite struct {
	ID         *string   `json:"id"`
	Name       *string   `json:"name"`
	CityIDs    *[]string `json:"cityIds"`
	Start      **Instant `json:"start"`
	End        **Instant `json:"end"`
	Summary    **string  `json:"summary"`
	CoverAsset **string  `json:"coverAsset"`
}

// Sent answers whether a nullable field was in the body at all, which is the
// question every CASE WHEN in the upsert asks. It is a function rather than a
// method so it can be spelled once for all four pointer-to-pointer fields.
func Sent[T any](p **T) bool { return p != nil }

// Value flattens a sent nullable field to the value the statement writes: nil
// for a sent null, the pointer otherwise. A field that was NOT sent never
// reaches this — the CASE WHEN keeps the stored value instead.
func Value[T any](p **T) *T {
	if p == nil {
		return nil
	}
	return *p
}

// ValidateTrip answers the first field that is wrong, and nothing about
// whether the ids it names exist. Existence is the store's, under the
// traveller's advisory lock, because a check made out here is a check made
// against a database that can move underneath it.
//
// IT VALIDATES WHAT WAS SENT AND NOTHING ELSE (DEC-89). A field that is not in
// the body has no value to be wrong, so `name` is refused for being empty and
// not for being absent. THE ONE RULE THAT MOVED OUT OF HERE ENTIRELY is "a
// trip needs a name": absent is legal on an UPDATE and impossible on a CREATE,
// and only the store knows which it is holding. It is enforced under the
// advisory lock, beside the city and cover existence checks, for the same
// reason they are there.
//
// AND THE DATE ORDERING IS ONLY HALF ANSWERABLE HERE, which is new. `end`
// before `start` is refused when BOTH are in the body; a body carrying only
// one of them has to be checked against the stored other, or a partial write
// trips trips_dates_ordered_ck and reaches the client as a 500 with no field
// on it. That half is the store's too.
func ValidateTrip(t TripWrite) error {
	if t.ID == nil || !idPattern.MatchString(*t.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if t.Name != nil {
		if strings.TrimSpace(*t.Name) == "" {
			return InvalidFieldError{Field: "name", Why: "a trip needs a name"}
		}
		if len(*t.Name) > MaxNameBytes {
			return InvalidFieldError{Field: "name",
				Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*t.Name), MaxNameBytes)}
		}
	}
	if summary := Value(t.Summary); summary != nil && len(*summary) > MaxSummaryBytes {
		return InvalidFieldError{Field: "summary",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*summary), MaxSummaryBytes)}
	}
	if t.CityIDs != nil {
		if err := checkCityIDs(*t.CityIDs); err != nil {
			return err
		}
	}
	if cover := Value(t.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	start, end := Value(t.Start), Value(t.End)
	if start != nil && end != nil && end.Time().Before(start.Time()) {
		return InvalidFieldError{Field: "end", Why: "a trip cannot end before it starts"}
	}
	return nil
}

func checkCityIDs(ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return InvalidFieldError{Field: "cityIds", Why: fmt.Sprintf("%q is not an id", id)}
		}
		if seen[id] {
			return InvalidFieldError{Field: "cityIds",
				Why: fmt.Sprintf("%q appears twice, and a trip visits a city once in its ordered list", id)}
		}
		seen[id] = true
	}
	return nil
}

// ---------------------------------------------------------------- MEDIA (R3)

// allowedContentTypes IS THE ONE RUNTIME LIST, and everything else about the
// allowlist is derived from it (DEC-51 as narrowed by DEC-104, PD-10).
//
// TWO ENFORCEMENT POINTS AND ONE LIST. The Go check exists to produce a
// `422 invalid_field` naming the field; migration 0003's
// `media_objects_content_type_ck` is the guarantee, on DEC-58's precedent —
// a Go check can be bypassed by the next route somebody adds and nothing
// notices. Both spell the same two values, and
// TestTheSchemaAllowlistAndTheGoAllowlistAreTheSameSet walks the CHECK's own
// predicate out of pg_constraint and compares the sets in BOTH directions, so
// they cannot drift.
//
// AND A THIRD READER THAT IS NOT AN ENFORCEMENT POINT: DEC-87 SIGNS this value
// into the presigned PUT, sourced from the SAME validated string, because the
// 422 and the CHECK both constrain the database ROW while the uploader sets
// Content-Type on the PUT and the bucket keeps it. Measured: a row reading
// `image/png` can address an object the bucket serves as `text/html`.
//
// `heic` IS OUT AND `image/jpeg` STAYS, AND THE ASYMMETRY IS THE WHOLE ANSWER
// (DEC-104). Nothing in this system can produce a HEIC — the client's shutter
// is inert by decision, DEC-41 seeds two PNGs, and the fixture's 284
// photographs resolve to two `image/png` objects. `image/jpeg` earns its place
// independently of any camera: internal/postgres/schema_test.go seeds shared
// fixtures with it, so removing it breaks every leg using that helper. One of
// the three is reachable from the test suite today and one is reachable from
// nothing at all. The general form is worth more than the character: AN
// ALLOWLIST ENTRY NOTHING CAN PRODUCE OR TEST IS NOT A FREE OPTION — it is a
// claim the schema makes that no leg can check. It comes back with the real
// capture, which is the same dependency conversation the shutter already
// needs, and adding a media type is additive and does not move the format
// version.
var allowedContentTypes = []string{"image/jpeg", "image/png"}

// contentTypePattern is `^image/(jpeg|png)$` — spec L23's compiled regexp,
// compiled once at package scope and BUILT FROM the list above rather than
// written beside it.
//
// THE LIST AND THE EXPRESSION WOULD BE TWO SPELLINGS OF ONE SET, which is how
// every count in this project has gone wrong. So there is one literal and the
// expression is derived; what keeps the derivation honest is a leg asserting
// this pattern's String() is exactly `^image/(jpeg|png)$`, which is the
// expression the plan names, in one falsifiable place.
var contentTypePattern = regexp.MustCompile(contentTypeExpression(allowedContentTypes))

// contentTypeExpression turns the allowlist into the anchored alternation.
//
// It factors the shared `image/` prefix out, so the expression a reader sees is
// the one the decision is written as rather than a machine-generated
// alternation of whole strings. A type outside that family would make the
// factoring wrong, which is why it is asserted rather than assumed: the leg
// that checks String() reddens the moment one is added.
func contentTypeExpression(allowed []string) string {
	const family = "image/"
	subtypes := make([]string, len(allowed))
	for i, mediaType := range allowed {
		subtypes[i] = strings.TrimPrefix(mediaType, family)
	}
	return "^" + family + "(" + strings.Join(subtypes, "|") + ")$"
}

// AllowedContentTypes is the allowlist, as a fresh slice each call — a caller
// holding this package's own backing array could reorder or rewrite it, which
// is the same reason httpx.Codes() copies.
func AllowedContentTypes() []string {
	out := make([]string, len(allowedContentTypes))
	copy(out, allowedContentTypes)
	return out
}

// ContentTypeAllowed is the one question every caller asks.
func ContentTypeAllowed(mediaType string) bool {
	return contentTypePattern.MatchString(mediaType)
}

// MediaBegin is the body of `POST /v1/media`, and every field is a pointer
// because absent means leave alone (DEC-89).
//
// ON A CREATE THERE IS NOTHING TO LEAVE ALONE, so all three are required — but
// the pointer is what lets the refusal NAME THE RIGHT FIELD. A bare `int64`
// makes an absent `byteSize` and `"byteSize": 0` the same value, so a client
// that forgot the key is told its photograph is too small, which is the
// defect DEC-89 was ruled about wearing a smaller hat.
type MediaBegin struct {
	SHA256      *string `json:"sha256"`
	ByteSize    *int64  `json:"byteSize"`
	ContentType *string `json:"contentType"`
}

// ValidateMediaBegin answers the first field that is wrong.
//
// THE ORDER IS DELIBERATE AND IT IS THE ORDER OF THE CAPABILITY. Every refusal
// here happens BEFORE anything is signed, which is what `maxBytes` is: an
// API-side refusal to MINT, taken before the capability exists (PD-20). It can
// never be a ceiling at the bucket — SigV4 signs an exact header VALUE, so
// what the signature pins is `== byteSize` and never `<= maxBytes`. Both
// sentences are needed and neither is true alone.
//
// `maxBytes` is a PARAMETER rather than a constant because it is
// MEDIA_MAX_BYTES, and this package reads no environment: internal/config is
// the only importer of os.Getenv (spec L30).
func ValidateMediaBegin(b MediaBegin, maxBytes int64) error {
	if b.SHA256 == nil || !assetPattern.MatchString(*b.SHA256) {
		return InvalidFieldError{Field: "sha256",
			Why: "a content address is 64 lowercase hex characters"}
	}
	if b.ContentType == nil || !ContentTypeAllowed(*b.ContentType) {
		return InvalidFieldError{Field: "contentType",
			Why: fmt.Sprintf("this build stores %s and nothing else",
				strings.Join(allowedContentTypes, " and "))}
	}
	if b.ByteSize == nil || *b.ByteSize <= 0 {
		return InvalidFieldError{Field: "byteSize",
			Why: "a photograph has a positive size, and SigV4 signs an exact length"}
	}
	if *b.ByteSize > maxBytes {
		return InvalidFieldError{Field: "byteSize",
			Why: fmt.Sprintf("%d bytes, and this build mints an upload for at most %d",
				*b.ByteSize, maxBytes)}
	}
	return nil
}

// MediaMint is the body of `POST /v1/media/mint`: a LIST of ids, so a
// twelve-photograph grid is one round trip rather than twelve (OE-1).
//
// THE LIST IS A POINTER FOR DEC-89'S REASON AND ONE MORE. An absent `ids` and
// `"ids": []` are different requests — one is a client that forgot the key and
// one is a client asking for nothing — and only the first is worth a 422 that
// says so.
type MediaMint struct {
	IDs *[]string `json:"ids"`
}

// MaxMintIDs bounds one mint request, and it is THIS BUILD'S POLICY rather
// than schema — the same sense MaxNameBytes is.
//
// THE NUMBER COMES FROM WHAT THE ROUTE IS FOR. M1's photo grid is the largest
// caller and the client draws a screen of tiles at a time; a presigned URL is
// 394 characters, so 100 ids is roughly 39 kB of response — comfortably inside
// the body ceiling, and two orders of magnitude above any grid the client
// actually paints. What it stops is a list long enough to make one authorised
// request into a minute of HMAC: presigning is local arithmetic, so the cost is
// linear and unbounded without a bound here.
const MaxMintIDs = 100

// ValidateMediaMint answers the first thing that is wrong with a mint request.
func ValidateMediaMint(m MediaMint) error {
	if m.IDs == nil {
		return InvalidFieldError{Field: "ids", Why: "a mint takes a list of media object ids"}
	}
	if len(*m.IDs) == 0 {
		return InvalidFieldError{Field: "ids", Why: "a mint of nothing has no answer to give"}
	}
	if len(*m.IDs) > MaxMintIDs {
		return InvalidFieldError{Field: "ids",
			Why: fmt.Sprintf("%d ids, and this build mints at most %d in one request",
				len(*m.IDs), MaxMintIDs)}
	}
	for _, id := range *m.IDs {
		if !assetPattern.MatchString(id) {
			return InvalidFieldError{Field: "ids",
				Why: fmt.Sprintf("%q is not a media object id: 64 lowercase hex characters", id)}
		}
	}
	return nil
}

// ValidateMediaID is the guard on an id arriving through ROUTE ARGUMENTS
// rather than through a body.
//
// IT IS A SEPARATE FUNCTION FROM THE STRUCT VALIDATORS BECAUSE ITS INPUT IS A
// DIFFERENT KIND OF UNTRUSTED. A body has been through DecodeJSON and a struct
// validator; a path segment is whatever was in the URL — a stale route, a bad
// push, a deep link — and it reaches the store with no validator between it and
// a query unless something like this is on the handler's first line.
func ValidateMediaID(id string) error {
	if !assetPattern.MatchString(id) {
		return InvalidFieldError{Field: "id",
			Why: "a media object id is 64 lowercase hex characters"}
	}
	return nil
}
