// An in-memory token bucket, one per client address.
package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// limiterPruneAbove is when a sweep of idle buckets happens: only once the
// map is bigger than this, so the ordinary case pays nothing.
var limiterPruneAbove = 1024

// Limiter hands out `perMinute` requests per key, refilling continuously.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	burst     float64
	perSecond float64
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter builds a limiter allowing perMinute requests per key.
func NewLimiter(perMinute int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		buckets:   map[string]*bucket{},
		burst:     float64(perMinute),
		perSecond: float64(perMinute) / 60,
		now:       now,
	}
}

// Allow spends one token for key, or reports that there is none to spend.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, held := l.buckets[key]
	if !held {
		if len(l.buckets) >= limiterPruneAbove {
			l.prune(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Len is how many buckets are held.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// prune drops every bucket that has refilled to full.
func (l *Limiter) prune(now time.Time) {
	for key, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.perSecond >= l.burst {
			delete(l.buckets, key)
		}
	}
}

// ClientKey is the address the limiter counts against.
func ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit refuses over-quota requests with the envelope, before the handler
// runs, counting against the client address.
func RateLimit(l *Limiter, log *slog.Logger) Middleware {
	return RateLimitBy(l, log, "client", func(r *http.Request) (string, bool) {
		return ClientKey(r), true
	})
}

// RateLimitBy is RateLimit with the key chosen by the caller.
func RateLimitBy(l *Limiter, log *slog.Logger, keyName string, key func(*http.Request) (string, bool)) Middleware {
	logger := func() *slog.Logger {
		if log != nil {
			return log
		}
		return slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k, held := key(r)
			if !held {
				logger().LogAttrs(r.Context(), slog.LevelError, "the rate limiter could not key this request",
					slog.String("key", keyName),
					slog.String("path", LoggedPath(r)),
					slog.String("requestId", RequestIDFrom(r.Context())),
				)
				WriteError(w, r, CodeInternal)
				return
			}
			if !l.Allow(k) {
				logger().LogAttrs(r.Context(), slog.LevelWarn, "rate limited",
					slog.String(keyName, k),
					slog.String("path", LoggedPath(r)),
					slog.String("requestId", RequestIDFrom(r.Context())),
				)
				WriteError(w, r, CodeRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
