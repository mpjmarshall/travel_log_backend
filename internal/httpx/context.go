// Request-scoped values, carried the way go_backend.md L22 asks.
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

// contentTypeJSON is the one spelling.
const contentTypeJSON = "application/json"

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom answers the empty string when there is no id, rather than
// panicking.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestFacts is's two things the access line learns AFTER it has already
// been entered.
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
// the access line can carry it.
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
func RecordRoute(ctx context.Context, pattern string) {
	facts := factsFrom(ctx)
	if facts == nil || pattern == "" {
		return
	}
	facts.mu.Lock()
	defer facts.mu.Unlock()
	facts.route = pattern
}
