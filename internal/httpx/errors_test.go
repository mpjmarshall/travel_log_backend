package httpx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"travellog/internal/httpx"
)

// decCodes is DEC-12's vocabulary, written out here as the DECISION's list —
// deliberately a second copy, so this leg fails when the const block and the
// decision disagree in EITHER direction. The count is never carried: it is
// len() of this literal, and a separate leg derives the same number from the
// source with grep.
var decCodes = []string{
	"conflict",
	"forbidden",
	"internal",
	"invalid_body",
	"invalid_field",
	"not_found",
	"payload_too_large",
	"rate_limited",
	"timeout",
	"unauthenticated",
	"unsupported_format",
	"upload_incomplete",
}

func TestTheVocabularyIsExactlyTheTwelveCodesDEC12Names(t *testing.T) {
	got := make([]string, 0, len(httpx.Codes()))
	for _, c := range httpx.Codes() {
		got = append(got, string(c))
	}
	sort.Strings(got)

	if len(got) != 12 {
		t.Fatalf("the vocabulary holds %d codes, want 12: %v", len(got), got)
	}
	for i := range decCodes {
		if got[i] != decCodes[i] {
			t.Fatalf("vocabulary = %v, want %v", got, decCodes)
		}
	}
}

// Totality in both directions. A code with no status would answer 500 silently;
// a status entry for a word not in the block is a vocabulary that has grown
// without anybody saying so.
func TestEveryCodeHasAStatusAndNoStatusIsOrphaned(t *testing.T) {
	for _, c := range httpx.Codes() {
		if got := httpx.StatusFor(c); got < 400 || got > 599 {
			t.Errorf("StatusFor(%q) = %d, want a 4xx or 5xx", c, got)
		}
	}
	if got := len(httpx.Codes()); got != 12 {
		t.Errorf("Codes() = %d entries, want 12", got)
	}
}

func TestTheStatusOfEachCodeIsTheOneItsDecisionNames(t *testing.T) {
	want := map[httpx.Code]int{
		httpx.CodeUnauthenticated:   http.StatusUnauthorized,
		httpx.CodeForbidden:         http.StatusForbidden,
		httpx.CodeNotFound:          http.StatusNotFound,
		httpx.CodeConflict:          http.StatusConflict,
		httpx.CodeInvalidBody:       http.StatusBadRequest,
		httpx.CodeInvalidField:      http.StatusUnprocessableEntity,
		httpx.CodeRateLimited:       http.StatusTooManyRequests,
		httpx.CodePayloadTooLarge:   http.StatusRequestEntityTooLarge,
		httpx.CodeTimeout:           http.StatusServiceUnavailable,
		httpx.CodeInternal:          http.StatusInternalServerError,
		httpx.CodeUploadIncomplete:  http.StatusConflict,
		httpx.CodeUnsupportedFormat: http.StatusNotAcceptable,
	}
	if len(want) != 12 {
		t.Fatalf("this table holds %d rows, want 12", len(want))
	}
	for c, status := range want {
		if got := httpx.StatusFor(c); got != status {
			t.Errorf("StatusFor(%q) = %d, want %d", c, got, status)
		}
	}
}

// TestTimeoutIsWhatTheStdlibItselfWrites is the reason CodeTimeout is 503 and
// not 504. http.TimeoutHandler writes StatusServiceUnavailable itself, and the
// handler cannot change it. A mapping that said 504 would disagree with the one
// response in the app that the mapping does not get to produce.
func TestTimeoutIsWhatTheStdlibItselfWrites(t *testing.T) {
	if got := httpx.StatusFor(httpx.CodeTimeout); got != http.StatusServiceUnavailable {
		t.Errorf("StatusFor(timeout) = %d, want %d — what http.TimeoutHandler writes",
			got, http.StatusServiceUnavailable)
	}
}

// DEC-12: "no prose, ever". The key SET is the assertion, not the presence of
// `code` — a body carrying `code` plus a `message` satisfies a contains-check
// and is exactly what the decision forbids.
func TestWriteErrorWritesTheCodeAndNothingElse(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)

	httpx.WriteError(rec, req, httpx.CodeNotFound)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := rec.Body.String(); got != `{"code":"not_found"}` {
		t.Errorf("body = %s, want {\"code\":\"not_found\"}", got)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("body keys = %v, want exactly [code]", got)
	}
}

func TestWriteFieldErrorAddsFieldAndOnlyField(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/trips/kyoto", nil)

	httpx.WriteFieldError(rec, req, "title")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(got) != 2 || got["code"] != "invalid_field" || got["field"] != "title" {
		t.Errorf("body = %v, want exactly {code: invalid_field, field: title}", got)
	}
}

// The `field` key is DEC-12's ONE optional additive key, and optional means
// absent rather than empty: `"field":""` would have the client render a blank
// name into a per-surface sentence.
func TestFieldIsOmittedRatherThanEmptyOnEveryOtherCode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)

	httpx.WriteError(rec, req, httpx.CodeInvalidField)

	if got := rec.Body.String(); got != `{"code":"invalid_field"}` {
		t.Errorf("body = %s, want no field key at all", got)
	}
}

// A Code reached by conversion — httpx.Code("banana") — is the one-word bypass
// the AST sweep is built to catch at review time. This is the runtime half:
// even if one lands, no word outside the vocabulary reaches the wire.
func TestAnUnknownCodeIsWrittenAsInternalRatherThanEchoed(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)

	httpx.WriteError(rec, req, httpx.Code("banana"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Body.String(); got != `{"code":"internal"}` {
		t.Errorf("body = %s, want {\"code\":\"internal\"} — never the invented word", got)
	}
}

// === The mapping function DEC-62 asks for: the sentinel is the domain's word,
// the code is the wire's. ===

type fakeDomainError struct{ code httpx.Code }

func (e fakeDomainError) Error() string         { return "fake domain error" }
func (e fakeDomainError) ErrorCode() httpx.Code { return e.code }

func TestCodeForMapsSentinelsWrappersAndCoders(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want httpx.Code
	}{
		{"the too-large sentinel", httpx.ErrBodyTooLarge, httpx.CodePayloadTooLarge},
		{"the invalid-body sentinel", httpx.ErrInvalidBody, httpx.CodeInvalidBody},
		{"a WRAPPED sentinel", fmt.Errorf("decoding: %w", httpx.ErrInvalidBody), httpx.CodeInvalidBody},
		{"a domain error naming its own code", fakeDomainError{httpx.CodeConflict}, httpx.CodeConflict},
		{"a WRAPPED domain error", fmt.Errorf("service: %w", fakeDomainError{httpx.CodeNotFound}), httpx.CodeNotFound},
		{"anything else", errors.New("the disk is on fire"), httpx.CodeInternal},
		{"nil", nil, httpx.CodeInternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := httpx.CodeFor(c.err); got != c.want {
				t.Errorf("CodeFor(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// A domain error can name any word it likes; the vocabulary is still closed.
// Without this, DEC-62's seam is the hole DEC-12's sweep cannot see, because
// the word is a runtime value and no AST walk reaches it.
func TestADomainErrorCannotInventAWireWord(t *testing.T) {
	if got := httpx.CodeFor(fakeDomainError{httpx.Code("teapot")}); got != httpx.CodeInternal {
		t.Errorf("CodeFor(a Coder returning an unknown word) = %q, want internal", got)
	}
}

func TestWriteErrorForGoesThroughTheSameMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)

	httpx.WriteErrorFor(rec, req, fmt.Errorf("reading the body: %w", httpx.ErrBodyTooLarge))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if got := rec.Body.String(); got != `{"code":"payload_too_large"}` {
		t.Errorf("body = %s", got)
	}
}

// === DEC-96: a dependency that is down is a 503, not a 500 ===

// A 500 TELLS A CLIENT THE SERVER HAS A BUG — do not retry, the request is
// poison — WHEN THE TRUTH IS "the dependency is down, try again shortly".
// Measured by the operations lens: with Postgres killed, every route answered
// `500 {"code":"internal"}` with no Retry-After. It is also unanswerable
// afterwards, because a 500 count then conflates handler bugs with outages.
//
// THIS BRANCH ADDS NO WORD TO THE VOCABULARY. `timeout` is already 503 and
// already means "try again"; what was missing was the classification and the
// header. The block stays at twelve for this ruling — DEC-103 grows it, for a
// different reason, and that is a separate decision.
//
// THE THREE SHAPES ARE THE THREE DEC-96 NAMES, and each is reachable from the
// standard library, which is what keeps this classification out of pgconn's
// way: spec L20 has pgx as a blank import driver only, and internal/postgres'
// own comment records that reading SQLSTATE off the driver is not available
// here. Measured against a real pool pointed at a dead port: a pgconn connect
// error unwraps to *net.OpError and satisfies errors.As(err, &net.Error).
func TestADependencyThatIsDownIsATimeoutAndNotAnInternalError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a driver connect failure", fmt.Errorf("postgres: reading trips: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})},
		{"a connection database/sql has already closed", fmt.Errorf("postgres: %w", sql.ErrConnDone)},
		{"a pool acquire that ran out of time", fmt.Errorf("postgres: taking a connection: %w",
			context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := httpx.CodeFor(tc.err); got != httpx.CodeTimeout {
				t.Errorf("CodeFor(%v) = %q, want %q — a 500 tells a client the request "+
					"is poison and must not be retried, which is the opposite of "+
					"the truth", tc.err, got, httpx.CodeTimeout)
			}
			if got := httpx.StatusFor(httpx.CodeTimeout); got != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", got)
			}
		})
	}
}

// AND THE CONTROL, WITHOUT WHICH THE ABOVE IS SATISFIED BY A CLASSIFIER THAT
// CALLS EVERYTHING A TIMEOUT. A genuine handler fault is still `internal`, and
// 500 is still what a client is told not to retry.
func TestAnOrdinaryFailureIsStillInternal(t *testing.T) {
	for _, err := range []error{
		errors.New("a handler dereferenced nothing"),
		fmt.Errorf("postgres: scanning a trip: %w", errors.New("sql: Scan error")),
	} {
		if got := httpx.CodeFor(err); got != httpx.CodeInternal {
			t.Errorf("CodeFor(%v) = %q, want %q", err, got, httpx.CodeInternal)
		}
	}
}

// RETRY-AFTER IS A FUNCTION OF THE STATUS AND NOT OF THE CALLER, which is the
// whole of why it is set where it is. Two different mechanisms produce a 503
// here — this classification, and http.TimeoutHandler, which writes its own
// response and takes no part in any of this — and a header set at each call
// site would be set at one of them.
func TestEvery503CarriesRetryAfter(t *testing.T) {
	h := httpx.RetryAfter()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteErrorFor(w, r, fmt.Errorf("postgres: %w", sql.ErrConnDone))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want \"5\" — 'the dependency is down, try again "+
			"shortly' is the whole difference between this and a poison request", got)
	}
	if got := rec.Body.String(); got != `{"code":"timeout"}` {
		t.Errorf("body = %s", got)
	}
}

// AND IT IS NOT SET ON ANYTHING ELSE. A Retry-After on a 422 or a 404 is an
// instruction to retry a request that will fail identically for ever.
func TestRetryAfterIsNotSetOnAnswersThatWillNotChange(t *testing.T) {
	for _, code := range httpx.Codes() {
		if httpx.StatusFor(code) == http.StatusServiceUnavailable {
			continue
		}
		h := httpx.RetryAfter()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteError(w, r, code)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))
		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Errorf("%s (%d) carries Retry-After: %s — retrying will fail identically",
				code, rec.Code, got)
		}
	}
}

// pgLike is a driver error that carries a SQLSTATE the way *pgconn.PgError
// does. It is a stand-in rather than the real thing for the reason
// httpx.sqlStater is an interface rather than an import: this package must not
// name pgconn, and the whole claim is that it does not have to.
type pgLike struct{ state string }

func (e pgLike) Error() string    { return "ERROR: something (SQLSTATE " + e.state + ")" }
func (e pgLike) SQLState() string { return e.state }

// THE SECOND HALF OF THE CLASSIFICATION, AND IT IS THE HALF DEC-96'S OWN LIST
// DOES NOT NAME. Measured while writing the ruling's lock leg: with a lock held
// on `trips`, the blocked read is cut off by statement_timeout and comes back
// as SQLSTATE 57014 — not a net error, not a context deadline — so it landed
// in `default` and answered 500, and the ruling's own leg could not pass.
func TestAServerThatGaveUpOnTheStatementIsAlsoATimeout(t *testing.T) {
	for _, tc := range []struct{ state, what string }{
		{"57014", "statement_timeout, which is DEC-96's own second bound firing"},
		{"55P03", "lock_timeout, which is the third bound firing"},
		{"53300", "too many connections"},
		{"08006", "the connection failed mid-statement"},
		{"57P01", "the administrator shut the server down"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			err := fmt.Errorf("postgres: reading trips: %w", pgLike{tc.state})
			if got := httpx.CodeFor(err); got != httpx.CodeTimeout {
				t.Errorf("CodeFor(SQLSTATE %s) = %q, want %q — %s",
					tc.state, got, httpx.CodeTimeout, tc.what)
			}
		})
	}
}

// AND THE CLASSES THAT MUST STAY 500, WHICH IS WHERE THE LINE IS DRAWN.
//
// A constraint violation is the server having a bug and a client must not
// retry it. The two SERIALIZATION states are the interesting exclusions: both
// are retryable in principle and neither is a dependency being unavailable —
// they are this application's own concurrency, and this build takes one
// advisory lock per traveller precisely so it does not meet them. One in a log
// is a defect to fix, not an outage to wait out.
func TestAConstraintViolationAndADeadlockAreStillInternal(t *testing.T) {
	for _, tc := range []struct{ state, what string }{
		{"23503", "a foreign key violation — the server wrote something incoherent"},
		{"22012", "division by zero"},
		{"40001", "serialization_failure — this app's own concurrency, not an outage"},
		{"40P01", "deadlock_detected — a defect to fix, not something to wait out"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			err := fmt.Errorf("postgres: upserting the trip: %w", pgLike{tc.state})
			if got := httpx.CodeFor(err); got != httpx.CodeInternal {
				t.Errorf("CodeFor(SQLSTATE %s) = %q, want %q — %s",
					tc.state, got, httpx.CodeInternal, tc.what)
			}
		})
	}
}
