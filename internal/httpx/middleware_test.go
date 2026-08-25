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

// === Chain ===

// The fold direction is the whole of this function and it is invisible by
// inspection: reverse the loop and the chain still compiles, still runs every
// middleware exactly once, and runs them INSIDE OUT — recover innermost, where
// it catches nothing that happens above it.
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

// === Recover ===

// The panic value is the one string in this test that must NOT travel to the
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

// RECOVER IS OUTERMOST, so the request it holds predates the id. It reads the
// id off the response header the request-id middleware has already set on the
// shared ResponseWriter — without that, the one log line that matters most is
// the one line that cannot be correlated.
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

// http.ErrAbortHandler is the stdlib's own signal that a handler is abandoning
// the response ON PURPOSE. net/http suppresses its log and closes the
// connection; a recover that swallows it instead turns a deliberate abort into
// a 500 with a body, on a connection the handler has already given up on.
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

// A handler that wrote 200 and then panicked has already sent its status. A
// second WriteHeader is a no-op with a "superfluous" line in the server log,
// and appending the envelope to a body that is already half a document
// produces something the client cannot parse at all.
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

// === RequestID ===

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

// An inbound X-Request-Id is a string a stranger chose. It lands in every log
// line for the request, so trusting it hands anyone on the internet a way to
// forge, collide with or inject into the log — and there is no proxy in front
// of this server whose header could be trusted instead. Caddy is deferred, and
// so is the question of trusting anything it sets.
func TestAnInboundRequestIDIsNotTrusted(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "chosen-by-the-client")

	httpx.Chain(http.HandlerFunc(ok), httpx.RequestID()).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got == "chosen-by-the-client" {
		t.Errorf("the client's own id was adopted: %q", got)
	}
}

// === AccessLog ===

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

// A handler that writes a body and never calls WriteHeader has sent 200, and a
// recorder that reports 0 for it makes every successful request look like a
// request that answered nothing.
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

// The query string is not logged. DEC-10/DEC-11's share path is deferred, but
// its shape is settled: a capability lives in the URL. A logger that records
// query strings records capabilities, in plain text, forever.
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

// === Timeout ===

// http.TimeoutHandler writes its OWN body, which is the one response DEC-12's
// AST sweep structurally cannot see. So the handler is constructed with the
// envelope as its message — and the body must PARSE, not merely contain the
// word.
//
// The handler sleeps for a BOUNDED time rather than waiting on a channel the
// test closes at cleanup: with a no-op timeout — which is what a broken one
// looks like — a handler waiting on cleanup deadlocks the test binary instead
// of failing it.
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

// === The chain as it actually ships ===

// FIVE SINCE DEC-96. It was four, and the count is asserted rather than
// derived on purpose: a middleware silently added to the shipped chain is a
// behaviour nothing else in this package would notice.
func TestBaseIsTheFiveMiddlewaresInTheOrderTheChainNeeds(t *testing.T) {
	log, _ := testLogger()
	if got := len(httpx.Base(log, time.Minute)); got != 5 {
		t.Fatalf("Base has %d middlewares, want 5 — recover, request id, access log, "+
			"retry-after, timeout", got)
	}
}

// AND THE POSITION, PROVEN BY WHAT IT PRODUCES. Retry-After must be ABOVE
// Timeout: http.TimeoutHandler writes its own 503 from inside net/http, so a
// wrapper below it never sees that status at all. A leg reading the slice
// order would pass against a chain folded the other way; this one hangs a
// handler and reads the header off the answer.
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

// Order, proven by what it produces rather than by reading the slice. A panic
// answered as JSON proves recover is above the handler; the access line
// carrying the response's id proves request id is above access log; and an
// access line existing AT ALL for a timed-out request proves access log is
// above the timeout — inside it, TimeoutHandler would have returned first and
// the line would never be written.
//
// MEASURED CONSEQUENCE OF RECOVER BEING OUTERMOST, and the reason the access
// line's status is asserted to be 0: the access log's defer runs as the panic
// unwinds — BEFORE the outer recover has written anything — so the line records
// the status the HANDLER wrote, which is none. 0 is the honest answer and the
// request id is what joins the two lines. Do not "fix" this by moving recover
// inside the access log: it would then catch nothing that happens in the
// middlewares above it.
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

// === DEC-101: the access log answers a latency question ===

// MEASURED over 21.35 hours of the live stack, 15,960 access lines:
// `durationMs` is an int64 of MILLISECONDS, so 15,151 of them read
// `durationMs:0` and NO LATENCY QUESTION IS ANSWERABLE FROM THOSE LOGS AT ALL.
// The only non-zero values in the whole sample were 4 and 9.
//
// THE NON-ZERO ASSERTION IS THE ONE THAT MATTERS. A leg asserting only that
// the field EXISTS would have passed against the defect for 21 hours, which is
// exactly what happened.
//
// AND THE HANDLER SLEEPS FOR LESS THAN A MILLISECOND, WHICH IS THE WHOLE
// DESIGN OF THIS LEG. The first draft slept 2ms and the mutation — rename the
// field and keep `Milliseconds()` — SURVIVED IT, because two milliseconds is
// two milliseconds either way. That mutation is the one the ruling calls "a
// rename that looks like a fix and is not", so the leg has to run in the range
// where the two units disagree: 95% of this API's requests, and 15,151 of the
// 15,960 lines that were measured. 200µs is comfortably under one millisecond
// and comfortably over one microsecond, so neither assertion is a race.
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

// `travellerId` ON THE LINE WHERE AUTH RESOLVED ONE, AND ABSENT WHERE IT DID
// NOT. It costs nothing at one traveller, and it is the field that has to
// thread through middleware — which is the whole difficulty: the access log is
// ABOVE auth, and its deferred line runs against the request auth was HANDED,
// not the one auth created. A slot in the context is what closes that, and a
// leg that only checked the authenticated case would pass against a slot that
// is never cleared between requests.
//
// The redactor is keyed on substrings of token/passphrase/authorization, so an
// id is safe to log.
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

// THE MATCHED PATTERN BESIDE THE RAW PATH. `path` alone is the raw URL, so once
// `/v1/trips/{id}` exists nothing aggregates: every trip is its own line and
// "how slow is the trip write" has no query that answers it.
//
// IT IS RECORDED AND NOT DERIVED, because `http.ServeMux` sets `r.Pattern` on a
// CLONE it passes downwards — the outer request the access log holds never sees
// it. httpapi.Mount records it from the route table, which is the same string
// and is the one the table is already the authority for.
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

// /healthz OFF THE INFO LOG. The disk cost is survivable anywhere; the SIGNAL
// cost is a 20:1 dilution of the one file you read at 3am — the container
// probes every five seconds forever.
//
// IT IS DEMOTED AND NOT DROPPED, and it is demoted only while it is HEALTHY. A
// probe that fails is the most interesting line in the file.
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

// AND THE CONTROL: an ordinary route is NOT demoted. Without it the leg above
// is satisfied by an access log that writes everything at Debug.
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
