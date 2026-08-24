// The route table, declared as data and registered from the slice (DEC-28).
//
// WHY A SLICE RATHER THAN A RUN OF mux.Handle CALLS, and it is a measurement
// rather than a preference: `http.ServeMux` has NO exported enumeration of its
// registered patterns in Go 1.26, so a coverage check written against the mux
// is unimplementable and gets silently downgraded to a grep. ServeMux still
// does every scrap of the matching — spec L18 is untouched — and what the
// slice buys is a thing a test can iterate.
//
// THE MIDDLEWARE PER ROUTE IS DERIVED FROM `Auth`, AND THE DERIVATION IS
// STATED HERE BECAUSE IT IS NOT OBVIOUS. EVERY route in this table wears a
// ceiling; `Auth` decides WHICH, and it decides it completely. A route with no
// traveller is a credential attempt, so it takes DEC-48's per-address ceiling,
// which bounds unauthenticated Argon2 work. A route with a traveller takes the
// per-traveller ceiling, which bounds a stolen token — and it could not take
// any other key, because the traveller id is the only identity such a request
// carries and a route without one has no id to count against. So the derivation
// is not a convenience here: today it is forced.
// /healthz is unauthenticated and unlimited and is deliberately NOT in this
// table — it is cmd/api's, because a liveness probe is not part of the API.
//
// THE SIXTH FIELD WAS PUT AND DECLINED, at the fix that composed the two
// middlewares. The line below used to promise it "the day an unauthenticated
// route arrives that is not a credential attempt", and that day is the public
// share read, which is two steps away and not here. A per-route ceiling added
// now would be a field whose value is a pure function of `Auth` on every row —
// the exact shape `Mutating` is in, and `Mutating` needs a leg of its own to
// stop it becoming decoration. What the share read actually needs is a THIRD
// budget (per address, generous, for a route with no identity at all), and a
// field that can only say "credential" or "traveller" would not carry it. The
// field arrives with the row that cannot be derived, and it arrives holding
// three values rather than two.
package httpapi

import "net/http"

// Route is one row of the table. `Mutating` is declared by DEC-28 and is not
// read by Mount today; TestMutatingAgreesWithTheMethod is what stops it
// drifting into decoration.
type Route struct {
	Method   string
	Pattern  string
	Handler  http.HandlerFunc
	Auth     bool
	Mutating bool
}

// Routes is the whole API surface at VS7: two credential routes, one
// conditional read, one whole-state write.
func Routes(deps Deps) []Route {
	return []Route{
		{http.MethodPost, "/v1/auth/register", register(deps), false, true},
		{http.MethodPost, "/v1/auth/session", signIn(deps), false, true},
		{http.MethodGet, "/v1/logbook", readLogbook(deps), true, false},
		{http.MethodPut, "/v1/trips/{id}", putTrip(deps), true, true},
	}
}
