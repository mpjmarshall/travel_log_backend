// The wiring: that the two auth routes are on the mux the server serves, and
// that the chain around it does what it is there for. Test-first.
//
// WHAT IS ASSERTED HERE IS WIRING AND NOTHING ELSE. Every rule about what
// register and sign-in DO is in internal/httpapi, over the same handlers and
// the same middleware; repeating it here would be two suites of one thing.
// What only this package can say is whether the routes reach the server that
// `docker compose up` starts.
package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/httpx"
)

func wiredConfig() config.Config {
	return config.Config{
		AuthRateLimitPerMin:      60,
		TravellerRateLimitPerMin: 600,
		Argon2MaxConcurrent:      4,
	}
}

// THE TWO CEILINGS COME FROM THE TWO VARIABLES, AND NOT FROM EACH OTHER. A
// swapped pair is invisible to every other leg in this package: the routes are
// still mounted, the statuses are still right, and the credential routes would
// be running at 600 a minute against a 64 MiB-per-attempt Argon2 surface while
// a phone met a ceiling of 10. Spending from the two limiters is the only way
// to see it from outside internal/httpx.
func TestTheTwoCeilingsComeFromTheirOwnVariables(t *testing.T) {
	cfg := config.Config{AuthRateLimitPerMin: 3, TravellerRateLimitPerMin: 7}
	credential, traveller := limiters(cfg)

	spend := func(l *httpx.Limiter) int {
		served := 0
		for range 50 {
			if l.Allow("k") {
				served++
			}
		}
		return served
	}

	if got := spend(credential); got != 3 {
		t.Errorf("the credential limiter served %d requests at AUTH_RATE_LIMIT_PER_MIN=3, want 3", got)
	}
	if got := spend(traveller); got != 7 {
		t.Errorf("the traveller limiter served %d requests at TRAVELLER_RATE_LIMIT_PER_MIN=7, want 7", got)
	}
	if credential == traveller {
		t.Error("the two ceilings are one limiter, so the credential routes and the " +
			"authenticated ones spend the same allowance")
	}
}

func wiredMux(t *testing.T, log *slog.Logger) *http.ServeMux {
	t.Helper()
	mount, err := apiRoutes(wiredConfig(), nil, log)
	if err != nil {
		t.Fatalf("apiRoutes: %v", err)
	}
	return newMux(&stubPinger{}, log, mount)
}

func TestTheTwoAuthRoutesAreOnTheServersMux(t *testing.T) {
	mux := wiredMux(t, quiet())

	for _, path := range []string{"/v1/auth/register", "/v1/auth/session"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("POST %s resolves to no pattern — the route is not mounted, so the\n"+
				"    slice arc cannot get past its first request", path)
		}
		if want := "POST " + path; pattern != want {
			t.Errorf("POST %s resolves to %q, want %q", path, pattern, want)
		}
	}
}

func TestHealthzIsStillOnTheMuxBesideThem(t *testing.T) {
	mux := wiredMux(t, quiet())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d after the auth routes were mounted beside it", rec.Code)
	}
}

func TestApiRoutesRefusesAnArgon2CeilingBelowOne(t *testing.T) {
	cfg := wiredConfig()
	cfg.Argon2MaxConcurrent = 0
	if _, err := apiRoutes(cfg, nil, quiet()); err == nil {
		t.Errorf("apiRoutes accepted ARGON2_MAX_CONCURRENT=0.\n" +
			"    DEC-48: zero is not 'unlimited' — a zero-capacity semaphore blocks the\n" +
			"    first login for ever. config.Load floors it at 1 and this is the half\n" +
			"    that holds if a caller is not config.")
	}
}

// The chain around the mux. Timeout is deliberately NOT in it — see
// serverChain — so what is asserted is the three that are.
func TestTheServedHandlerCarriesARequestIdAndSurvivesAPanic(t *testing.T) {
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))

	mux := wiredMux(t, log)
	mux.HandleFunc("GET /panics", func(http.ResponseWriter, *http.Request) {
		panic("a handler panicked")
	})
	server := httptest.NewServer(serverChain(mux, log))
	t.Cleanup(server.Close)

	healthy, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthy.Body.Close()
	if healthy.Header.Get(httpx.RequestIDHeader) == "" {
		t.Errorf("no %s on the response — nothing in the log can be tied to a request",
			httpx.RequestIDHeader)
	}

	panicked, err := server.Client().Get(server.URL + "/panics")
	if err != nil {
		t.Fatalf("GET /panics: %v", err)
	}
	defer panicked.Body.Close()
	body, _ := io.ReadAll(panicked.Body)
	if panicked.StatusCode != http.StatusInternalServerError {
		t.Errorf("a panicking handler = %d, want 500", panicked.StatusCode)
	}
	if string(body) != `{"code":"internal"}` {
		t.Errorf("a panicking handler answered %q, want the envelope", body)
	}
	if strings.Contains(string(body), "a handler panicked") {
		t.Errorf("the panic value reached the body: %s", body)
	}
	if !strings.Contains(logs.String(), "a handler panicked") {
		t.Errorf("the panic value reached NEITHER the body nor the log:\n%s", logs.String())
	}
}

// The chain must not change what /healthz sends. VS1's probe asserts that body
// byte for byte, trailing newline included, and it talks to the SERVER rather
// than to the mux.
func TestTheChainLeavesHealthzsBodyExactlyAsItWas(t *testing.T) {
	log := quiet()
	mux := wiredMux(t, log)

	bare := httptest.NewRecorder()
	mux.ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	chained := httptest.NewRecorder()
	serverChain(mux, log).ServeHTTP(chained, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if bare.Body.String() != chained.Body.String() {
		t.Errorf("the chain changed /healthz's body:\n  bare:    %q\n  chained: %q",
			bare.Body.String(), chained.Body.String())
	}
	if bare.Code != chained.Code {
		t.Errorf("the chain changed /healthz's status: %d then %d", bare.Code, chained.Code)
	}
	if got := chained.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

// A last check that the wiring is the REAL thing rather than a stub beside it:
// the mounted route answers 422 for a body internal/auth refuses, which no
// placeholder handler would.
func TestTheMountedRegisterRouteIsTheRealHandler(t *testing.T) {
	mux := wiredMux(t, quiet())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register",
		strings.NewReader(`{"email":"not an address","passphrase":"a long enough passphrase"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("POST /v1/auth/register with a bad address = %d %s, want 422",
			rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"code":"invalid_field","field":"email"}` {
		t.Errorf("body = %q", got)
	}
	var _ auth.Traveller
}

// The route table is closed, so an unknown path is the FIRST thing a
// mistyped client meets — and until VS7 it met net/http's own plain text.
//
// THIS IS THE WIRING LEG AND NOT THE BEHAVIOUR LEG. What MuxErrors does to a
// 404 and a 405 is proven in internal/httpx over the same wrapper; what only
// this package can say is whether serverChain puts it around the mux the
// server serves.
func TestAnUnknownPathThroughTheServerChainCarriesTheEnvelope(t *testing.T) {
	log := quiet()
	chained := httptest.NewRecorder()
	serverChain(wiredMux(t, log), log).ServeHTTP(chained,
		httptest.NewRequest(http.MethodGet, "/v1/nothing-here", nil))

	if chained.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", chained.Code, http.StatusNotFound)
	}
	if got, want := chained.Body.String(), `{"code":"not_found"}`; got != want {
		t.Errorf("body = %q, want %q — net/http's own plain text reached the client", got, want)
	}
	if got, want := chained.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
}

// The four routes VS7 leaves on the server, asserted here for the reason the
// two auth ones are: what only this package can say is whether they reach the
// server `docker compose up` starts. What each of them DOES is proven in
// internal/httpapi, over the same handlers and the same middleware.
func TestTheLogbookRoutesAreOnTheServersMux(t *testing.T) {
	mux := wiredMux(t, quiet())

	for _, route := range []struct{ method, path, pattern string }{
		{http.MethodPost, "/v1/auth/register", "POST /v1/auth/register"},
		{http.MethodPost, "/v1/auth/session", "POST /v1/auth/session"},
		{http.MethodGet, "/v1/logbook", "GET /v1/logbook"},
		{http.MethodPut, "/v1/trips/kyoto", "PUT /v1/trips/{id}"},
	} {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
		_, pattern := mux.Handler(req)
		if pattern != route.pattern {
			t.Errorf("%s %s resolves to %q, want %q — the slice arc cannot get past it",
				route.method, route.path, pattern, route.pattern)
		}
	}
}
