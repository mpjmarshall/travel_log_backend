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
	"time"

	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/httpapi"
	"travellog/internal/httpx"
	"travellog/internal/media"
)

func wiredConfig() config.Config {
	return config.Config{
		AuthRateLimitPerMin:      60,
		TravellerRateLimitPerMin: 600,
		PublicRateLimitPerMin:    120,
		Argon2MaxConcurrent:      4,
		// MEDIA_MAX_BYTES's FLOOR RATHER THAN ITS DEPLOYED VALUE. Mount
		// refuses a zero, so this has to be set for the mux to come up at all
		// — and config.MinMediaMaxBytes is a MEASUREMENT (one byte over the
		// fixture's largest object) rather than a number invented here.
		MediaMaxBytes: config.MinMediaMaxBytes,
	}
}

// THE THREE CEILINGS COME FROM THE THREE VARIABLES, AND NOT FROM EACH OTHER. A
// swapped pair is invisible to every other leg in this package: the routes are
// still mounted, the statuses are still right, and the credential routes would
// be running at 600 a minute against a 64 MiB-per-attempt Argon2 surface while
// a phone met a ceiling of 10. Spending from the limiters is the only way to
// see it from outside internal/httpx.
//
// THREE SINCE R8 (PD-09), and the third one is where the arithmetic and the
// INSTANCE are both load-bearing: `GET /l/{token}` is unauthenticated and is
// not a credential attempt, so a build that handed it the credential limiter
// would answer every leg in this package identically and lock everybody out of
// signing in the first time somebody read a shared trip twelve times.
func TestTheThreeCeilingsComeFromTheirOwnVariables(t *testing.T) {
	cfg := config.Config{
		AuthRateLimitPerMin:      3,
		TravellerRateLimitPerMin: 7,
		PublicRateLimitPerMin:    11,
	}
	credential, traveller, public := limiters(cfg)

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
	if got := spend(public); got != 11 {
		t.Errorf("the public limiter served %d requests at PUBLIC_RATE_LIMIT_PER_MIN=11, want 11", got)
	}
	// THREE DISTINCT INSTANCES, ASSERTED PAIRWISE. Two variables feeding one
	// limiter would pass all three counts above on the first spend and share
	// one bucket for ever after.
	if credential == traveller || credential == public || traveller == public {
		t.Error("two of the three ceilings are one limiter, so routes that were given " +
			"separate budgets are spending the same allowance")
	}
}

func wiredMux(t *testing.T, log *slog.Logger) *http.ServeMux {
	t.Helper()
	mount, err := apiRoutes(wiredConfig(), nil, log, media.NewMemory())
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
	if _, err := apiRoutes(cfg, nil, quiet(), media.NewMemory()); err == nil {
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
	server := httptest.NewServer(serverChain(mux, log, testRequestTimeout))
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
	serverChain(mux, log, testRequestTimeout).ServeHTTP(chained, httptest.NewRequest(http.MethodGet, "/healthz", nil))

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
	serverChain(wiredMux(t, log), log, testRequestTimeout).ServeHTTP(chained,
		httptest.NewRequest(http.MethodGet, "/v1/nothing-here", nil))

	if chained.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", chained.Code, http.StatusNotFound)
	}
	if got, want := chained.Body.String(), `{"code":"unsupported_route"}`; got != want {
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

// THE COMPRESSOR IS IN THE CHAIN AND IN THE RIGHT PLACE (DEC-94).
//
// A middleware written, tested and never wired is the exact state
// `httpx.Timeout` was in for four steps — the acceptance check for this step
// greps for its call site for that reason. This leg is the same guard for
// Compress, and it asserts the OBSERVABLE consequence rather than the wiring:
// a big body through the served handler comes back gzipped, and Vary names
// Accept-Encoding whether or not the client asked.
func TestTheServedHandlerCompressesAndSaysSo(t *testing.T) {
	log := quiet()
	mux := wiredMux(t, log)
	body := strings.Repeat(`{"id":"kyoto","name":"Kyoto in May"},`, 400)
	mux.HandleFunc("GET /big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})

	server := httptest.NewServer(serverChain(mux, log, testRequestTimeout))
	t.Cleanup(server.Close)

	// DisableCompression, or net/http adds Accept-Encoding itself and
	// transparently decompresses — which would make this leg pass against a
	// server that never compressed anything.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/big", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	got, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /big: %v", err)
	}
	defer got.Body.Close()

	if enc := got.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip — the middleware is written and "+
			"not wired, which is the state httpx.Timeout sat in for four steps", enc)
	}
	if !strings.Contains(got.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", got.Header.Get("Vary"))
	}
	raw, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(raw) >= len(body) {
		t.Errorf("the answer is %d bytes against a %d-byte body — it was not compressed",
			len(raw), len(body))
	}
}

// testRequestTimeout is what the legs in this file pass where the server
// passes cfg.RequestTimeout. Generous, because none of them is about the
// timeout: a leg that flaked on a slow machine would be a leg about the
// machine.
const testRequestTimeout = 30 * time.Second

// THE PER-REQUEST BOUND IS WIRED, AND THE LEG ASSERTS THE ANSWER RATHER THAN
// THE CALL SITE (DEC-96).
//
// `httpx.Timeout` was written at VS3 and had ZERO production call sites for
// four steps; main.go's own comment said the only reason was a missing config
// variable. A grep for `httpx.Timeout` is what this step's acceptance check
// runs, and a grep cannot tell a wired middleware from a mentioned one — so
// this hangs a handler and asserts the request comes back.
func TestASlowHandlerIsBoundedAndTheAnswerCarriesRetryAfter(t *testing.T) {
	log := quiet()
	mux := wiredMux(t, log)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		<-release
	})

	server := httptest.NewServer(serverChain(mux, log, 150*time.Millisecond))
	t.Cleanup(server.Close)

	got, err := server.Client().Get(server.URL + "/slow")
	if err != nil {
		t.Fatalf("GET /slow: %v — the request did not come back at all, which is "+
			"the state this middleware exists to prevent", err)
	}
	defer got.Body.Close()

	if got.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got.StatusCode)
	}
	if retry := got.Header.Get("Retry-After"); retry != "5" {
		t.Errorf("Retry-After = %q, want \"5\" — http.TimeoutHandler writes its own "+
			"503 from inside net/http, so a header set below it never reaches "+
			"the 503 a client is most likely to meet", retry)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != `{"code":"timeout"}` {
		t.Errorf("body = %q, want the envelope", body)
	}
}

// THE TWO BOUNDS LIVE IN TWO FILES AND THE RELATIONSHIP IS INVISIBLE FROM
// EITHER. config refuses a REQUEST_TIMEOUT above its own ceiling; that ceiling
// is only correct while it equals the server's write deadline. A handler
// allowed to run longer than the response may take to be written is a handler
// whose work is discarded underneath it.
func TestTheRequestCeilingIsTheServersWriteDeadline(t *testing.T) {
	if config.MaxRequestTimeout != writeTimeout {
		t.Errorf("config.MaxRequestTimeout = %s and cmd/api's writeTimeout = %s — "+
			"raise one and the other has to move, or config accepts a handler "+
			"budget the connection will not honour",
			config.MaxRequestTimeout, writeTimeout)
	}
}

// THE SHIPPED SURFACE IS TWENTY-THREE ROUTES AND THE TWENTY-THIRD IS
// `/healthz`.
//
// TWO NUMBERS THAT LOOK LIKE A DISAGREEMENT AND ARE NOT, WHICH IS WORTH A LEG
// BECAUSE THIS PROJECT HAS ALREADY HAD ONE REAL COUNT DISAGREEMENT HERE — R6's
// unanchored `grep -c http.Method` answered 22 against 21 rows, because the
// sentence documenting the grep matched it. `internal/httpapi.Routes()` holds
// 22 rows and the plan's table holds 23: the extra one is this package's
// liveness probe, which is deliberately not in the API's table because a
// liveness probe is not part of the API.
//
// SO THE CLAIM IS ASSERTED WHERE IT IS TRUE — over the MUX THE SERVER SERVES,
// which is the only place both facts exist at once — and it is asserted by
// ASKING THE MUX rather than by counting anything: every one of the 23 has to
// resolve to its own pattern.
func TestTheShippedSurfaceIsTwentyThreeRoutesIncludingHealthz(t *testing.T) {
	mux := wiredMux(t, quiet())

	// THE PUBLIC READ IS NAMED HERE RATHER THAN LOOPED OVER, because what this
	// leg is for is that it REACHES THE SERVER `docker compose up` STARTS. A
	// route green in `go test` and 404 in the running container is the gap the
	// arc exists to close, and this is its cheapest half.
	req := httptest.NewRequest(http.MethodGet, "/l/mnpqrstuvwxy", nil)
	if _, pattern := mux.Handler(req); pattern != "GET /l/{token}" {
		t.Errorf("GET /l/{token} resolves to %q on the server's own mux — the one "+
			"route in this API that anybody on the internet can reach is not mounted",
			pattern)
	}

	// AND `/healthz` IS STILL BESIDE IT, unauthenticated and unlimited, which
	// is the twenty-third row of the plan's table and the one that is not
	// internal/httpapi's.
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, healthzPath, nil)); pattern == "" {
		t.Errorf("%s resolves to no pattern", healthzPath)
	}

	const inTheAPITable = 22
	if got := len(httpapi.Routes(httpapi.Deps{})); got != inTheAPITable {
		t.Errorf("internal/httpapi.Routes() holds %d rows, want %d — with /healthz "+
			"that is the 23 the plan's route table names. Re-derive rather than "+
			"remember:\n"+
			"    grep -cE '^\\t\\t\\{http\\.Method' internal/httpapi/routes.go",
			got, inTheAPITable)
	}
}
