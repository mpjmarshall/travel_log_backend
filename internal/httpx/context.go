// Request-scoped values, carried the way go_backend.md L22 asks: "Use the
// standard `context` package to handle request timeouts and pass request-scoped
// data."
//
// There is exactly one value here today — the request id — and it has its own
// unexported key type. A `string` key would be reachable, and collidable, by
// any package that happened to use the same text; the compiler cannot see that
// collision and neither can a reviewer.
package httpx

import "context"

type contextKey int

const requestIDKey contextKey = iota

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
