// Request-scoped values, carried the way go_backend.md L22 asks: "Use the
// standard `context` package to handle request timeouts and pass request-scoped
// data."
//
// There is exactly one value here today — the request id — and it has its own
// unexported key type. A `string` key would be reachable, and collidable, by
// any package that happened to use the same text; the compiler cannot see that
// collision and neither can a reviewer.
package httpx

import (
	"context"
	"sync"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	factsKey
)

// contentTypeJSON is the one spelling. Written once because two spellings —
// with and without `; charset=utf-8` — is how a client's exact-match check
// starts failing on one route out of five.
const contentTypeJSON = "application/json"

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom answers the empty string when there is no id, rather than
// panicking: every log call site reads it unconditionally, and a log line is
// not worth a 500.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestFacts is the two things the access line learns AFTER it has already
// been entered, and it is a mutable slot rather than a context value for a
// reason that is easy to get wrong.
//
// THE ACCESS LOG IS ABOVE AUTH AND ABOVE THE MUX. Its deferred line runs
// against the request IT was handed; auth resolves the traveller and calls
// `r.WithContext`, and `http.ServeMux` sets `r.Pattern` on a clone — both of
// which produce a NEW request that the access log never sees. A plain
// `context.WithValue` written below therefore cannot travel back up.
//
// So the access log installs an empty slot on the way IN and reads it on the
// way OUT, and the middlewares below fill it. The context value is a POINTER,
// which is the whole mechanism: the pointer travels down every clone, and what
// it points at is shared.
//
// THE MUTEX IS NOT DECORATION. http.TimeoutHandler runs the handler on a
// SEPARATE GOROUTINE and returns without it, so the deferred read can race a
// write from a handler that is still running — which is not a theoretical
// window, it is exactly what a timed-out request does.
type requestFacts struct {
	mu          sync.Mutex
	travellerID string
	route       string
}

func (f *requestFacts) read() (travellerID, route string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.travellerID, f.route
}

func withFacts(ctx context.Context) (context.Context, *requestFacts) {
	facts := &requestFacts{}
	return context.WithValue(ctx, factsKey, facts), facts
}

func factsFrom(ctx context.Context) *requestFacts {
	facts, _ := ctx.Value(factsKey).(*requestFacts)
	return facts
}

// RecordTraveller names the traveller this request turned out to be for, so
// the access line can carry it (DEC-101).
//
// IT IS A NO-OP WHEN NOTHING IS LISTENING, deliberately: every caller is a
// middleware that must work whether or not an access log is in the chain
// above it, and a panic here would turn a logging gap into a 500.
func RecordTraveller(ctx context.Context, id string) {
	facts := factsFrom(ctx)
	if facts == nil || id == "" {
		return
	}
	facts.mu.Lock()
	defer facts.mu.Unlock()
	facts.travellerID = id
}

// RecordRoute names the route PATTERN that matched, beside the raw path.
//
// IT IS RECORDED RATHER THAN DERIVED. `http.ServeMux` fills `r.Pattern` on the
// request it passes DOWNWARDS, so the outer request the access log holds never
// has it. httpapi.Mount records it from the route table instead — the same
// string, from the file that is already the authority for it.
func RecordRoute(ctx context.Context, pattern string) {
	facts := factsFrom(ctx)
	if facts == nil || pattern == "" {
		return
	}
	facts.mu.Lock()
	defer facts.mu.Unlock()
	facts.route = pattern
}
