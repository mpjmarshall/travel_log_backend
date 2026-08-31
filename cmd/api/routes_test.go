// The wiring: the auth routes are on the mux the server actually serves, and
// the middleware chain around them does its job.
package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"travellog/internal/mail"

	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/httpapi"
	"travellog/internal/httpx"
	"travellog/internal/media"
)

func wiredConfig() config.Config {
	return config.Config{
		Development:              true,
		AuthRateLimitPerMin:      60,
		TravellerRateLimitPerMin: 600,
		PublicRateLimitPerMin:    120,
		MediaMaxBytes:            config.MinMediaMaxBytes,
	}
}

// The three ceilings come from their own three variables, not from each other.
func TestTheThreeCeilingsComeFromTheirOwnVariables(t *testing.T) {
	cfg := config.Config{
		Development:              true,
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
	if credential == traveller || credential == public || traveller == public {
		t.Error("two of the three ceilings are one limiter, so routes that were given " +
			"separate budgets are spending the same allowance")
	}
}

func wiredMux(t *testing.T, log *slog.Logger) *http.ServeMux {
	t.Helper()
	mount, err := apiRoutes(wiredConfig(), nil, log, media.NewMemory(), mail.SenderFunc(
		func(context.Context, string, mail.Message) error { return nil }))
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

// The chain must not change what /healthz sends.
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

// A last check that the wiring is the real thing rather than a stub beside
// it.
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

// The route table is closed, so an unknown path is the first thing a mistyped
// client meets — and until it met net/http's own plain text.
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

// The four logbook routes are on the server's mux, asserted here for the same
// reason the auth ones are: only this package sees the assembled server.
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

// The compressor is in the chain and in the right place.
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
// passes cfg.RequestTimeout.
const testRequestTimeout = 30 * time.Second

// The per-request bound is wired, and the leg asserts the answer rather than
// the call site.
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

// The two bounds live in two files and the relationship is invisible from
// either.
func TestTheRequestCeilingIsTheServersWriteDeadline(t *testing.T) {
	if config.MaxRequestTimeout != writeTimeout {
		t.Errorf("config.MaxRequestTimeout = %s and cmd/api's writeTimeout = %s — "+
			"raise one and the other has to move, or config accepts a handler "+
			"budget the connection will not honour",
			config.MaxRequestTimeout, writeTimeout)
	}
}

// The shipped surface is twenty-three routes and the twenty-third is
// `/healthz`.
func TestTheShippedSurfaceIsTwentyFourRoutesIncludingHealthz(t *testing.T) {
	mux := wiredMux(t, quiet())

	req := httptest.NewRequest(http.MethodGet, "/l/mnpqrstuvwxy", nil)
	if _, pattern := mux.Handler(req); pattern != "GET /l/{token}" {
		t.Errorf("GET /l/{token} resolves to %q on the server's own mux — the one "+
			"route in this API that anybody on the internet can reach is not mounted",
			pattern)
	}

	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, healthzPath, nil)); pattern == "" {
		t.Errorf("%s resolves to no pattern", healthzPath)
	}

	const inTheAPITable = 24
	if got := len(httpapi.Routes(httpapi.Deps{})); got != inTheAPITable {
		t.Errorf("httpapi.Routes() holds %d rows, want %d — with /healthz "+
			"that is the 25 the route table names. Re-derive rather than "+
			"remember:\n"+
			"    grep -cE '^\\t\\t\\{http\\.Method' internal/httpapi/routes.go",
			got, inTheAPITable)
	}
}
