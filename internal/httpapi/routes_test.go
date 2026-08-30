// The route table, asserted over the SLICE rather than over the mux.
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

// every route in the table is rate limited.
func TestEveryRouteInTheTableIsRateLimited(t *testing.T) {
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

// They are not one budget — three of them, since R8.
func TestTheThreeBudgetsAreThreeBudgets(t *testing.T) {
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

// `TestMutatingAgreesWithTheMethod` was here and is deleted with its field
func TestEveryRouteWearsTheCeilingItsTableRowNames(t *testing.T) {
	for _, route := range Routes(newHarness(t, options{}).deps) {
		t.Run(route.Method+" "+route.Pattern, func(t *testing.T) {
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
				bearerHeader = bearer(t, h)
			}

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

// bodyFor is a request body each route will get past its own decoder.
func bodyFor(route Route) string {
	if strings.HasPrefix(route.Pattern, "/v1/media") {
		digest := strings.Repeat("a", 64)
		return `{"sha256":"` + digest + `","byteSize":10,"contentType":"image/png","ids":["` + digest + `"]}`
	}
	return aTrip
}

// The headers are on exactly the rows that declare them, and the assertion is
// on presence.
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

	if declared == 0 {
		t.Error("no route in the table declares NoStore, so this leg checked nothing")
	}
	if plain == 0 {
		t.Error("every route in the table declares NoStore, so the absence half " +
			"checked nothing")
	}
}

// A nil dependency panics at wiring time rather than at's first request,
// That is the argument already made for the limiter.
func TestMountRefusesToWireAHalfBuiltAPI(t *testing.T) {
	full := newHarness(t, options{}).deps

	for _, tc := range []struct {
		name  string
		strip func(*Deps)
		names string
	}{
		{"no credential rate limiter", func(d *Deps) { d.AuthLimit = nil }, "auth routes need a rate limiter"},
		{"no traveller rate limiter", func(d *Deps) { d.TravellerLimit = nil }, "authenticated routes need a rate limiter"},
		{"no public rate limiter", func(d *Deps) { d.PublicLimit = nil }, "public read needs a rate limiter"},
		{"no logbook store", func(d *Deps) { d.Logbook = nil }, "logbook routes need a store"},
		{"no logger", func(d *Deps) { d.Log = nil }, "routes need a logger"},
		{"no share store", func(d *Deps) { d.Share = nil }, "share routes need a store"},
		{"no public store", func(d *Deps) { d.Public = nil }, "public read needs a store"},
		{"no city store", func(d *Deps) { d.Cities = nil }, "city route needs a store"},
		{"no place store", func(d *Deps) { d.Places = nil }, "place routes need a store"},
		{"no photo store", func(d *Deps) { d.Photos = nil }, "photo routes need a store"},
		{"no walk store", func(d *Deps) { d.Walks = nil }, "walk route needs a store"},
		{"no media store", func(d *Deps) { d.Media = nil }, "media routes need a store"},
		{"no object store", func(d *Deps) { d.Objects = nil }, "media routes need an object store"},
		{"no MEDIA_MAX_BYTES", func(d *Deps) { d.MediaMaxBytes = 0 }, "MEDIA_MAX_BYTES is not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("Mount with %s did not panic", tc.name)
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("Mount panicked with %T, want the string it writes", r)
				}
				if !strings.Contains(msg, tc.names) {
					t.Errorf("Mount with %s panicked on a DIFFERENT dependency.\n"+
						"  got:  %s\n  want it to name: %s", tc.name, msg, tc.names)
				}
			}()
			deps := full
			tc.strip(&deps)
			Mount(http.NewServeMux(), deps)
		})
	}
}
