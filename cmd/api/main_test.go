// BACKFILL.
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

// portOf returns the ":<port>" form that -addr takes, from a test server's
// URL.
func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing test server URL %q: %v", rawURL, err)
	}
	return ":" + u.Port()
}

func TestHealthzAnswers200(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHealthzAnswersJSONContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

// The body is asserted as JSON rather than as a string, because what the
// client consumes is the decoded object.
func TestHealthzBodyDecodesToStatusOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

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

// The route is registered as "GET /healthz", which is net/http's
// method-pattern syntax and not decoration.
func TestHealthzRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET / = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestProbeExitsZeroWhenTheServerIsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if code := probe(portOf(t, srv.URL)); code != 0 {
		t.Errorf("probe = %d, want 0 against a 200", code)
	}
}

// The healthy-while-dead mutation.
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

// probe must ask for /healthz with a GET.
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

// The two halves wired together, which is what the container actually runs:
// the real mux behind a real listener, probed by the real flag.
func TestProbeAgreesWithTheRealMux(t *testing.T) {
	srv := httptest.NewServer(newMux(&stubPinger{}, quiet()))
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
