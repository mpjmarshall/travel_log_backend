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
// AND `Limit` IS HONESTLY EARLY. On every row in this table it is still a pure
// function of `Auth`, which is exactly the shape `Mutating` was in when it was
// deleted below. The difference that makes it not decoration is that Mount
// READS it: a wrong value here changes which ceiling a route wears, where a
// wrong `Mutating` changed nothing at all. The row that cannot be derived
// arrives in R8, and the leg that catches a wrong one today is
// TestEveryRouteWearsTheCeilingItsTableRowNames.
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
)

func (l Limit) String() string {
	if l == LimitTraveller {
		return "traveller"
	}
	return "credential"
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

// Routes is the whole API surface at R5: two credential routes, one
// conditional read, one whole-state write, D3's cascade, and the three media
// routes.
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

		// THE THREE MEDIA ROUTES, AND ONLY TWO OF THEM CARRY A CAPABILITY IN
		// THEIR ANSWER — which is the whole reason `NoStore` is a field rather
		// than a derivation. Begin answers an upload URL and mint answers up
		// to a hundred read URLs; commit answers a ROW, and gets nothing,
		// because a policy applied where it is not needed is a policy nobody
		// can read the meaning of.
		{http.MethodPost, "/v1/media", beginMedia(deps), true, LimitTraveller, true},
		{http.MethodPost, "/v1/media/{id}/commit", commitMedia(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/media/mint", mintMedia(deps), true, LimitTraveller, true},
	}
}
