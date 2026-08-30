// The middleware chain, outermost first: recover, request id, access log,
// timeout — and then auth, which slots in below these four.
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

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// RequestIDHeader is both what is read back in tests and what a client sees.
const RequestIDHeader = "X-Request-Id"

// Chain applies mw[0] OUTERMOST.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Base is the four, plus the Retry-After, in order.
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
					slog.String("path", LoggedPath(r)),
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
func requestIDForRecover(w http.ResponseWriter, r *http.Request) string {
	if id := w.Header().Get(RequestIDHeader); id != "" {
		return id
	}
	return RequestIDFrom(r.Context())
}

// RequestID mints an id, puts it on the response and in the context.
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
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AccessLog writes one line per request: what was asked, what was answered,
// how long it took, and the id that joins it to everything else.
func AccessLog(log *slog.Logger, quiet ...string) Middleware {
	demote := make(map[string]bool, len(quiet))
	for _, path := range quiet {
		demote[path] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			ctx, facts := withFacts(r.Context())
			r = r.WithContext(ctx)

			defer func() {
				travellerID, route := facts.read()
				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("path", LoggedPath(r)),
					slog.Int("status", sw.status),
					slog.Int("bytes", sw.bytes),
					slog.Int64("durationUs", time.Since(start).Microseconds()),
					slog.String("requestId", RequestIDFrom(r.Context())),
				}
				if travellerID != "" {
					attrs = append(attrs, slog.String("travellerId", travellerID))
				}
				if route != "" {
					attrs = append(attrs, slog.String("route", route))
				}
				log.LogAttrs(r.Context(), accessLevel(sw.status, demote[r.URL.Path]), "request", attrs...)
			}()

			next.ServeHTTP(sw, r)
		})
	}
}

// accessLevel is the level one access line is written at.
func accessLevel(status int, quiet bool) slog.Level {
	if status == 0 || status >= http.StatusInternalServerError {
		return slog.LevelError
	}
	if quiet {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// Timeout is http.TimeoutHandler, constructed with the JSON envelope as its
// message.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		timed := http.TimeoutHandler(next, d, bodyTimeout)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timed.ServeHTTP(&jsonByDefault{ResponseWriter: w}, r)
		})
	}
}

// statusWriter records what went out.
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

// A handler that writes a body without calling WriteHeader has sent 200.
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
// one.
type jsonByDefault struct{ http.ResponseWriter }

func (w *jsonByDefault) WriteHeader(status int) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", contentTypeJSON)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *jsonByDefault) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// CapabilityHeaders is the response policy for a body that carries a bearer
// capability.
func CapabilityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store, private")
			w.Header().Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}
