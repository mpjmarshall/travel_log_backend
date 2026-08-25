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
	// THIRTEEN AT R5, AND THE NUMBER IS A LITERAL ON PURPOSE. Deriving it from
	// `len(Routes(...))` would make this line unfalsifiable — it would say
	// "the table holds as many routes as the table holds". What it is for is
	// that a route ARRIVING or LEAVING is a decision somebody made, and it
	// should cost one line in a test rather than nothing at all. It was four
	// at VS7; R3 added begin, commit and mint; R5 adds six — D3's cascade,
	// H1's three share writes, U1's pencil and the one revocation surface.
	//
	// SIX AND NOT SEVEN, BECAUSE "REVOKE THEM ALL" IS A QUERY PARAMETER. The
	// plan's own table holds 23 rows at the end of R8 and allots this step
	// six; `?scope=all` rides on `DELETE /v1/auth/session` rather than
	// claiming a `DELETE /v1/auth/sessions` the table does not have. See the
	// row's own comment.
	if len(routes) != 13 {
		t.Errorf("the table holds %d routes; R5's surface is thirteen — two credential "+
			"routes, one conditional read, one whole-state write, D3's cascade, H1's "+
			"three share writes, U1's pencil, the revocation surface, and the three "+
			"media routes", len(routes))
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

// `TestMutatingAgreesWithTheMethod` WAS HERE AND IS DELETED WITH ITS FIELD
// (OE-10), and the deletion is recorded rather than silent because R3 was the
// step told to re-examine it.
//
// It asserted that a field equals a function of another field, over a field
// nothing read — so the only mutation it could catch was somebody mistyping a
// boolean in a row nobody consults. The trigger the deletion named has now
// FIRED: `POST /v1/media/mint` is the POST that writes nothing, so
// `Mutating == (Method != GET)` genuinely stops holding at this step. It still
// does not bring the field back, because a field is real when something reads
// it and not when it would be accurate. routes.go carries the long form.
//
// WHAT REPLACES IT IS A LEG ABOUT A FIELD THAT IS READ. `Limit` decides which
// ceiling Mount applies, so a wrong value changes behaviour — which is exactly
// what `Mutating` could not do.
func TestEveryRouteWearsTheCeilingItsTableRowNames(t *testing.T) {
	for _, route := range Routes(newHarness(t, options{}).deps) {
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			// TWO SEPARATE BUDGETS, AND THE LEG SETS THE ONE THE ROW NAMES TO
			// ONE AND THE OTHER TO PLENTY. That is what makes it about WHICH
			// limiter rather than about limiting: a route wired to the wrong
			// bucket meets a ceiling of a thousand and never 429s at all.
			opt := options{ratePerMin: 1000, travellerPerMin: 1000}
			switch route.Limit {
			case LimitTraveller:
				opt.travellerPerMin = 1
			default:
				opt.ratePerMin = 1
			}
			h := newHarness(t, opt)

			bearerHeader := ""
			if route.Auth {
				// `bearer` spends two tokens from the CREDENTIAL bucket, which
				// is why the credential ceiling is 1000 on this branch — a
				// traveller-limited route must not be refused because
				// signing in was.
				bearerHeader = bearer(t, h)
			}

			// ONE ROUTE DESTROYS ITS OWN CREDENTIAL, AND IT IS SIGNED IN AGAIN
			// BETWEEN THE TWO SPENDS RATHER THAN EXEMPTED. `DELETE
			// /v1/auth/session` revokes the token it was called with, so the
			// second request would be a 401 before the limiter is ever
			// reached — and an exemption would leave the only revocation
			// surface in the API as the one route nothing asserts a ceiling
			// on. Signing in again spends the CREDENTIAL bucket, which is
			// 1000 on this branch, and mints a new token for the same
			// traveller — so the TRAVELLER bucket, which is what this leg is
			// about, is untouched by the re-sign-in.
			renews := route.Method == http.MethodDelete && route.Pattern == "/v1/auth/session"

			spend := func() int {
				path := strings.ReplaceAll(route.Pattern, "{id}", strings.Repeat("a", 64))
				status := h.do(t, route.Method, path, bodyFor(route), bearerHeader).status
				if renews && route.Auth {
					bearerHeader = signInAs(t, h, "matt@example.com")
				}
				return status
			}
			if got := spend(); got == http.StatusTooManyRequests {
				t.Fatalf("the FIRST request = 429, so this route is counting against "+
					"a bucket something else had already emptied; its row says %s",
					route.Limit)
			}
			if got := spend(); got != http.StatusTooManyRequests {
				t.Errorf("the second request at a ceiling of 1 = %d, want 429 — the "+
					"%s ceiling this row names is not the one being applied", got, route.Limit)
			}
		})
	}
}

// bodyFor is a request body each route will get past its own decoder. The
// legs above are about ceilings and headers, so what matters is that the
// request REACHES the handler rather than what the handler answers.
func bodyFor(route Route) string {
	if strings.HasPrefix(route.Pattern, "/v1/media") {
		digest := strings.Repeat("a", 64)
		return `{"sha256":"` + digest + `","byteSize":10,"contentType":"image/png","ids":["` + digest + `"]}`
	}
	return aTrip
}

// DEC-51's HEADERS ARE ON EXACTLY THE ROWS THAT DECLARE THEM, AND THE
// ASSERTION IS ON PRESENCE (PD-09).
//
// The security lens's finding about v7.0's only header-adjacent leg is the
// reason that last clause is written down: it compared two answers to each
// other, and would have passed with the headers absent from BOTH. So this
// asserts the literal values on the rows that say NoStore, and asserts their
// ABSENCE on the rows that do not — because a policy applied everywhere is a
// policy that says nothing about anything.
func TestTheCapabilityHeadersAreOnTheRowsThatDeclareThem(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	var declared, plain int
	for _, route := range Routes(h.deps) {
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
			path := strings.ReplaceAll(route.Pattern, "{id}", strings.Repeat("a", 64))
			bearerHeader := token
			if !route.Auth {
				bearerHeader = ""
			}
			got := h.do(t, route.Method, path, bodyFor(route), bearerHeader)

			cache := got.header.Get("Cache-Control")
			referrer := got.header.Get("Referrer-Policy")
			if route.NoStore {
				declared++
				if cache != "no-store, private" {
					t.Errorf("Cache-Control = %q, want %q", cache, "no-store, private")
				}
				if referrer != "no-referrer" {
					t.Errorf("Referrer-Policy = %q, want %q", referrer, "no-referrer")
				}
				return
			}
			plain++
			if cache != "" || referrer != "" {
				t.Errorf("Cache-Control = %q and Referrer-Policy = %q on a row that "+
					"declares no capability — a policy applied everywhere is a policy "+
					"that says nothing about anything", cache, referrer)
			}
		})
	}

	// THE VACUOUS DIRECTIONS, BOTH OF THEM. A table with no NoStore rows
	// passes the loop above having asserted nothing about the headers, and a
	// table where EVERY row declares them passes having asserted nothing about
	// the discrimination.
	if declared == 0 {
		t.Error("no route in the table declares NoStore, so this leg checked nothing")
	}
	if plain == 0 {
		t.Error("every route in the table declares NoStore, so the absence half " +
			"checked nothing")
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
			Media:          full.Media, Objects: full.Objects, MediaMaxBytes: full.MediaMaxBytes,
		}},
		{"no traveller rate limiter", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			AuthLimit: full.AuthLimit,
			Media:     full.Media, Objects: full.Objects, MediaMaxBytes: full.MediaMaxBytes,
		}},
		{"no logbook store", Deps{
			Auth: full.Auth, Log: full.Log,
			AuthLimit: full.AuthLimit, TravellerLimit: full.TravellerLimit,
			Media: full.Media, Objects: full.Objects, MediaMaxBytes: full.MediaMaxBytes,
		}},
		{"no media store", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			AuthLimit: full.AuthLimit, TravellerLimit: full.TravellerLimit,
			Objects: full.Objects, MediaMaxBytes: full.MediaMaxBytes,
		}},
		{"no object store", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			AuthLimit: full.AuthLimit, TravellerLimit: full.TravellerLimit,
			Media: full.Media, MediaMaxBytes: full.MediaMaxBytes,
		}},
		{"no MEDIA_MAX_BYTES", Deps{
			Auth: full.Auth, Logbook: full.Logbook, Log: full.Log,
			AuthLimit: full.AuthLimit, TravellerLimit: full.TravellerLimit,
			Media: full.Media, Objects: full.Objects,
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
