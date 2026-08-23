// The wire's vocabulary: twelve words, closed, and no prose ever.
//
// DEC-12. The client's design system has no toast, no snackbar and no error
// colour; every failure surface is a fixed per-surface sentence the CLIENT
// owns. Prose the server sends is prose the server must translate, style and
// keep consistent with a design system it cannot see — so the body is
// `{"code":"…"}` and the vocabulary is CLOSED, or it drifts into prose by
// accretion.
//
// THE BLOCK SHIPS WHOLE EVEN THOUGH THE SLICE USES TEN. `upload_incomplete`
// belongs to media and `forbidden` to the share path, neither of which is in
// the slice — and they are here anyway, because v5 left an ELEVEN-code block
// against a twelve-code decision and the sweep below is one-directional: it
// asserts every word on the wire is IN the block, so a missing word is not
// caught until the step that needs it, at which point the sweep reddens
// against correct code and somebody "fixes" it by growing the block.
//
// THREE MECHANISMS KEEP IT CLOSED, AND EACH CLOSES A HOLE THE OTHERS CANNOT:
//
//   - The AST sweep (sweep_test.go) rejects any argument to WriteError that is
//     not a named constant from this block. TYPING ALONE DOES NOT DO THIS, and
//     that is measured rather than assumed: `Code` is a defined string type, so
//     an UNTYPED string constant converts implicitly and `WriteError(w, r,
//     "banana")` compiles. See TestAStringLiteralArgumentIsRejected.
//   - StatusFor's map is the single runtime source of membership, so an
//     unknown word answers 500 and WriteError substitutes `internal` rather
//     than echoing it. That covers the runtime path an AST walk cannot reach.
//   - CodeFor validates what a domain error asks for (DEC-62). The sentinel is
//     the domain's word and the code is the wire's; a domain that names a word
//     outside this block gets `internal`, not its invention.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
)

// Code is one word of the wire vocabulary. It is a defined type for
// readability at the call site, NOT as the closure mechanism — see the file
// comment for why the type cannot be the mechanism.
type Code string

const (
	CodeUnauthenticated   Code = "unauthenticated"
	CodeForbidden         Code = "forbidden"
	CodeNotFound          Code = "not_found"
	CodeConflict          Code = "conflict"
	CodeInvalidBody       Code = "invalid_body"
	CodeInvalidField      Code = "invalid_field"
	CodeRateLimited       Code = "rate_limited"
	CodePayloadTooLarge   Code = "payload_too_large"
	CodeTimeout           Code = "timeout"
	CodeInternal          Code = "internal"
	CodeUploadIncomplete  Code = "upload_incomplete"
	CodeUnsupportedFormat Code = "unsupported_format"
)

// statusByCode is the ONE runtime list. Codes() derives from it rather than
// standing beside it as a second literal, because two lists of twelve is how a
// vocabulary reaches two disagreeing states — which is the defect DEC-12
// records against its own earlier revisions.
//
// Two rows are decisions rather than obvious mappings:
//
//   - `timeout` is 503 and not 504, because http.TimeoutHandler writes
//     StatusServiceUnavailable ITSELF and the handler does not get to choose.
//     A table saying 504 would disagree with the one response in this app that
//     the table does not produce.
//   - `upload_incomplete` is 409 and not 422. The referenced object EXISTS and
//     the request is well-formed; what is wrong is the object's state, which
//     is a conflict. It is grouped with 409 rather than with the field
//     validation that produces 422. The media step may overturn this — it owns
//     the flow — and if it does, this comment is where the reason goes.
var statusByCode = map[Code]int{
	CodeUnauthenticated:   http.StatusUnauthorized,
	CodeForbidden:         http.StatusForbidden,
	CodeNotFound:          http.StatusNotFound,
	CodeConflict:          http.StatusConflict,
	CodeInvalidBody:       http.StatusBadRequest,
	CodeInvalidField:      http.StatusUnprocessableEntity,
	CodeRateLimited:       http.StatusTooManyRequests,
	CodePayloadTooLarge:   http.StatusRequestEntityTooLarge,
	CodeTimeout:           http.StatusServiceUnavailable,
	CodeInternal:          http.StatusInternalServerError,
	CodeUploadIncomplete:  http.StatusConflict,
	CodeUnsupportedFormat: http.StatusNotAcceptable,
}

// Coder is how a domain error names its own wire word (DEC-62). The domain
// package declares its sentinels and their codes; httpx never learns what a
// trip or a traveller is. One mapping function, and it is CodeFor.
type Coder interface{ ErrorCode() Code }

// Codes returns the vocabulary, sorted. A fresh slice each call: a caller
// holding the package's own backing array could reorder the vocabulary.
func Codes() []Code {
	out := make([]Code, 0, len(statusByCode))
	for c := range statusByCode {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// StatusFor answers the HTTP status for a code, and 500 for a word that is not
// in the vocabulary. Never a 2xx: every code in this block is a failure.
func StatusFor(c Code) int {
	if status, ok := statusByCode[c]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func (c Code) known() bool {
	_, ok := statusByCode[c]
	return ok
}

// CodeFor is DEC-62's one mapping function: it turns an error from anywhere
// below the handler into a word from the block above it.
//
// ORDER IS A DECISION. A domain error that names its own code wins over a
// transport sentinel, because the domain is the more specific speaker — and a
// domain error WRAPPING a transport one (a store that wrapped a decode
// failure) means the domain has already decided what it is.
//
// AND THE DEFAULT IS `internal`, INCLUDING FOR nil. A nil error reaching an
// error-writing path is a bug at the call site, and answering 200 to it would
// mean a handler silently reported success for work it did not do.
func CodeFor(err error) Code {
	if err == nil {
		return CodeInternal
	}

	var coder Coder
	if errors.As(err, &coder) {
		if c := coder.ErrorCode(); c.known() {
			return c
		}
		return CodeInternal
	}

	switch {
	case errors.Is(err, ErrBodyTooLarge):
		return CodePayloadTooLarge
	case errors.Is(err, ErrInvalidBody):
		return CodeInvalidBody
	}
	return CodeInternal
}

// errorPayload is the whole of an error body. `field` is DEC-12's ONE
// permitted additive key and it is omitempty, because optional means absent —
// `"field":""` would have the client render a blank name into a fixed
// sentence.
type errorPayload struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
}

// WriteError writes `{"code":"…"}` at the status the vocabulary assigns.
//
// An unknown word is replaced rather than echoed. That is the runtime half of
// the closure: the AST sweep catches a literal at review time, and this catches
// a `Code(someString)` conversion at run time, which no AST walk can see.
func WriteError(w http.ResponseWriter, r *http.Request, c Code) {
	if !c.known() {
		slog.ErrorContext(r.Context(), "httpx: a code outside the vocabulary reached the wire",
			slog.String("code", string(c)),
			slog.String("requestId", RequestIDFrom(r.Context())),
		)
		c = CodeInternal
	}
	WriteJSON(w, r, StatusFor(c), errorPayload{Code: c})
}

// WriteFieldError is the one call that adds `field`.
func WriteFieldError(w http.ResponseWriter, r *http.Request, field string) {
	WriteJSON(w, r, StatusFor(CodeInvalidField), errorPayload{
		Code:  CodeInvalidField,
		Field: field,
	})
}

// WriteErrorFor maps an error to its word and writes it — and puts the error
// itself in the log with the request id, which is the only place detail is
// allowed to go. DEC-12: "Real detail goes to slog with the request id, never
// to the body."
func WriteErrorFor(w http.ResponseWriter, r *http.Request, err error) {
	c := CodeFor(err)
	if err != nil {
		slog.ErrorContext(r.Context(), "request failed",
			slog.String("code", string(c)),
			slog.String("requestId", RequestIDFrom(r.Context())),
			slog.String("err", err.Error()),
		)
	}
	WriteError(w, r, c)
}

// The two bodies that cannot come from the encoder, and why.
//
// http.TimeoutHandler takes its body as a STRING at construction time, before
// any request exists — and the JSON encoder is confined to two functions
// (spec L19), so a third helper marshalling this is not available either. So
// they are literals. What keeps them honest is not care: it is
// TestThePrebuiltBodiesEqualWhatTheEncoderProduces, which asserts each is
// byte-identical to what WriteJSON writes for the same payload.
const (
	bodyTimeout  = `{"code":"timeout"}`
	bodyInternal = `{"code":"internal"}`
)

func prebuiltBody(c Code) string {
	switch c {
	case CodeTimeout:
		return bodyTimeout
	case CodeInternal:
		return bodyInternal
	}
	return ""
}
