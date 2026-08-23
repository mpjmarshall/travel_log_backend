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
// STATED HERE BECAUSE IT IS NOT OBVIOUS. Every route in this table that does
// NOT require a traveller is an auth route, so `!Auth` is exactly DEC-48's
// rate-limited set: the limiter bounds unauthenticated Argon2 work, and there
// is no unauthenticated route here that is not an attempt at a credential.
// /healthz is unauthenticated and unlimited and is deliberately NOT in this
// table — it is cmd/api's, because a liveness probe is not part of the API.
// The day an unauthenticated route arrives that is not a credential attempt,
// this becomes a sixth field rather than a derivation.
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
