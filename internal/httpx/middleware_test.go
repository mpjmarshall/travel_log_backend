package httpx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"travellog/internal/httpx"
)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// logLines decodes the buffer into one map per line.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// firstLine is firstLine(t, buf) with a message instead of an index panic —
// "there were no log lines" is a result this suite has to be able to report.
func firstLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := logLines(t, buf)
	if len(lines) == 0 {
		t.Fatalf("no log lines at all")
	}
	return lines[0]
}

// The fold direction is the whole of this function and it is invisible by
// inspection.
func TestChainAppliesTheFirstMiddlewareOutermost(t *testing.T) {
	var order []string
	mark := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "enter "+name)
				next.ServeHTTP(w, r)
				order = append(order, "leave "+name)
			})
		}
	}

	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), mark("outer"), mark("middle"), mark("inner"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{
		"enter outer", "enter middle", "enter inner",
		"handler",
		"leave inner", "leave middle", "leave outer",
	}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v\nwant  = %v", order, want)
	}
}

func TestChainWithNoMiddlewareIsTheHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Chain(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// The panic value is the one string in this test that must not travel to the
// wire.
func TestAPanickingHandlerAnswers500AndTheEnvelope(t *testing.T) {
	log, _ := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("the traveller id was nil, and here is the DSN: postgres://u:p@host/db")
	}), httpx.Recover(log)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := rec.Body.String(); got != `{"code":"internal"}` {
		t.Errorf("body = %s, want {\"code\":\"internal\"}", got)
	}
	if strings.Contains(rec.Body.String(), "postgres://") {
		t.Errorf("the panic value reached the wire: %s", rec.Body.String())
	}
}

// recover is outermost, so the request it holds predates the id.
func TestThePanicItselfGoesToTheLogWithTheRequestID(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil traveller")
	}), httpx.Recover(log), httpx.RequestID()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("no X-Request-Id on the response")
	}

	var found map[string]any
	for _, line := range logLines(t, buf) {
		if strings.Contains(line["msg"].(string), "panic") {
			found = line
		}
	}
	if found == nil {
		t.Fatalf("no panic line in the log: %s", buf.String())
	}
	if !strings.Contains(found["panic"].(string), "nil traveller") {
		t.Errorf("the panic line does not carry the value: %v", found)
	}
	if found["requestId"] != id {
		t.Errorf("panic line requestId = %v, want %q", found["requestId"], id)
	}
	if _, ok := found["stack"]; !ok {
		t.Errorf("the panic line carries no stack: %v", found)
	}
}

// http.ErrAbortHandler is the stdlib's own signal that a handler is
// abandoning the response on purpose.
func TestRecoverRepanicsErrAbortHandler(t *testing.T) {
	log, _ := testLogger()
	rec := httptest.NewRecorder()

	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Errorf("recovered %v, want ErrAbortHandler to have been re-panicked", p)
		}
	}()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}), httpx.Recover(log)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
}

// A handler that wrote 200 and then panicked has already sent its status.
func TestRecoverDoesNotWriteOverAResponseThatHasStarted(t *testing.T) {
	log, _ := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, trip{ID: "kyoto"})
		panic("after the write")
	}), httpx.Recover(log)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the 200 the handler already sent", rec.Code)
	}
	if got := rec.Body.String(); got != `{"id":"kyoto","title":""}` {
		t.Errorf("body = %s, want the handler's own body unamended", got)
	}
}

func TestRequestIDIsOnTheResponseAndInTheContext(t *testing.T) {
	rec := httptest.NewRecorder()
	var fromContext string

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromContext = httpx.RequestIDFrom(r.Context())
	}), httpx.RequestID()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	header := rec.Header().Get("X-Request-Id")
	if header == "" {
		t.Fatal("no X-Request-Id header")
	}
	if fromContext != header {
		t.Errorf("context id = %q, header = %q — they must be one value", fromContext, header)
	}
	if len(header) != 32 {
		t.Errorf("id = %q (%d chars), want 32 hex characters", header, len(header))
	}
}

func TestTwoRequestsGetTwoIDs(t *testing.T) {
	mw := httpx.RequestID()
	h := mw(http.HandlerFunc(ok))

	first := httptest.NewRecorder()
	second := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))

	if a, b := first.Header().Get("X-Request-Id"), second.Header().Get("X-Request-Id"); a == b {
		t.Errorf("two requests share one id: %q", a)
	}
}

// An inbound X-Request-Id is a string a stranger chose.
func TestAnInboundRequestIDIsNotTrusted(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "chosen-by-the-client")

	httpx.Chain(http.HandlerFunc(ok), httpx.RequestID()).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got == "chosen-by-the-client" {
		t.Errorf("the client's own id was adopted: %q", got)
	}
}

func TestAccessLogWritesOneLineWithTheRequestsFacts(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusCreated, trip{ID: "kyoto"})
	}), httpx.RequestID(), httpx.AccessLog(log)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/register", nil))

	lines := logLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want exactly 1: %s", len(lines), buf.String())
	}
	line := lines[0]
	if line["method"] != "POST" || line["path"] != "/v1/auth/register" {
		t.Errorf("method/path = %v %v", line["method"], line["path"])
	}
	if line["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", line["status"])
	}
	if line["bytes"] != float64(len(`{"id":"kyoto","title":""}`)) {
		t.Errorf("bytes = %v", line["bytes"])
	}
	if line["requestId"] != rec.Header().Get("X-Request-Id") {
		t.Errorf("requestId = %v, want %q", line["requestId"], rec.Header().Get("X-Request-Id"))
	}
	if _, ok := line["durationUs"]; !ok {
		t.Errorf("no durationUs: %v", line)
	}
}

// A handler that writes a body and never calls WriteHeader has sent 200.
func TestAnImplicit200IsLoggedAs200(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}), httpx.AccessLog(log)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := firstLine(t, buf)["status"]; got != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", got)
	}
}

// The query string is not logged.
func TestTheQueryStringIsNotLogged(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(ok), httpx.AccessLog(log)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook?token=abcdef123456", nil))

	if strings.Contains(buf.String(), "abcdef123456") {
		t.Errorf("the query string reached the log: %s", buf.String())
	}
	if got := firstLine(t, buf)["path"]; got != "/v1/logbook" {
		t.Errorf("path = %v, want the path without its query", got)
	}
}

func TestAFailedRequestIsLoggedAtError(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.CodeInternal)
	}), httpx.AccessLog(log)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := firstLine(t, buf)["level"]; got != "ERROR" {
		t.Errorf("level = %v, want ERROR for a 500", got)
	}
}

// http.TimeoutHandler writes its own body, which is the one response's AST
// sweep structurally cannot see.
func TestATimedOutRequestGetsTheJSONEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}), httpx.Timeout(10*time.Millisecond)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json — TimeoutHandler sets none of its own", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the timeout body is not JSON: %q: %v", rec.Body.String(), err)
	}
	if len(body) != 1 || body["code"] != "timeout" {
		t.Errorf("body = %v, want exactly {code: timeout}", body)
	}
}

func TestAFastHandlerPassesThroughTheTimeoutUntouched(t *testing.T) {
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("id,title\n"))
	}), httpx.Timeout(time.Minute)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the handler's own 418", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q — the timeout's default overwrote the handler's own", ct)
	}
	if rec.Body.String() != "id,title\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// five since.
func TestBaseIsTheFiveMiddlewaresInTheOrderTheChainNeeds(t *testing.T) {
	log, _ := testLogger()
	if got := len(httpx.Base(log, time.Minute)); got != 5 {
		t.Fatalf("Base has %d middlewares, want 5 — recover, request id, access log, "+
			"retry-after, timeout", got)
	}
}

// The position, proven by what it produces.
func TestTheTimeoutsOwn503CarriesRetryAfter(t *testing.T) {
	log, _ := testLogger()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	rec := httptest.NewRecorder()
	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}), httpx.Base(log, 10*time.Millisecond)...).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q on the timeout's own 503, want \"5\" — this is "+
			"the 503 a client meets most often, and it is the one a header set "+
			"below TimeoutHandler cannot reach", got)
	}
}

// Order, proven by what it produces rather than by reading the slice.
func TestThroughTheWholeChainAPanicIsAJSON500WithACorrelatedAccessLine(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil traveller")
	}), httpx.Base(log, time.Minute)...).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"code":"internal"}` {
		t.Errorf("status/body = %d %s", rec.Code, rec.Body.String())
	}

	id := rec.Header().Get("X-Request-Id")
	var access, panicLine map[string]any
	for _, line := range logLines(t, buf) {
		if _, isAccess := line["durationUs"]; isAccess {
			access = line
		}
		if _, isPanic := line["panic"]; isPanic {
			panicLine = line
		}
	}
	if access == nil || panicLine == nil {
		t.Fatalf("want both an access line and a panic line, got: %s", buf.String())
	}
	if access["requestId"] != id || panicLine["requestId"] != id {
		t.Errorf("the two lines are not correlated: access=%v panic=%v header=%q",
			access["requestId"], panicLine["requestId"], id)
	}
	if access["status"] != float64(0) {
		t.Errorf("access status = %v, want 0 — see this test's doc comment", access["status"])
	}
}

func TestThroughTheWholeChainATimeoutIsLoggedAndAnswered(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}), httpx.Base(log, 10*time.Millisecond)...).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var access map[string]any
	for _, line := range logLines(t, buf) {
		if _, isAccess := line["durationUs"]; isAccess {
			access = line
		}
	}
	if access == nil {
		t.Fatalf("no access line for a timed-out request: %s", buf.String())
	}
	if access["status"] != float64(http.StatusServiceUnavailable) {
		t.Errorf("access status = %v, want 503", access["status"])
	}
}

// MEASURED over 21.35 hours of the live stack, 15,960 access lines.
func TestTheAccessLineCarriesMicrosecondsAndTheyAreNotZero(t *testing.T) {
	log, buf := testLogger()
	rec := httptest.NewRecorder()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Microsecond)
		httpx.WriteJSON(w, r, http.StatusOK, trip{ID: "kyoto"})
	}), httpx.RequestID(), httpx.AccessLog(log)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	line := firstLine(t, buf)
	if _, stale := line["durationMs"]; stale {
		t.Errorf("the line still carries durationMs: %v — milliseconds round a "+
			"sub-millisecond request to zero, which is 95%% of them", line)
	}
	got, held := line["durationUs"].(float64)
	if !held {
		t.Fatalf("no durationUs: %v", line)
	}
	if got <= 0 {
		t.Errorf("durationUs = %v after a 200µs sleep — a rename that keeps the "+
			"unit is a rename that looks like a fix and is not, and this is the "+
			"range where the two units disagree", got)
	}
}

// `travellerId` on the line where auth resolved one, and absent where it did
// not.
func TestTheAccessLineNamesTheTravellerWhenThereIsOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"an authenticated request", func(w http.ResponseWriter, r *http.Request) {
			httpx.RecordTraveller(r.Context(), "6f0f8e12-0000-4000-8000-000000000001")
			w.WriteHeader(http.StatusOK)
		}, "6f0f8e12-0000-4000-8000-000000000001"},
		{"a request nobody signed in", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := testLogger()
			httpx.Chain(tc.handler, httpx.RequestID(), httpx.AccessLog(log)).
				ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

			line := firstLine(t, buf)
			got, _ := line["travellerId"].(string)
			if got != tc.want {
				t.Errorf("travellerId = %q, want %q", got, tc.want)
			}
			if tc.want == "" {
				if _, held := line["travellerId"]; held {
					t.Errorf("travellerId is PRESENT and empty on an unauthenticated "+
						"line: %v — an empty field is a field a query has to "+
						"special-case", line)
				}
			}
		})
	}
}

// the matched pattern beside the raw path.
func TestTheAccessLineCarriesTheMatchedPatternBesideTheRawPath(t *testing.T) {
	log, buf := testLogger()

	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.RecordRoute(r.Context(), "PUT /v1/trips/{id}")
		w.WriteHeader(http.StatusOK)
	}), httpx.RequestID(), httpx.AccessLog(log)).
		ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/v1/trips/autumn-crossing", nil))

	line := firstLine(t, buf)
	if line["route"] != "PUT /v1/trips/{id}" {
		t.Errorf("route = %v, want the matched pattern", line["route"])
	}
	if line["path"] != "/v1/trips/autumn-crossing" {
		t.Errorf("path = %v, want the raw path — the pattern is BESIDE it and not "+
			"instead of it, or a request for one particular trip becomes "+
			"untraceable", line["path"])
	}
}

// /healthz off the info log.
func TestHealthzIsOffTheInfoLogWhileItIsHealthy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"a healthy probe", http.StatusOK, "DEBUG"},
		{"a probe that failed", http.StatusServiceUnavailable, "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := testLogger()
			httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}), httpx.RequestID(), httpx.AccessLog(log, "/healthz")).
				ServeHTTP(httptest.NewRecorder(),
					httptest.NewRequest(http.MethodGet, "/healthz", nil))

			line := firstLine(t, buf)
			if line["level"] != tc.want {
				t.Errorf("level = %v, want %v", line["level"], tc.want)
			}
		})
	}
}

// The control: an ordinary route is not demoted.
func TestAnOrdinaryRequestStaysAtInfo(t *testing.T) {
	log, buf := testLogger()
	httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), httpx.RequestID(), httpx.AccessLog(log, "/healthz")).
		ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if got := firstLine(t, buf)["level"]; got != "INFO" {
		t.Errorf("level = %v, want INFO", got)
	}
}
