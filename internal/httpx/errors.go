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
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
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
	case DependencyIsDown(err):
		return CodeTimeout
	}
	return CodeInternal
}

// DependencyIsDown is DEC-96's classification: a request that could not reach
// the database has not encountered a handler bug.
//
// WHY IT MATTERS MORE THAN IT LOOKS. With Postgres killed, every route answered
// `500 {"code":"internal"}` — and a 500 tells a client the server has a bug and
// the request is poison, do not retry, when the truth is "the dependency is
// down, try again shortly". It is also unanswerable afterwards: a 500 count
// then conflates handler bugs with outages, which is the one question an
// operator asks at 3am.
//
// IT ADDS NO WORD TO THE VOCABULARY. `timeout` is already 503 and already
// means "try again"; what was missing was the classification and the header.
//
// EVERY SHAPE IT RECOGNISES IS IN THE STANDARD LIBRARY, and that is a
// constraint rather than a preference. spec L20 has pgx as a blank import
// driver only, and internal/postgres' own comment records that reading SQLSTATE
// off the driver is not available here — so a classification that needed
// pgconn could not live anywhere. DEC-96's three shapes happen to be exactly
// the three stdlib can see, MEASURED against a real pool pointed at a dead
// port: `*pgconn.ConnectError` satisfies `errors.As(err, &net.Error)`, because
// it wraps the `*net.OpError` from the dial.
//
// WHAT IT DELIBERATELY DOES NOT COVER: a `statement_timeout` cancellation,
// which comes back as `*pgconn.PgError` SQLSTATE 57014 and does NOT unwrap to
// a net error — measured in the same probe. That request is bounded by
// httpx.Timeout instead, which answers 503 through a different mechanism and
// picks up the same Retry-After from RetryAfter's writer. Two paths, one
// answer, and neither needs pgconn.
func DependencyIsDown(err error) bool {
	var netErr net.Error
	switch {
	case errors.As(err, &netErr):
		return true
	case errors.Is(err, sql.ErrConnDone):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return true
	}
	return serverGaveUp(err)
}

// sqlStater is a driver error that carries a SQLSTATE. It is a STRUCTURAL
// interface and not an import, which is the whole reason this works here.
//
// spec L20 has pgx as a blank import driver only, and internal/postgres' own
// comment records the consequence: "reading a violation back off the driver
// would mean importing pgconn to read SQLSTATE 23503, which cmd/api's import
// sweep forbids". `*pgconn.PgError` has a `SQLState() string` method, so an
// interface declared here matches it without naming it — the same idiom
// DEC-62's `Coder` uses in the other direction. Measured against a real
// server: a statement cut off by statement_timeout satisfies
// `errors.As(err, &sqlStater)` and answers "57014".
type sqlStater interface{ SQLState() string }

// serverGaveUp is the second half of DEC-96's classification, and it is the
// half the ruling's own list does not name.
//
// DEC-96 lists "pgconn connect errors, sql.ErrConnDone, a pool-acquire
// deadline" — all three of which the standard library can see. MEASURED while
// writing the leg the ruling asks for: with a lock held on `trips`, the
// blocked read is cut off by `statement_timeout` and comes back as SQLSTATE
// 57014, which is NOT a net error and NOT a context deadline, so it landed in
// `default` and answered 500. The ruling's own leg — "one leg holds a lock and
// asserts the request gets a BOUNDED 503 rather than silence" — could not pass
// without this.
//
// FOUR CLASSES, AND WHAT IS LEFT OUT IS THE POINT:
//
//	08  connection_exception       — the connection failed mid-statement
//	53  insufficient_resources     — out of connections, memory or disk
//	57  operator_intervention      — 57014 is statement_timeout and a client
//	                                 cancellation; 57P01/02/03 are shutdown
//	55P03 lock_not_available       — lock_timeout, the third bound's own error
//
// NOT 40001 (serialization_failure) and NOT 40P01 (deadlock_detected). Both
// are retryable in principle and NEITHER is a dependency being unavailable:
// they are this application's own concurrency, and answering 503 to them would
// tell the client to retry work the server should be retrying or preventing.
// This build has no retry loop and takes one advisory lock per traveller
// precisely so it does not meet them; if one ever appears in a log it is a
// defect to fix rather than an outage to wait out.
//
// AND NOT ANY OTHER CLASS. 22012 (division by zero), 23503 (foreign key) and
// every constraint violation stay `internal`, because they are the server
// having a bug and a client must not retry them.
func serverGaveUp(err error) bool {
	var stater sqlStater
	if !errors.As(err, &stater) {
		return false
	}
	state := stater.SQLState()
	if len(state) < 2 {
		return false
	}
	switch state[:2] {
	case "08", "53", "57":
		return true
	}
	return state == "55P03"
}

// retryAfterSeconds is what a 503 tells the client to wait.
//
// FIVE, AND IT IS A GUESS RATHER THAN A MEASUREMENT — said so here because
// every other number in this repository is derived. What it is derived
// FROM is the shape of the two outages it covers: a restarting Postgres
// container is healthy again in single-digit seconds (compose's healthcheck
// interval is 3s with 20 retries), and a statement cut off by a lock queue
// clears when the migration ahead of it commits. Five is long enough that a
// phone retrying does not add to the queue and short enough that a user who
// pressed a button does not conclude the app is broken.
const retryAfterSeconds = "5"

// RetryAfter puts `Retry-After` on every 503 that leaves this server.
//
// IT IS A MIDDLEWARE AND NOT A LINE IN WriteError, and that is the decision.
// TWO DIFFERENT MECHANISMS PRODUCE A 503 HERE and only one of them goes
// through this package's writers: `http.TimeoutHandler` writes its own status
// and body from inside net/http and takes no part in the error path at all.
// A header set at the call sites would be set at one of the two — which is
// exactly the class of miss DEC-96 is correcting, since the timeout branch is
// the 503 a client is MOST likely to meet.
//
// It decides at WriteHeader time, for the same reason jsonByDefault does: the
// status is not known before then, and it is too late after.
func RetryAfter() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(&retryAfterWriter{ResponseWriter: w}, r)
		})
	}
}

type retryAfterWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *retryAfterWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		// A handler that set its own Retry-After knows something this does
		// not — a rate limiter with a real window, say — so it is not
		// overwritten.
		if status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", retryAfterSeconds)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *retryAfterWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *retryAfterWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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

// The three bodies that cannot come from the encoder, and why.
//
// http.TimeoutHandler takes its body as a STRING at construction time, before
// any request exists; WriteJSON's own encoder-failure branch cannot call the
// encoder that has just failed; and mux.go writes its body from INSIDE
// WriteHeader, where a marshal would be a second chance to fail with the
// status already sent. The JSON encoder is confined to two functions
// (spec L19), so a third helper marshalling any of them is not available
// either. So they are literals. What keeps them honest is not care: it is
// TestThePrebuiltBodiesEqualWhatTheEncoderProduces, which asserts each is
// byte-identical to what WriteJSON writes for the same payload.
const (
	bodyTimeout  = `{"code":"timeout"}`
	bodyInternal = `{"code":"internal"}`
	bodyNotFound = `{"code":"not_found"}`
)

func prebuiltBody(c Code) string {
	switch c {
	case CodeTimeout:
		return bodyTimeout
	case CodeInternal:
		return bodyInternal
	case CodeNotFound:
		return bodyNotFound
	}
	return ""
}
