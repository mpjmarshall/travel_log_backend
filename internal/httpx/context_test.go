package httpx_test

import (
	"context"
	"testing"

	"travellog/internal/httpx"
)

func TestRequestIDRoundTripsThroughTheContext(t *testing.T) {
	ctx := httpx.WithRequestID(context.Background(), "abc123")
	if got := httpx.RequestIDFrom(ctx); got != "abc123" {
		t.Errorf("RequestIDFrom = %q, want abc123", got)
	}
}

// Every logging call site reads the id unconditionally, so "no id" has to be a
// string rather than a panic — a log line is not worth a 500.
func TestRequestIDFromAContextThatHasNoneIsEmpty(t *testing.T) {
	if got := httpx.RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom(a bare context) = %q, want the empty string", got)
	}
}

// The key is an unexported type, so nothing outside this package can collide
// with it — including a string key with the same text, which is the collision
// the context documentation warns about and which no test would otherwise see.
func TestAStringKeyWithTheSameTextDoesNotCollide(t *testing.T) {
	ctx := context.WithValue(context.Background(), "requestId", "planted") //nolint:staticcheck // the point of the leg
	if got := httpx.RequestIDFrom(ctx); got != "" {
		t.Errorf("RequestIDFrom read a plain string key: %q", got)
	}
}
