// The route table, declared as data and registered from the slice (DEC-28).
//
// WHY A SLICE RATHER THAN A RUN OF mux.Handle CALLS, and it is a measurement
// rather than a preference: `http.ServeMux` has NO exported enumeration of its
// registered patterns in Go 1.26, so a coverage check written against the mux
// is unimplementable and gets silently downgraded to a grep. ServeMux still
// does every scrap of the matching — spec L18 is untouched — and what the
// slice buys is a thing a test can iterate.
//
// A ROUTE'S RESPONSE POLICY AND ITS RATE BUDGET ARE FIELDS NOW, AND THE
// MIDDLEWARE IS APPLIED FROM THE TABLE (PD-09). Both used to be DERIVED, and
// this file said so: `Auth` decided the limiter completely, and nothing decided
// the cache policy at all. Two findings changed that, one measured and one
// structural.
//
//   - MEASURED ON THE LIVE SERVER: NO response set `Cache-Control` at all —
//     `/healthz`, the 401s and `GET /v1/logbook` returned Content-Type,
//     X-Request-Id and Date and nothing else. That is survivable while every
//     route carries an Authorization header, because RFC 9111 §3.5 forbids a
//     SHARED cache from storing such a response. R8's `GET /l/{token}` carries
//     no Authorization header at all, so nothing reaches it: a 200 with an
//     ETag and no Cache-Control is heuristically cacheable by any
//     intermediary, and after "Stop sharing" a cached envelope keeps handing
//     out live media capabilities for as long as it survives — which unbounds
//     the very window DEC-44 shrank to two minutes. `NoStore` is therefore not
//     derivable from anything else on the row: `POST /v1/media/mint` carries
//     capabilities and `PUT /v1/trips/{id}` does not, and both are
//     authenticated writes.
//   - STRUCTURALLY: this file used to promise a per-route limit field "the day
//     an unauthenticated route arrives that is not a credential attempt". That
//     day is R8's public read, which needs a THIRD budget — per address,
//     generous, for a route with no identity at all — and a derivation from
//     `Auth` cannot express three values. The consequence of getting it wrong
//     is not cosmetic: the public read would inherit AUTH_RATE_LIMIT_PER_MIN,
//     a CREDENTIAL budget of 10/min, from the same bucket instance as register
//     and sign-in, so one person browsing a shared trip locks everybody out of
//     signing in.
//
// AND `Limit` IS NO LONGER DERIVABLE, WHICH IS THE ROW R8 SAID WOULD ARRIVE.
// This block used to end "the row that cannot be derived arrives in R8"; it is
// `GET /l/{token}`, which is `Auth: false` and is NOT `LimitCredential`. Three
// values cannot come out of one boolean, and the leg that catches a wrong one
// is TestEveryRouteWearsTheCeilingItsTableRowNames.
//
// /healthz is unauthenticated and unlimited and is deliberately NOT in this
// table — it is cmd/api's, because a liveness probe is not part of the API.
package httpapi

import "net/http"

// Limit names WHICH limiter a route counts against — not a number, because a
// number here would be a second place for AUTH_RATE_LIMIT_PER_MIN and
// TRAVELLER_RATE_LIMIT_PER_MIN to live and the two would drift from
// internal/config.
type Limit int

const (
	// LimitCredential is DEC-48's per-address ceiling. It bounds
	// unauthenticated Argon2 work and is deliberately low (10/min), so it is
	// wrong for anything a phone does in a loop.
	LimitCredential Limit = iota

	// LimitTraveller bounds a stolen token and keys on the traveller, so it
	// has to be high enough that no honest client ever meets it (600/min). It
	// can only be applied INSIDE RequireTraveller, because the traveller is on
	// the context only after the credential has been resolved.
	LimitTraveller

	// LimitPublic is R8's third budget, and it is the row this file predicted
	// in its own words: "the day an unauthenticated route arrives that is not
	// a credential attempt, this becomes a sixth field rather than a
	// derivation." That day is `GET /l/{token}`.
	//
	// IT KEYS ON THE ADDRESS BECAUSE THERE IS NOTHING ELSE. The request
	// carries no credential, so there is no identity to key on — and it is not
	// a credential ATTEMPT, so LimitCredential is both the wrong number and
	// the wrong BUCKET: sharing the instance would mean one person browsing a
	// shared trip locks everybody out of signing in.
	LimitPublic
)

func (l Limit) String() string {
	switch l {
	case LimitTraveller:
		return "traveller"
	case LimitPublic:
		return "public"
	default:
		return "credential"
	}
}

// Route is one row of the table.
//
// `Mutating` IS GONE (OE-10), AND R3 IS THE STEP THAT WAS TOLD TO RE-EXAMINE
// IT. The deletion's own text said: "R3 must check whether it has acquired a
// reader; if it has, this deletion is withdrawn". It has not. Nothing in the
// slice read it, nothing in R1-R8 reads it, and it was guarded by a leg
// asserting that a field equals a function of another field — decoration with
// a test over it, by this project's own standard.
//
// THE TRIGGER THE DELETION NAMED HAS ACTUALLY FIRED AND STILL DOES NOT SAVE
// IT, which is worth writing down because it is the closest a deleted thing
// has come to returning. `POST /v1/media/mint` IS the POST that writes
// nothing, so `Mutating == (Method != GET)` genuinely stops holding at this
// step. But a field is real when something READS it, not when it would be
// accurate: two fields arriving that are read is not an argument for keeping a
// third that is not. It comes back the day something asks "does this route
// change the log" — an audit line, a cache invalidation, a read-replica router
// — and it comes back with that caller in the same commit.
type Route struct {
	Method  string
	Pattern string
	Handler http.HandlerFunc
	Auth    bool

	// Limit is which ceiling this route wears. See the type.
	Limit Limit

	// NoStore is DEC-51's capability policy: `Cache-Control: no-store, private`
	// and `Referrer-Policy: no-referrer`.
	//
	// IT IS TRUE FOR A RESPONSE CARRYING A CAPABILITY AND FOR NOTHING ELSE.
	// `Referrer-Policy` is load-bearing rather than hygiene here, and the
	// reason is what a presigned URL IS: a PURE BEARER CAPABILITY with
	// unlimited replay, which accepts unsigned request headers such as `Range`
	// and cannot be revoked before it expires. `no-referrer` is what keeps it
	// out of the next site's logs.
	NoStore bool
}

// Routes is the whole API surface at R8: two credential routes, one
// conditional read, one whole-state write, D3's cascade, T5's city, C1's pin,
// D2's removal, H1's three share writes, U1's pencil, the one revocation
// surface, the three media routes, R7's four photograph routes, N1's one walk
// route — and R8's public read, which is the only row with no bearer token in
// front of it.
//
// THE COUNT IS RE-DERIVED AND NOT CARRIED, and the command is anchored so it
// cannot match its own mention:
//
//	grep -cE '^\t\t\{http\.Method' internal/httpapi/routes.go
//
// The unanchored `grep -c http.Method` R6 used answers 22 at this commit
// against 21 rows, because THIS SENTENCE matches it — which is rule 10's own
// failure ("a grep matching its own replacement") turning up in a place nobody
// was watching. TestEveryRouteInTheTableReachesTheMux is the guard that
// depends on no pattern at all.
func Routes(deps Deps) []Route {
	return []Route{
		{http.MethodPost, "/v1/auth/register", register(deps), false, LimitCredential, false},
		{http.MethodPost, "/v1/auth/session", signIn(deps), false, LimitCredential, false},
		{http.MethodGet, "/v1/logbook", readLogbook(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/trips/{id}", putTrip(deps), true, LimitTraveller, false},

		// D3's CASCADE, AND IT IS AUTHENTICATED AND NOTHING ELSE. The
		// name-confirmation gate the safety lens asked for is DECLINED, in
		// writing, at the top of trip_handlers.go.
		{http.MethodDelete, "/v1/trips/{id}", deleteTrip(deps), true, LimitTraveller, false},

		// R6's THREE ROWS: T5's city, C1's pin, and D2's removal.
		//
		// NONE OF THE THREE IS `NoStore`, for the reason the share rows below
		// are not: the flag is for a response carrying a capability the SERVER
		// MINTED. These carry a log — a city, a place, or the whole document —
		// and a `coverAsset` inside one is an object id rather than a URL,
		// which is the whole of DEC-46.
		//
		// THE FIRST TWO ARE PUTs ON CLIENT-MINTED KEYS (DEC-33), idempotent by
		// construction. THE THIRD IS A DELETE WHOSE REACH IS A REQUIRED QUERY
		// PARAMETER, and it is worth reading beside R5's `?scope=all`, which
		// is the opposite call made on purpose: optional there, because the
		// path is singular and the default is the smaller act; required here,
		// because D2's two branches destroy different amounts and neither is
		// obvious. See place_handlers.go.
		{http.MethodPut, "/v1/cities/{id}", putCity(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/places/{id}", putPlace(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/places/{id}", removePlace(deps), true, LimitTraveller, false},

		// R7's FIVE ROWS: M2's note, D1's delete, N1's 'Later', M2.2's
		// 'Change', and N1's two walk controls on one path.
		//
		// NONE OF THE FIVE IS `NoStore`, for the reason none of R5's or R6's
		// is: the flag is for a response carrying a capability the SERVER
		// MINTED. These carry a log — a photograph, a walk, a group of
		// photographs, or the whole document — and a photograph's `asset` is
		// an object id rather than a URL, which is the whole of DEC-46. The
		// URL is minted by `POST /v1/media/mint`, which IS marked.
		//
		// FOUR VERBS AND FOUR SHAPES, each read off what moved rather than off
		// a resource habit. The PUTs are idempotent on client-minted keys
		// (DEC-33). The DELETE is the only destructive route in this plan that
		// answers 204, because nothing in this schema references a photograph
		// and one row leaving is something a cache CAN splice — D2's and D3's
		// envelopes exist for cascades.
		//
		// `/v1/photos/snooze` IS A COLLECTION PATH AND IT SITS BEFORE
		// `/v1/photos/{id}` HERE FOR READABILITY ONLY. `http.ServeMux` does
		// every scrap of the matching and prefers the more specific pattern
		// regardless of registration order (Go 1.22's precedence rules), so
		// the order of this slice decides nothing — which is exactly why the
		// slice is a registration source and not a router.
		//
		// AND THERE IS DELIBERATELY NO `DELETE /v1/walks/{id}`. N1's 'Discard'
		// is a flag, D2's sheet promises the track survives both branches, and
		// nothing in this app authorises destroying a recording of a day. Same
		// argument that leaves `DELETE /v1/cities/{id}` out of R6 — except
		// that there the database is the backstop (DEC-57's three RESTRICT
		// keys) and here it is not: `walks` simply has no route.
		{http.MethodPost, "/v1/photos/snooze", snoozePhotos(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/photos/{id}", putPhoto(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/photos/{id}", deletePhoto(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/photos/{id}/refile", refilePhoto(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/walks/{id}", putWalk(deps), true, LimitTraveller, false},

		// H1's THREE WRITES, ON ONE PATH WITH THREE VERBS — the client's three
		// methods rather than a REST habit. See share_handlers.go.
		//
		// NONE OF THE THREE IS `NoStore`, AND THE MINT IS THE ONE THAT LOOKS
		// LIKE IT SHOULD BE. `NoStore` is DEC-51's policy for a response
		// carrying a CAPABILITY, and the mint's answer does carry one — but it
		// carries the token the CALLER just sent in the request body, so a
		// cache that stored the response would be storing a secret the client
		// already holds and just transmitted. What the flag exists for is a
		// capability the server MINTED and the caller has no other copy of:
		// `POST /v1/media` and `POST /v1/media/mint`, whose presigned URLs are
		// pure bearer capabilities with unlimited replay. Marking this row
		// would make the flag mean two things, which is how a policy stops
		// being readable. (R8's `GET /l/{token}` is the row that needs it
		// most, and it is not here yet.)
		{http.MethodPut, "/v1/trips/{id}/share", setShareOptions(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/trips/{id}/share", newShareLink(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/trips/{id}/share", stopSharing(deps), true, LimitTraveller, false},

		// U1's PENCIL. `GET /v1/me` is DELETED (OE-7) — see me_handlers.go.
		{http.MethodPatch, "/v1/me", patchMe(deps), true, LimitTraveller, false},

		// THE ONLY REVOCATION SURFACE IN THE API, against a thirty-day untuned
		// TTL. `?scope=all` is its sibling and rides on this row rather than
		// on a second one.
		//
		// IT IS `LimitTraveller` AND NOT `LimitCredential`, WHICH READS
		// BACKWARDS AND IS RIGHT. The credential ceiling bounds
		// UNAUTHENTICATED Argon2 work; this route is authenticated, so it can
		// only be reached by somebody already holding a live token — and the
		// traveller limiter can only be applied INSIDE RequireTraveller
		// anyway, because the traveller is on the context only after the
		// credential has been resolved.
		{http.MethodDelete, "/v1/auth/session", revokeSession(deps), true, LimitTraveller, false},

		// THE THREE MEDIA ROUTES, AND ONLY TWO OF THEM CARRY A CAPABILITY IN
		// THEIR ANSWER — which is the whole reason `NoStore` is a field rather
		// than a derivation. Begin answers an upload URL and mint answers up
		// to a hundred read URLs; commit answers a ROW, and gets nothing,
		// because a policy applied where it is not needed is a policy nobody
		// can read the meaning of.
		{http.MethodPost, "/v1/media", beginMedia(deps), true, LimitTraveller, true},
		{http.MethodPost, "/v1/media/{id}/commit", commitMedia(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/media/mint", mintMedia(deps), true, LimitTraveller, true},

		// R8's ONE ROW, AND IT IS THE ONLY ROUTE IN THIS TABLE WITH NO BEARER
		// TOKEN IN FRONT OF IT. Everything about it is a decision about what a
		// stranger holding a URL can see, and all three of its fields differ
		// from every other row.
		//
		// `Auth: false` — the reader is nobody. The traveller comes OUT of the
		// token lookup, through a GLOBAL unique index on the digest, because
		// the request arrives with none in hand.
		//
		// `LimitPublic` — see the type. Under the derivation this table used
		// to make, an unauthenticated route inherited the CREDENTIAL ceiling
		// and its bucket, so one person reading a shared trip would 429
		// everybody's sign-in.
		//
		// `NoStore: true` — AND THIS IS THE ROW THE FLAG WAS ADDED FOR
		// (PD-09). Every other capability response in this table carries an
		// `Authorization` header, so RFC 9111 §3.5 forbids a SHARED cache from
		// storing it. Nothing reaches this one: a 200 with an ETag and no
		// `Cache-Control` is heuristically cacheable by any intermediary, and
		// a cached envelope keeps handing out live media capabilities after
		// 'Stop sharing' for as long as it survives — which unbounds the very
		// window DEC-84 fixed at fifteen minutes. `Referrer-Policy:
		// no-referrer` is the other half: a share page fetches these URLs and
		// then links somewhere, and without it the capability travels in a
		// `Referer` header into the next origin's access log.
		//
		// THE PATH IS `/l/{token}` AND NOT `/v1/…`, WHICH IS DEC-10's SHAPE
		// AND THE CLIENT'S. `Trip.shareLinkId` is twelve characters and H1
		// renders `travellog.app/l/<token>` — a URL somebody reads off a
		// screen and types. A version prefix would make it longer for a
		// negotiation this route cannot have: a stranger sends no
		// `X-Logbook-Format` and has nothing to do with a 406.
		{http.MethodGet, "/l/{token}", publicShare(deps), false, LimitPublic, true},
	}
}
