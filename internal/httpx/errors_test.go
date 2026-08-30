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

// decCodes is the vocabulary, written out here as the decision's list —
// deliberately a second copy.
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
	"unsupported_route",
	"upload_incomplete",
}

func TestTheVocabularyIsExactlyTheCodesDEC12Names(t *testing.T) {
	got := make([]string, 0, len(httpx.Codes()))
	for _, c := range httpx.Codes() {
		got = append(got, string(c))
	}
	sort.Strings(got)

	if len(got) != len(decCodes) {
		t.Fatalf("the vocabulary holds %d codes, want %d: %v", len(got), len(decCodes), got)
	}
	for i := range decCodes {
		if got[i] != decCodes[i] {
			t.Fatalf("vocabulary = %v, want %v", got, decCodes)
		}
	}
}

// Totality in both directions.
func TestEveryCodeHasAStatusAndNoStatusIsOrphaned(t *testing.T) {
	for _, c := range httpx.Codes() {
		if got := httpx.StatusFor(c); got < 400 || got > 599 {
			t.Errorf("StatusFor(%q) = %d, want a 4xx or 5xx", c, got)
		}
	}
	if got := len(httpx.Codes()); got != len(decCodes) {
		t.Errorf("Codes() = %d entries, want %d", got, len(decCodes))
	}
}

func TestTheStatusOfEachCodeIsTheOneItsDecisionNames(t *testing.T) {
	want := map[httpx.Code]int{
		httpx.CodeUnauthenticated:   http.StatusUnauthorized,
		httpx.CodeForbidden:         http.StatusForbidden,
		httpx.CodeNotFound:          http.StatusNotFound,
		httpx.CodeUnsupportedRoute:  http.StatusNotFound,
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
	if len(want) != len(decCodes) {
		t.Fatalf("this table holds %d rows, want %d — every word gets a row, or a "+
			"code added without one is a 500 with nobody noticing",
			len(want), len(decCodes))
	}
	for c, status := range want {
		if got := httpx.StatusFor(c); got != status {
			t.Errorf("StatusFor(%q) = %d, want %d", c, got, status)
		}
	}
}

// TestTimeoutIsWhatTheStdlibItselfWrites is the reason CodeTimeout is 503 and
// not 504.
func TestTimeoutIsWhatTheStdlibItselfWrites(t *testing.T) {
	if got := httpx.StatusFor(httpx.CodeTimeout); got != http.StatusServiceUnavailable {
		t.Errorf("StatusFor(timeout) = %d, want %d — what http.TimeoutHandler writes",
			got, http.StatusServiceUnavailable)
	}
}

// : "no prose, ever".
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

// The `field` key is the one optional additive key, and optional means absent
// Than empty.
func TestFieldIsOmittedRatherThanEmptyOnEveryOtherCode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)

	httpx.WriteError(rec, req, httpx.CodeInvalidField)

	if got := rec.Body.String(); got != `{"code":"invalid_field"}` {
		t.Errorf("body = %s, want no field key at all", got)
	}
}

// A Code reached by conversion — httpx.Code("banana") — is the one-word
// bypass the ast sweep is built to catch at review time.
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

// A 500 tells A client the server has A bug — do not retry, the request is
// poison — when the truth is "the dependency is down, try again shortly".
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

// The control, without which the above is satisfied by A classifier that
// calls everything A timeout.
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

// retry-after is a function of the status and not of the caller, which is the
// whole of why it is set where it is.
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

// It is not set on anything else.
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
// does.
type pgLike struct{ state string }

func (e pgLike) Error() string    { return "ERROR: something (SQLSTATE " + e.state + ")" }
func (e pgLike) SQLState() string { return e.state }

// The second half of the classification, and the half a SQLSTATE list does
// not name.
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

// The classes that must stay 500, which is where the line is drawn.
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
