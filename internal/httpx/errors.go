// Package httpx writes error responses: a closed vocabulary of codes, never
// prose. Detail goes to the log, never to the client.
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

// Code is one word of the error vocabulary. The type aids readability; the
// ast sweep in sweep_test.go is what keeps the vocabulary closed.
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

	CodeUnsupportedRoute Code = "unsupported_route"
)

// The only runtime membership list. timeout is 503 because TimeoutHandler
// writes that itself; upload_incomplete is 409 because the object exists.
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
	CodeUnsupportedRoute:  http.StatusNotFound,
}

// Coder lets a domain error name its own wire word.
type Coder interface{ ErrorCode() Code }

// Codes returns the vocabulary, sorted, as a fresh slice.
func Codes() []Code {
	out := make([]Code, 0, len(statusByCode))
	for c := range statusByCode {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// StatusFor answers the status for a code, and 500 for an unrecognised one.
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

// CodeFor turns any error into a word from the vocabulary. A domain error
// naming its own code wins; anything unrecognised, nil included, is internal.
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

// DependencyIsDown reports whether the database was unreachable, so the reply
// is 503 rather than 500. Only shapes the standard library can see: pgconn is banned here.
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

// sqlStater matches any driver error carrying a SQLSTATE, structurally,
// Pgconn may not be imported here.
type sqlStater interface{ SQLState() string }

// serverGaveUp reports whether a SQLSTATE class means the server stopped
// serving. Deadlock and serialization are excluded: they are our own concurrency.
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

// What a 503 tells the client to wait. Not measured.
const retryAfterSeconds = "5"

// RetryAfter sets Retry-After on every 503. Middleware because TimeoutHandler
// writes its own 503 from inside net/http, bypassing this package's writers.
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

// The whole of an error body. field is omitempty so a blank one never renders.
type errorPayload struct {
	Code  Code   `json:"code"`
	Field string `json:"field,omitempty"`
}

// WriteError writes the code at its status. An unrecognised word is replaced
// with internal, catching a Code(someString) the ast sweep cannot see.
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

// WriteFieldError is the one call that adds field.
func WriteFieldError(w http.ResponseWriter, r *http.Request, field string) {
	WriteJSON(w, r, StatusFor(CodeInvalidField), errorPayload{
		Code:  CodeInvalidField,
		Field: field,
	})
}

// WriteErrorFor maps an error to its word, writes it, and logs the error with
// the request id.
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

// Bodies the encoder cannot produce: TimeoutHandler needs a string up front,
// Mux.go writes from inside WriteHeader. A test keeps them identical.
const (
	bodyTimeout          = `{"code":"timeout"}`
	bodyInternal         = `{"code":"internal"}`
	bodyUnsupportedRoute = `{"code":"unsupported_route"}`
)

func prebuiltBody(c Code) string {
	switch c {
	case CodeTimeout:
		return bodyTimeout
	case CodeInternal:
		return bodyInternal
	case CodeUnsupportedRoute:
		return bodyUnsupportedRoute
	}
	return ""
}
