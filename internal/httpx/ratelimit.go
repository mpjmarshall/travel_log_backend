// An in-memory token bucket, one per client address (DEC-48).
//
// WHAT IT GUARDS. The plan states the 64 MiB-per-login Argon2 cost three times
// and then left `POST /v1/auth/register` and `POST /v1/auth/session` with no
// limit at all. Thirty-two concurrent unauthenticated POSTs is 2 GiB of Argon2
// memory on the box whose connection pool was sized so carefully. The limiter
// is one half of the answer; the concurrency semaphore, which caps how many
// argon2.IDKey calls run at once, is the other and belongs to the auth step.
//
// IT REJECTS RATHER THAN QUEUES. Making the caller wait converts memory
// exhaustion into timeout exhaustion, which is the same outage wearing a
// different error.
//
// IT KEYS ON RemoteAddr, AND THAT IS A DEFERRAL RATHER THAN A SIMPLIFICATION.
// Caddy is not in the slice, so there is no proxy in front of this server and
// RemoteAddr is the real peer. The moment a proxy appears, every request
// arrives from the proxy's address and this becomes one bucket for the whole
// internet — so `X-Forwarded-For` resolution, and the leg that proves it (two
// different forwarded values, one RemoteAddr, separate buckets), belong to the
// step that adds Caddy. Recorded in CLAUDE.md's "Inherited unfinished".
package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// limiterPruneAbove is when a sweep of idle buckets happens: only once the map
// is bigger than this, so the ordinary case pays nothing.
//
// A package-level var rather than a const so the pruning leg can lower it —
// an internal test, because unbounded map growth is the one failure of this
// type that no external behaviour reveals until the process is killed.
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
//
// `now` may be nil, meaning time.Now. It is a parameter because a limiter
// tested against the wall clock is a test that either sleeps for a minute or
// asserts nothing about refill — and refill is where a token bucket is wrong.
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
//
// THE `min` CEILING ON THE REFILL IS THE WHOLE OF IT. Without it an idle hour
// becomes an hour's worth of burst, and a quiet attacker lands 3,600 logins in
// one second having sent nothing for an hour.
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

// Len is how many buckets are held. Exported because unbounded growth is the
// failure this type can have that nothing else can see: an attacker with a
// large address space adds a map entry per address and never returns.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// prune drops every bucket that has refilled to full.
//
// THE INVARIANT THAT MAKES THIS SAFE: a full bucket and an absent bucket are
// the same thing — Allow creates an absent one full. So dropping full buckets
// changes no answer this limiter will ever give, which is why the sweep needs
// no lifetime, no TTL and no tuning.
func (l *Limiter) prune(now time.Time) {
	for key, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.perSecond >= l.burst {
			delete(l.buckets, key)
		}
	}
}

// ClientKey is the address the limiter counts against.
//
// THE PORT IS STRIPPED, and that is the whole function. RemoteAddr is
// `host:port` and the port is a new ephemeral number on every connection — so
// keying on the raw string gives each request its own bucket, and a limiter
// that passes every unit test limits nothing whatever in production.
//
// An address with no port to split falls back to the whole string rather than
// to an empty key, which would put every such client in one shared bucket.
func ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit refuses over-quota requests with the envelope, before the handler
// runs, counting against the client address.
//
// The client address goes to the log and never to the body: the body is the
// code alone, and an address is the one detail an operator actually needs here.
func RateLimit(l *Limiter, log *slog.Logger) Middleware {
	return RateLimitBy(l, log, "client", func(r *http.Request) (string, bool) {
		return ClientKey(r), true
	})
}

// RateLimitBy is RateLimit with the key chosen by the caller.
//
// WHY THE KEY IS A PARAMETER RATHER THAN ALWAYS THE ADDRESS. The address is the
// right key for an unauthenticated credential attempt, which is all this
// package had a caller for until the authenticated routes got a ceiling of
// their own. It is the wrong key for a request that carries an identity: a
// stolen token used from a thousand addresses is a thousand buckets and no
// limit at all, and every traveller behind one NAT is one bucket. What the
// identity IS cannot be decided here — this package imports no domain and has
// never heard of a traveller — so the caller supplies the function.
//
// `keyName` is what the refusal is logged under, and it is not decoration: an
// address that ran out and an identity that ran out are different events with
// different responses, and a log that spells them the same way makes an
// operator read the value to find out which one they are looking at.
//
// A REQUEST THE FUNCTION CANNOT KEY IS REFUSED WITH A 500, NOT WAVED THROUGH
// AND NOT PUT IN A SHARED BUCKET. The only way it fails is a middleware mounted
// where the fact it reads is not on the request yet, which is a wiring defect
// rather than a client fault. Failing open would remove the ceiling in exactly
// the case nobody notices — the app works and the guard is not there — and the
// empty-string bucket would be one allowance shared by everybody, which is a
// denial of service handed out for free. This is the same ruling DEC-48 already
// made for a nil limiter: a misconfiguration must not read as "no limit".
func RateLimitBy(l *Limiter, log *slog.Logger, keyName string, key func(*http.Request) (string, bool)) Middleware {
	// A NIL LOGGER MUST NOT TURN A RATE-LIMITED REQUEST INTO A 500, and before
	// this it did: both branches below call log.LogAttrs, and the refusal path
	// is the one that runs when the system is already under pressure. So the
	// symptom would have been the API answering 500 instead of 429 at exactly
	// the moment a limiter was doing its job.
	//
	// The fallback rather than a panic, because this is not the wiring seam:
	// httpapi.Mount panics on a nil logger the way it panics on a nil limiter,
	// which is where a misconfiguration should be caught. This is the second
	// line, for any caller that is not Mount — and it is the shape logFailure,
	// public_handlers and the migrator all already use.
	//
	// RESOLVED AT USE AND NOT HERE. Capturing slog.Default() when the
	// middleware is built freezes whatever was installed at wiring time, so a
	// later slog.SetDefault — a test installing a capturing handler, or
	// logging configured after the mux — would be followed by logFailure and
	// the migrator and ignored by this one. The three siblings all resolve per
	// call; this now does too.
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
