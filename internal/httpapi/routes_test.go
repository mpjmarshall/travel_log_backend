// The route table, asserted over the SLICE rather than over the mux (DEC-28).
//
// `http.ServeMux` has no exported enumeration of its registered patterns, so a
// coverage check written against it is unimplementable and gets silently
// downgraded to a grep. Everything here iterates Routes() and then asks the
// mux about each row, which is a check that can fail for a reason about the
// code.
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tableOnAMux(t *testing.T) (*http.ServeMux, []Route) {
	t.Helper()
	h := newHarness(t, options{})
	mux := http.NewServeMux()
	Mount(mux, h.deps)
	return mux, Routes(h.deps)
}

func TestEveryRouteInTheTableReachesTheMux(t *testing.T) {
	mux, routes := tableOnAMux(t)
	if len(routes) != 4 {
		t.Errorf("the table holds %d routes; VS7's surface is four — two credential "+
			"routes, one conditional read, one whole-state write", len(routes))
	}

	for _, route := range routes {
		path := strings.ReplaceAll(route.Pattern, "{id}", "kyoto")
		req := httptest.NewRequest(route.Method, path, nil)
		_, pattern := mux.Handler(req)
		if want := route.Method + " " + route.Pattern; pattern != want {
			t.Errorf("%s %s resolves to %q, want %q", route.Method, path, pattern, want)
		}
	}
}

// DEC-48's limiter bounds unauthenticated Argon2 work, and `!Auth` is exactly
// that set. This is the leg that makes the derivation in routes.go a fact
// rather than a comment: the credential routes refuse a second call in the
// same minute, and the authenticated ones do not.
func TestOnlyTheUnauthenticatedRoutesAreRateLimited(t *testing.T) {
	// Three a minute: register and sign-in spend two getting the credential,
	// which leaves exactly one for the loop below to spend and then be refused.
	h := newHarness(t, options{ratePerMin: 3})
	token := bearer(t, h)

	for _, route := range Routes(h.deps) {
		path := strings.ReplaceAll(route.Pattern, "{id}", "kyoto")
		limited := false
		for range 6 {
			if h.do(t, route.Method, path, aTrip, token).status == http.StatusTooManyRequests {
				limited = true
			}
		}
		if limited != !route.Auth {
			t.Errorf("%s %s: rate limited = %v, want %v", route.Method, path, limited, !route.Auth)
		}
	}
}

func TestEveryAuthenticatedRouteRefusesAMissingCredential(t *testing.T) {
	h := newHarness(t, options{})

	for _, route := range Routes(h.deps) {
		if !route.Auth {
			continue
		}
		path := strings.ReplaceAll(route.Pattern, "{id}", "kyoto")
		if got := h.do(t, route.Method, path, aTrip, ""); got.status != http.StatusUnauthorized {
			t.Errorf("%s %s with no credential = %d %s, want 401",
				route.Method, path, got.status, got.body)
		}
	}
}

// `Mutating` is declared by DEC-28 and read by nothing yet. This is what stops
// it becoming decoration: a row whose flag disagrees with its verb is a row
// somebody has stopped maintaining.
func TestMutatingAgreesWithTheMethod(t *testing.T) {
	for _, route := range Routes(newHarness(t, options{}).deps) {
		safe := route.Method == http.MethodGet || route.Method == http.MethodHead
		if route.Mutating == safe {
			t.Errorf("%s %s is declared Mutating=%v", route.Method, route.Pattern, route.Mutating)
		}
	}
}

// A NIL DEPENDENCY PANICS AT WIRING TIME RATHER THAN AT THE FIRST REQUEST, and
// that is the argument DEC-48 already made for the limiter. A nil store does
// not read as "no logbook" — it reads as working software until somebody asks
// for their log and gets a 500 out of the recover middleware.
func TestMountRefusesToWireAHalfBuiltAPI(t *testing.T) {
	full := newHarness(t, options{}).deps

	for _, tc := range []struct {
		name string
		deps Deps
	}{
		{"no rate limiter", Deps{Auth: full.Auth, Logbook: full.Logbook, Log: full.Log}},
		{"no logbook store", Deps{Auth: full.Auth, Log: full.Log, AuthLimit: full.AuthLimit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Mount with %s did not panic", tc.name)
				}
			}()
			Mount(http.NewServeMux(), tc.deps)
		})
	}
}
