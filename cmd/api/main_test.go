// BACKFILL (VS1-BACKFILL). These tests were written AFTER the code they guard.
// VS1 shipped before the project adopted test-first (agent-graph-spec-V4 §6.7),
// and a test cannot be retroactively written first. The substitute, which gives
// the same evidence in a different order, is that every leg below was watched
// to go RED against a stated mutation of its subject and GREEN again once the
// mutation was reverted. The mutations and their actual output are recorded in
// CLAUDE.md under "VS1-BACKFILL". A test that has never been red has never been
// shown to work; the mutation is the only thing making a backfilled test worth
// having.
//
// Standard library only: testing, net/http/httptest. No dependency is added.
package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// portOf returns the ":<port>" form that -addr takes, from a test server's URL.
// probe is given an addr and dials 127.0.0.1 on that addr's port, exactly as it
// does inside the container, so this is the real argument shape and not a stub.
func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing test server URL %q: %v", rawURL, err)
	}
	return ":" + u.Port()
}

// --- the /healthz handler -------------------------------------------------

func TestHealthzAnswers200(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHealthzAnswersJSONContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

// The body is asserted as JSON rather than as a string, because what the client
// consumes is the decoded object. VS2 replaces the constant with a real database
// ping and this leg is what says the envelope survived that.
func TestHealthzBodyDecodesToStatusOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if body.Status != "ok" {
		t.Errorf(`status field = %q, want "ok"`, body.Status)
	}
}

// The route is registered as "GET /healthz", which is net/http's method-pattern
// syntax and not decoration: without the method the same handler would answer a
// POST. Docker's HEALTHCHECK only ever issues a GET, so this leg guards the
// pattern rather than the probe.
func TestHealthzRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET / = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// --- the -healthcheck flag ------------------------------------------------
//
// This is the load-bearing half. The runtime image is `scratch`: no shell, no
// curl, so Docker's HEALTHCHECK has nothing to invoke but this flag. Exit 0 when
// the server is dead reports a healthy container that serves nothing; exit 1
// when it is alive means the stack never comes up, because compose's `api`
// service is what `--wait` waits on.

func TestProbeExitsZeroWhenTheServerIsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if code := probe(portOf(t, srv.URL)); code != 0 {
		t.Errorf("probe = %d, want 0 against a 200", code)
	}
}

// The healthy-while-dead mutation. A server that is listening but answering 503
// is exactly what VS2's database-down branch produces, and a probe that shrugs
// at it is worse than no HEALTHCHECK at all.
func TestProbeExitsOneWhenTheServerAnswersNon200(t *testing.T) {
	for _, status := range []int{
		http.StatusServiceUnavailable,
		http.StatusInternalServerError,
		http.StatusNotFound,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		if code := probe(portOf(t, srv.URL)); code != 1 {
			t.Errorf("probe = %d against a %d, want 1", code, status)
		}
		srv.Close()
	}
}

// Nothing listening at all — the container's first seconds, and the state a
// crashed binary leaves behind.
func TestProbeExitsOneWhenNothingIsListening(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ":" + strings.TrimPrefix(l.Addr().String(), "127.0.0.1:")
	if err := l.Close(); err != nil {
		t.Fatalf("closing the reserved listener: %v", err)
	}

	if code := probe(addr); code != 1 {
		t.Errorf("probe = %d against a closed port, want 1", code)
	}
}

func TestProbeExitsOneOnAnAddrItCannotSplit(t *testing.T) {
	if code := probe("not-a-host-port"); code != 1 {
		t.Errorf("probe = %d on a malformed addr, want 1", code)
	}
}

// probe must ask for /healthz with a GET. Asking for "/" would 404 against the
// real mux and report every healthy container as sick; asking with the wrong
// method would 405 for the same effect.
func TestProbeRequestsGETHealthz(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	probe(portOf(t, srv.URL))

	if gotMethod != http.MethodGet {
		t.Errorf("probe used %s, want GET", gotMethod)
	}
	if gotPath != "/healthz" {
		t.Errorf("probe asked for %q, want %q", gotPath, "/healthz")
	}
}

// The two halves wired together, which is what the container actually runs: the
// real mux behind a real listener, probed by the real flag. Either half can be
// correct in isolation and disagree here — a probe on the wrong path, or a route
// that lost its method pattern, shows up in this leg and in no other.
func TestProbeAgreesWithTheRealMux(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()

	if code := probe(portOf(t, srv.URL)); code != 0 {
		t.Errorf("probe = %d against the real mux, want 0", code)
	}

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != `{"status":"ok"}`+"\n" {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`+"\n")
	}
}
