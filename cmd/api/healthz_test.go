// TEST-FIRST (agent-graph-spec-V4 §6.7). These legs were written and watched to
// fail against VS1's placeholder /healthz — the one that answers a constant —
// which is precisely the pre-state VS2's step text names for its mutation
// proof: "make healthz return a constant and the 503 leg reddens". The red
// output is in CLAUDE.md under "VS2".
//
// The legs in main_test.go are BACKFILL and say so in their own header. These
// are not, and are kept in a separate file so the two labels cannot blur.
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

// quiet is the logger every leg that is not ABOUT logging passes in. newMux
// takes a *slog.Logger rather than reaching for slog.Default() so a test can
// read what the handler wrote — see TestHealthzLogsTheDriverErrorItRefusesToShow.
func quiet() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// stubPinger stands in for *sql.DB. newMux takes the narrow interface rather
// than the concrete type precisely so the database-down branch is reachable
// without a database — the 503 leg is the one VS2 exists for, and a leg that
// needs Postgres running is a leg that gets skipped.
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

// THE LEG THE STEP IS FOR. VS1's /healthz answered 200 unconditionally, so a
// container whose database had gone was reported healthy by Docker and kept
// receiving traffic. Nothing else in the repository can tell that apart.
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

// The failure body is still JSON. A 503 that answers text is a 503 the client's
// decoder cannot read, and the envelope is the half most likely to be dropped
// on an error path.
func TestHealthzStaysJSONWhenTheDatabaseIsDown(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

// The ping must not leak into the body. It carries a DSN with a password in it,
// and /healthz is the one route reachable without authentication.
func TestHealthzDoesNotEchoTheDatabaseError(t *testing.T) {
	rec := healthz(t, &stubPinger{err: errDatabaseDown})

	if body := rec.Body.String(); contains(body, "connection refused") || contains(body, "5432") {
		t.Errorf("the driver error reached the response body: %s", body)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// The other half of the leg above, and the pair is the point: the detail the
// body refuses to carry is not thrown away, it goes to the log. DEC-12 states
// the rule for the API's error envelope — "real detail goes to slog, never to
// the body" — and /healthz predates that envelope, so it is written here
// directly. A 503 whose cause is nowhere is an outage nobody can diagnose.
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

// And the healthy path is silent. A route Docker hits every five seconds must
// not write a line every five seconds — that is how a log stops being read.
func TestHealthzIsSilentWhenTheDatabaseIsUp(t *testing.T) {
	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	newMux(&stubPinger{}, logging.New(&buf, slog.LevelInfo)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if buf.Len() != 0 {
		t.Errorf("a healthy probe wrote a log line:\n%s", buf.String())
	}
}

// A cached answer is the same defect as a constant one, arriving later. Docker
// probes every five seconds and the whole value of the route is that the fifth
// probe can disagree with the fourth.
func TestHealthzPingsOnEveryRequest(t *testing.T) {
	db := &stubPinger{}
	for i := 1; i <= 3; i++ {
		healthz(t, db)
		if db.calls != i {
			t.Fatalf("after %d requests the database was pinged %d times, want %d", i, db.calls, i)
		}
	}
}

// spec L22: "Use the standard `context` package to handle request timeouts."
// An unbounded Ping against a wedged server holds the handler until the
// client's own deadline, and the container's HEALTHCHECK timeout is 3s — so a
// deadline-free ping turns a slow database into a hung probe rather than an
// unhealthy report.
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

// The container's two halves, wired: the real mux with a dead database behind a
// real listener, probed by the real -healthcheck flag. VS1 proved probe reports
// a non-200 as unhealthy; this is the leg that says the non-200 actually
// happens when Postgres goes, which is the whole chain Docker relies on.
func TestProbeReportsUnhealthyWhenTheDatabaseIsDown(t *testing.T) {
	srv := httptest.NewServer(newMux(&stubPinger{err: errDatabaseDown}, quiet()))
	defer srv.Close()

	if code := probe(portOf(t, srv.URL)); code != 1 {
		t.Errorf("probe = %d against a mux whose database is down, want 1", code)
	}
}
