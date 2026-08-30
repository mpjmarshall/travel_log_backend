// TEST-FIRST (agent-graph-spec-V4 §6.7).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/logging"
)

// quiet is the logger every leg that is not ABOUT logging passes in.
func quiet() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// stubPinger stands in for *sql.DB.
type stubPinger struct {
	err         error
	calls       int
	hadDeadline bool
}

func (p *stubPinger) PingContext(ctx context.Context) error {
	p.calls++
	_, p.hadDeadline = ctx.Deadline()
	return p.err
}

var errDatabaseDown = errDown{}

type errDown struct{}

func (errDown) Error() string { return "dial tcp 127.0.0.1:5432: connect: connection refused" }

func healthz(t *testing.T, db *stubPinger) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	newMux(db, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return rec
}

// the leg the step is for.
func TestHealthzAnswers503WhenTheDatabasePingFails(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthzReportsUnavailableInTheBodyWhenTheDatabasePingFails(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if body.Status != "unavailable" {
		t.Errorf(`status field = %q, want "unavailable"`, body.Status)
	}
}

// The failure body is still JSON.
func TestHealthzStaysJSONWhenTheDatabaseIsDown(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

// The ping must not leak into the body.
func TestHealthzDoesNotEchoTheDatabaseError(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	if body := rec.Body.String(); contains(body, "connection refused") || contains(body, "5432") {
		t.Errorf("the driver error reached the response body: %s", body)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// The other half of the leg above, and the pair is the point: the detail the
// body refuses to carry is not thrown away, it goes to the log.
func TestHealthzLogsTheDriverErrorItRefusesToShow(t *testing.T) {
	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	newMux(&stubPinger{err: errDatabaseDown}, logging.New(&buf, slog.LevelInfo)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("the driver error was not logged:\n%s", buf.String())
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Errorf("the driver error reached the body: %s", rec.Body.String())
	}
}

// And the healthy path is silent.
func TestHealthzIsSilentWhenTheDatabaseIsUp(t *testing.T) {
	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, logging.New(&buf, slog.LevelInfo)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if buf.Len() != 0 {
		t.Errorf("a healthy probe wrote a log line:\n%s", buf.String())
	}
}

// A cached answer is the same defect as a constant one, arriving later.
func TestHealthzPingsOnEveryRequest(t *testing.T) {
	db := &stubPinger{}
	for i := 1; i <= 3; i++ {
		healthz(t, db)
		if db.calls != i {
			t.Fatalf("after %d requests the database was pinged %d times, want %d", i, db.calls, i)
		}
	}
}

// An unbounded Ping against a wedged server holds the handler open, so the
// probe must carry its own deadline.
func TestHealthzPingsWithADeadline(t *testing.T) {
	db := &stubPinger{}
	healthz(t, db)

	if !db.hadDeadline {
		t.Error("the ping context carried no deadline")
	}
}

func TestHealthzStillAnswers200WhenTheDatabaseIsUp(t *testing.T) {
	rec := healthz(t, &stubPinger{})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// The container's two halves, wired: the real mux with a dead database behind
// a real listener, probed by the real -healthcheck flag.
func TestProbeReportsUnhealthyWhenTheDatabaseIsDown(t *testing.T) {
	srv := httptest.NewServer(newMux(&stubPinger{err: errDatabaseDown}, quiet()))
	defer srv.Close()

	if code := probe(portOf(t, srv.URL)); code != 1 {
		t.Errorf("probe = %d against a mux whose database is down, want 1", code)
	}
}
