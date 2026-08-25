// The middleware chain, outermost first: recover, request id, access log,
// timeout — and then auth, which lands at VS6 and slots in below these four.
//
// THE ORDER IS THE DESIGN, and each position is load-bearing:
//
//   - RECOVER IS OUTERMOST so it catches a panic raised by any of the other
//     middlewares, not only by the handler. http.TimeoutHandler re-panics its
//     handler's panic on the serving goroutine, so a panic underneath the
//     timeout still arrives here.
//   - REQUEST ID IS ABOVE THE ACCESS LOG, or the access line has nothing to be
//     correlated by.
//   - THE ACCESS LOG IS ABOVE THE TIMEOUT, or a timed-out request is never
//     logged at all: TimeoutHandler returns while the handler is still
//     running, and a log line written below it belongs to a goroutine whose
//     response was discarded.
//   - AUTH IS INNERMOST OF THE FIVE, because a 401 is a request that happened
//     and should be logged, timed and recovered like any other.
//
// ONE MEASURED CONSEQUENCE OF RECOVER BEING OUTERMOST, recorded because it
// looks like a defect and is not: the access log's deferred line runs as the
// panic unwinds, which is BEFORE the outer recover has written anything, so
// that line records the status the HANDLER wrote — none, i.e. 0. The 500 is
// real and the client gets it; the two log lines are joined by the request id
// rather than by one line carrying both facts. Moving recover inside the
// access log would fix the number and lose everything recover is for.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler. The type exists so Chain's variadic argument
// reads as a list of middlewares rather than a list of functions.
type Middleware func(http.Handler) http.Handler

// RequestIDHeader is both what is read back in tests and what a client sees.
const RequestIDHeader = "X-Request-Id"

// Chain applies mw[0] OUTERMOST.
//
// The fold direction is the whole of this function and it is invisible by
// inspection: reverse the loop and the chain still compiles, still runs every
// middleware exactly once, and runs them inside out — recover innermost, where
// it catches nothing that happens above it, and the timeout outermost, where it
// cuts off the access log it is supposed to be inside.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Base is the four VS3 fixes plus DEC-96's Retry-After, in order. Auth appends
// to it:
//
//	httpx.Chain(mux, append(httpx.Base(log, d), auth.Require(store))...)
//
// RETRY-AFTER SITS ABOVE TIMEOUT AND THAT POSITION IS THE WHOLE OF IT.
// http.TimeoutHandler writes its own 503 from inside net/http, so a header set
// anywhere BELOW it never reaches the 503 a client is most likely to meet.
// Above it, one wrapper covers that response and every handler-written 503 as
// well — which is why the header is set here rather than at the call sites
// that produce the status.
func Base(log *slog.Logger, timeout time.Duration) []Middleware {
	return []Middleware{
		Recover(log),
		RequestID(),
		AccessLog(log),
		RetryAfter(),
		Timeout(timeout),
	}
}

// Recover turns a panic into a 500 carrying the envelope, and puts the panic
// value and a stack in the log where the detail is allowed to go.
//
// TWO THINGS IT REFUSES TO DO:
//
//   - It does not swallow http.ErrAbortHandler. That value is the stdlib's own
//     signal that a handler is abandoning the response deliberately; net/http
//     suppresses its log and closes the connection. Catching it would turn a
//     deliberate abort into a 500 with a body, on a connection the handler has
//     already given up on.
//   - It does not write over a response that has started. A handler that sent
//     200 and then panicked has already had its status go out; appending the
//     envelope to a half-written document produces something the client cannot
//     parse at all, and the second WriteHeader is a no-op with a warning.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w}

			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if err, isErr := p.(error); isErr && errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}

				log.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.String("panic", fmt.Sprint(p)),
					slog.String("requestId", requestIDForRecover(w, r)),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)

				if !sw.wroteHeader {
					WriteError(sw, r, CodeInternal)
				}
			}()

			next.ServeHTTP(sw, r)
		})
	}
}

// requestIDForRecover reads the header first and the context second.
//
// Recover is outermost, so the request it holds predates the id. The id is on
// the response header, which the request-id middleware has already set on the
// shared ResponseWriter — without this the one log line that matters most is
// the one line that cannot be correlated.
func requestIDForRecover(w http.ResponseWriter, r *http.Request) string {
	if id := w.Header().Get(RequestIDHeader); id != "" {
		return id
	}
	return RequestIDFrom(r.Context())
}

// RequestID mints an id, puts it on the response and in the context.
//
// AN INBOUND X-Request-Id IS NOT TRUSTED, and that is a decision rather than an
// omission. The id lands in every log line for the request, so adopting one a
// stranger chose hands anyone on the internet a way to forge an id, collide
// with somebody else's, or inject into the log. There is no proxy in front of
// this server whose header could be trusted instead — Caddy is deferred, and
// the question of trusting what it sets is deferred with it.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newRequestID()
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
		})
	}
}

// newRequestID is 16 random bytes as 32 hex characters.
//
// crypto/rand rather than math/rand, and the reason is not secrecy: it is that
// since Go 1.24 crypto/rand.Read never fails — it panics internally on an
// unrecoverable source failure — so there is no error branch here to get
// wrong, and no seeding to forget.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AccessLog writes one line per request: what was asked, what was answered,
// how long it took, and the id that joins it to everything else.
//
// THE QUERY STRING IS NOT LOGGED. DEC-10 and DEC-11's public share path is
// deferred, but its shape is settled — a capability lives in the URL — and a
// logger that records query strings records capabilities, in plain text,
// for as long as the logs are kept.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}

			defer func() {
				log.LogAttrs(r.Context(), accessLevel(sw.status), "request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", sw.status),
					slog.Int("bytes", sw.bytes),
					slog.Int64("durationMs", time.Since(start).Milliseconds()),
					slog.String("requestId", RequestIDFrom(r.Context())),
				)
			}()

			next.ServeHTTP(sw, r)
		})
	}
}

// accessLevel is the level one access line is written at.
//
// A status of 0 means the handler wrote nothing — a panic on its way up, or a
// timeout that discarded the response. Both are worth the same attention as a
// 500.
func accessLevel(status int) slog.Level {
	if status == 0 || status >= http.StatusInternalServerError {
		return slog.LevelError
	}
	return slog.LevelInfo
}

// Timeout is http.TimeoutHandler, constructed with the JSON envelope as its
// message — the one response in this application that DEC-12's AST sweep
// structurally cannot see, because the stdlib writes it and no call to
// WriteError is involved.
//
// AND IT DOES NOT SET Content-Type, WHICH IS MEASURED RATHER THAN ASSUMED.
// TimeoutHandler's timeout branch is exactly `w.WriteHeader(503)` followed by
// `io.WriteString(w, h.errorBody())`; it touches no header. With no
// Content-Type set, net/http SNIFFS the body, and `{"code":"timeout"}` sniffs
// as text/plain — so a client selecting on the header would refuse to parse
// the one error it is most likely to meet. jsonByDefault fills it in at
// WriteHeader time, which is late enough that a handler with its own type
// keeps it.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		timed := http.TimeoutHandler(next, d, bodyTimeout)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timed.ServeHTTP(&jsonByDefault{ResponseWriter: w}, r)
		})
	}
}

// statusWriter records what went out. Unwrap is what keeps
// http.ResponseController working through the wrapper — without it, a future
// handler that needs a flush or a deadline finds a ResponseWriter with none of
// the optional interfaces it expects.
type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

// A handler that writes a body without calling WriteHeader has sent 200, and a
// recorder that reported 0 for it would make every ordinary success look like
// a request that answered nothing.
func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// jsonByDefault fills in Content-Type at WriteHeader time if nothing has set
// one. See Timeout for why it exists.
type jsonByDefault struct{ http.ResponseWriter }

func (w *jsonByDefault) WriteHeader(status int) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", contentTypeJSON)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *jsonByDefault) Unwrap() http.ResponseWriter { return w.ResponseWriter }
