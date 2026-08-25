// The share link: what a write to it may contain, and the one place a share
// token becomes a digest (DEC-85).
//
// A SHARE TOKEN IS NOT A SESSION TOKEN AND THE TWO HASHES ARE NOT ONE
// FUNCTION. `auth.HashToken` base64-decodes its input and refuses anything
// that is not exactly 32 raw bytes, because a session token is MINTED BY THIS
// SERVER and that is the only shape it mints. A share token is minted by the
// CLIENT — twelve characters of `abcdefghjkmnpqrstuvwxyz23456789`, from
// `logbook.dart:819` — so it is arbitrary text and base64 has nothing to do
// with it. Reusing auth's function would refuse every real token; writing this
// one in internal/auth would put a logbook concept in the package that
// deliberately knows nothing about a trip. Hence a second function, in the
// domain that owns the row, with the difference stated rather than left for
// somebody to discover by symmetry.
//
// WHAT DEC-85 BUYS AND WHAT IT COSTS. Under DEC-67 the table revokes and KEEPS,
// so a dump held EVERY capability ever issued, live and revoked, in the clear.
// Hashing costs that design nothing — the row is still there, still revoked,
// still countable. What it costs is `Trip.shareLinkId`: the server cannot emit
// a plaintext it does not hold, so the emitted trip carries `null` on every
// read and the CLIENT holds the only copy. DEC-91's `shared` boolean is the
// answer to that and it shipped in R1; docs/CLIENT-PREREQUISITES.md §8 is the
// client's half.
//
// THE ONE PLACE THE PLAINTEXT IS EVER ECHOED is the answer to `POST
// /v1/trips/{id}/share`, and it is echoed rather than recovered: the client
// SENT that token in the request body a microsecond earlier. Answering it is
// what makes DEC-32's splice leave a usable `shareLinkId` behind on the one
// write that has one.
package logbook

import (
	"crypto/sha256"
	"fmt"
	"regexp"
)

// MinShareTokenBytes is the floor on a client-minted token, and it is an
// ENTROPY argument rather than a formatting one.
//
// A share token is a pure bearer capability: anybody holding it reads the
// trip, and `GET /l/{token}` arrives with no traveller in hand. So a server
// that accepts `"a"` accepts a capability somebody can guess on the first try,
// and the only place that can be refused is here — the schema's own check is
// `token <> ”`, which 0001 could write and could not bound.
//
// TWELVE IS THE CLIENT'S OWN LENGTH AND THE NUMBER COMES FROM ITS ALPHABET.
// `shareLinkIdLength = 12` over a 31-character alphabet is 12·log2(31) = 59.5
// bits, which is the entropy this system actually ships. Anything shorter is a
// weaker capability than the client mints, and refusing it here is the only
// guard against a second caller — a script, a curl, R8's own fixtures —
// minting one.
const MinShareTokenBytes = 12

// MaxShareTokenBytes is a DoS bound and this build's own policy, in the sense
// MaxNameBytes is: `share_links.token` was `text` and nothing bounded it, so a
// one-megabyte token was storable and then hashed on every public read.
const MaxShareTokenBytes = 64

// shareTokenPattern is spec L23's compiled regexp for a client-minted token.
//
// IT IS DELIBERATELY WIDER THAN THE CLIENT'S ALPHABET. The client draws from
// `abcdefghjkmnpqrstuvwxyz23456789` — no `0`, `1`, `i`, `l` or `o`, so the
// token can be read off a screen and typed — and that is a property of the
// GENERATOR, not a constraint on the log. Pinning the server to it would make
// a perfectly good token from a future generator a 422, which is the mistake
// `idPattern` records having already been made once about ids.
//
// THE TWO BOUNDS ARE INTERPOLATED RATHER THAN TYPED INTO THE EXPRESSION, for
// the reason `contentTypePattern` is built from its list: a number written in
// two places is how every count in this project has gone wrong. What keeps the
// derivation honest is a leg asserting String() is exactly
// `^[a-z0-9]{12,64}$`.
var shareTokenPattern = regexp.MustCompile(
	fmt.Sprintf(`^[a-z0-9]{%d,%d}$`, MinShareTokenBytes, MaxShareTokenBytes))

// HashShareToken is sha256 of the token's own bytes.
//
// OF THE BYTES AS TYPED, WITH NO DECODE STEP, and the difference from
// `auth.HashToken` is the whole reason this function exists — see the file
// comment. It answers 32 bytes for any input, so the caller does not have to
// handle an error that cannot happen; what refuses a token that is the wrong
// SHAPE is ValidateShareMint, before this is ever reached.
func HashShareToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ShareWrite is the body of `PUT /v1/trips/{id}/share`: H1's three switches,
// and every field is a pointer because absent means leave alone (DEC-89).
//
// THE POINTERS ARE SINGLE AND NOT DOUBLE, unlike TripWrite's four. A boolean
// column here is NOT NULL, so there is no third state to carry: absent and
// sent-as-null are the same request and both mean leave it alone. TripWrite's
// `**T` exists for columns that are nullable in the schema, and none of these
// three is.
//
// H1 FLICKS ONE SWITCH AT A TIME AND THE TYPE IS HOW THAT IS HONOURED. The
// client's `setShareOptions` takes three `bool?` arguments and the sheet calls
// it with exactly one of them set, because every writing control on H1 goes
// inert while a write is in flight — two changes inside one save are both
// computed from the state as it was, and the second puts the first back.
type ShareWrite struct {
	SharePhotos      *bool `json:"sharePhotos"`
	ShareNotes       *bool `json:"shareNotes"`
	ShareCoordinates *bool `json:"shareCoordinates"`
}

// ShareMint is the body of `POST /v1/trips/{id}/share`: the token the CLIENT
// minted.
//
// THE SERVER DOES NOT MINT IT, AND THAT IS DEC-85's COST BEING PAID RATHER
// THAN AN OVERSIGHT. With tokens hashed at rest the server cannot hand a
// plaintext back on any later read, so whichever side mints it, the client is
// the one that has to hold it. Having the client mint it means the plaintext
// exists on the phone before the request is made and survives a lost response
// — where a server-minted token lost in a timeout would be a live capability
// nobody holds and nothing can revoke except 'New link'.
type ShareMint struct {
	Token *string `json:"token"`
}

// THERE IS NO ValidateShareOptions AND THAT IS DELIBERATE. Every field of
// ShareWrite is a `*bool`: the only two values JSON can put in one are `true`
// and `false`, both of which are legal, and a body naming none of the three is
// DEC-89's leave-alone rather than an error. A validator that can only ever
// return nil is a function with a test over it proving nothing — this
// project's own standard for decoration — so the absence is written down here
// instead, where somebody reaching for symmetry with ValidateTrip will find it.

// ValidateShareMint refuses a token that is not the shape of a capability.
func ValidateShareMint(m ShareMint) error {
	if m.Token == nil {
		return InvalidFieldError{Field: "token",
			Why: "a new link carries the token the client minted"}
	}
	if !shareTokenPattern.MatchString(*m.Token) {
		return InvalidFieldError{Field: "token",
			Why: "a share token is 12 to 64 characters of a-z and 0-9 — " +
				"anything shorter is a capability somebody can guess"}
	}
	return nil
}
