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
	// TWENTY-ONE AT R7, AND THE NUMBER IS A LITERAL ON PURPOSE. Deriving it
	// from `len(Routes(...))` would make this line unfalsifiable — it would say
	// "the table holds as many routes as the table holds". What it is for is
	// that a route ARRIVING or LEAVING is a decision somebody made, and it
	// should cost one line in a test rather than nothing at all. It was four
	// at VS7; R3 added begin, commit and mint; R5 added six — D3's cascade,
	// H1's three share writes, U1's pencil and the one revocation surface; R6
	// added three; R7 adds five.
	//
	// R5's SIX WAS NOT SEVEN BECAUSE "REVOKE THEM ALL" IS A QUERY PARAMETER,
	// AND R6's THREE IS NOT FOUR FOR THE SAME REASON: `?photos=keep|delete`
	// rides on `DELETE /v1/places/{id}`. R7's FIVE IS NOT SIX BECAUSE N1's two
	// walk controls are two fields of one body on one path — `setWalkName` and
	// `dismissWalk` write two columns of one row, and DEC-89's contract is what
	// tells them apart. There is deliberately no `DELETE /v1/walks/{id}`:
	// 'Discard' is a flag, D2's sheet promises the track survives both
	// branches, and nothing in this app authorises destroying a recording of a
	// day — the same argument that leaves `DELETE /v1/cities/{id}` out of R6.
	//
	// R8 ADDS EXACTLY ONE AND IT IS THE LAST: `GET /l/{token}`, the only row
	// in this table with no bearer token in front of it.
	//
	// TWENTY-TWO HERE AND TWENTY-THREE SHIPPED, AND THE DIFFERENCE IS NOT AN
	// ERROR IN EITHER. The plan's route table holds 23 rows and the
	// twenty-third is `GET /healthz`, which is cmd/api's and deliberately not
	// in this table — a liveness probe is not part of the API. The two numbers
	// are asserted in two places on purpose: this one, over the slice, and
	// cmd/api's own leg over the mux the server serves.
	if len(routes) != 22 {
		t.Errorf("the table holds %d routes; R8's surface is twenty-two — two "+
			"credential routes, one conditional read, one whole-state write, D3's "+
			"cascade, T5's city, C1's pin, D2's removal, H1's three share writes, "+
			"U1's pencil, the revocation surface, the three media routes, R7's four "+
			"photograph routes, N1's one walk route and the public read", len(routes))
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
	h := newHarness(t, options{ratePerMin: 3, travellerPerMin: 3, publicPerMin: 3})
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

// AND THEY ARE NOT ONE BUDGET — THREE OF THEM, SINCE R8. The credential
// ceiling bounds an unauthenticated 64 MiB-per-attempt Argon2 surface and is
// deliberately low; the authenticated one bounds a stolen token and has to be
// high enough that no honest client meets it; and the public one bounds a
// route with no identity at all and must not be either of the first two.
//
// This is the leg that fails if somebody "composes" by wrapping the
// authenticated routes in the credential limiter — every route would have a
// ceiling, TestEveryRouteInTheTableIsRateLimited would pass, and a phone
// syncing a log would meet a limit built for a password guesser.
//
// AND IT IS THE LEG THAT FAILS IF R8's ROW INHERITS THE CREDENTIAL BUCKET,
// which is what the old `Auth`-derived rule would have given it (PD-09): the
// expectation below is now a function of `route.Limit` and not of `route.Auth`,
// because those two stopped agreeing at this step. `GET /l/{token}` is
// unauthenticated and is NOT a credential attempt.
func TestTheThreeBudgetsAreThreeBudgets(t *testing.T) {
	// THE TRAVELLER CEILING IS DERIVED FROM THE TABLE AND NOT A CONSTANT, and
	// that is a correction R6 had to make rather than a preference. It was 60
	// with a hard-coded `for range 6`, which is a ceiling this leg's OWN
	// traffic outgrows the moment the table passes ten authenticated rows —
	// and it did: at sixteen rows the loop spends 90 tokens against 60 and the
	// last four routes are refused for a reason that has nothing to do with
	// which budget they draw on. A leg that reddens when a route is ADDED is a
	// leg somebody edits the number in; deriving it means the next step's
	// routes cost nothing. THE PUBLIC CEILING IS DERIVED THE SAME WAY.
	//
	// THE CREDENTIAL CEILING STAYS A SMALL LITERAL, because being refused
	// there is the assertion.
	const tries = 6
	ceiling := tries*len(Routes(Deps{})) + tries
	h := newHarness(t, options{ratePerMin: 3, travellerPerMin: ceiling, publicPerMin: ceiling})
	token := bearer(t, h)

	for _, route := range Routes(h.deps) {
		path := strings.ReplaceAll(route.Pattern, "{id}", "kyoto")
		path = strings.ReplaceAll(path, "{token}", "mnpqrstuvwxy")
		limited := false
		for range tries {
			if h.do(t, route.Method, path, aTrip, token).status == http.StatusTooManyRequests {
				limited = true
			}
		}
		if want := route.Limit == LimitCredential; limited != want {
			t.Errorf("%s %s: refused inside a ceiling of %d = %v, want %v.\n"+
				"    Its row names the %s ceiling. The authenticated routes must be\n"+
				"    spending the TRAVELLER budget, the credential routes the ADDRESS\n"+
				"    budget, and the public read a THIRD bucket of its own — a route\n"+
				"    drawing on the wrong one is either unusable or unbounded, and the\n"+
				"    public read drawing on the credential one locks everybody out of\n"+
				"    signing in.",
				route.Method, route.Pattern, ceiling, limited, want, route.Limit)
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
			opt := options{ratePerMin: 1000, travellerPerMin: 1000, publicPerMin: 1000}
			switch route.Limit {
			case LimitTraveller:
				opt.travellerPerMin = 1
			case LimitPublic:
				opt.publicPerMin = 1
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
				path = strings.ReplaceAll(path, "{token}", "mnpqrstuvwxy")
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
