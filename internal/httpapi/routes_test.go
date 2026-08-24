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

// EVERY ROUTE IN THE TABLE IS RATE LIMITED. This leg replaces
// TestOnlyTheUnauthenticatedRoutesAreRateLimited, which asserted the defect as
// though it were the design: `Mount` applied the limiter and the authentication
// as EITHER/OR, so `limited == !route.Auth` passed on a table in which every
// authenticated route had no ceiling at all.
//
// The two budgets are different budgets — see TestTheTwoBudgetsAreNotOneBudget
// below — so both ceilings are set low here and the claim is only that each
// route has ONE.
func TestEveryRouteInTheTableIsRateLimited(t *testing.T) {
	// Three of each: register and sign-in spend two credential tokens getting
	// the bearer, which leaves one for the loop to spend and then be refused.
	h := newHarness(t, options{ratePerMin: 3, travellerPerMin: 3})
	token := bearer(t, h)

	for _, route := range Routes(h.deps) {
		path := strings.ReplaceAll(route.Pattern, "{id}", "kyoto")
		limited := false
		for range 6 {
			if h.do(t, route.Method, path, aTrip, token).status == http.StatusTooManyRequests {
				limited = true
			}
		}
		if !limited {
			t.Errorf("%s %s answered 6 requests in a minute at a ceiling of 3 without one 429.\n"+
				"    A route with no ceiling is unlimited work for anybody holding a\n"+
				"    credential — or, on the credential routes, holding nothing at all.",
				route.Method, route.Pattern)
		}
	}
}

// AND THEY ARE NOT ONE BUDGET. The credential ceiling bounds an unauthenticated
// 64 MiB-per-attempt Argon2 surface and is deliberately low; the authenticated
// one bounds a stolen token and has to be high enough that no honest client
// meets it. This is the leg that fails if somebody "composes" by wrapping the
// authenticated routes in the credential limiter — every route would have a
// ceiling, TestEveryRouteInTheTableIsRateLimited would pass, and a phone
// syncing a log would meet a limit built for a password guesser.
func TestTheTwoBudgetsAreNotOneBudget(t *testing.T) {
	h := newHarness(t, options{ratePerMin: 3, travellerPerMin: 60})
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
			t.Errorf("%s %s: refused inside a ceiling of 60 = %v, want %v.\n"+
				"    The authenticated routes must be spending the TRAVELLER budget and\n"+
				"    the credential routes the ADDRESS budget; a route drawing on the\n"+
				"    wrong one is either unusable or unbounded.",
				route.Method, route.Pattern, limited, !route.Auth)
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
		{"no credential rate limiter", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			TravellerLimit: full.TravellerLimit,
		}},
		{"no traveller rate limiter", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			AuthLimit: full.AuthLimit,
		}},
		{"no logbook store", Deps{
			Auth: full.Auth, Log: full.Log,
			AuthLimit: full.AuthLimit, TravellerLimit: full.TravellerLimit,
		}},
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
