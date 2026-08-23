package httpx_test

import (
	"encoding/json"
	"errors"
	"fmt"
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
