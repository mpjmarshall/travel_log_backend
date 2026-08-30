package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"travellog/internal/httpx"
)

// clock is the injected time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)} }

func TestTheAllowanceIsSpentAndThenRefused(t *testing.T) {
	c := newClock()
	l := httpx.NewLimiter(5, c.now)

	for i := range 5 {
		if !l.Allow("203.0.113.9") {
			t.Fatalf("request %d of the allowance was refused", i+1)
		}
	}
	if l.Allow("203.0.113.9") {
		t.Error("the 6th request of a 5-per-minute allowance was allowed")
	}
}

func TestOneKeyRunningOutDoesNotRefuseAnother(t *testing.T) {
	c := newClock()
	l := httpx.NewLimiter(2, c.now)

	l.Allow("203.0.113.9")
	l.Allow("203.0.113.9")
	if l.Allow("203.0.113.9") {
		t.Fatal("the first key was not exhausted")
	}
	if !l.Allow("198.51.100.4") {
		t.Error("a second key was refused because the first had spent its allowance")
	}
}

func TestTheBucketRefillsOverTime(t *testing.T) {
	c := newClock()
	l := httpx.NewLimiter(60, c.now)

	for range 60 {
		l.Allow("k")
	}
	if l.Allow("k") {
		t.Fatal("not exhausted")
	}

	c.add(time.Second)
	if !l.Allow("k") {
		t.Error("a second of refill bought no request")
	}
	if l.Allow("k") {
		t.Error("a second of refill bought two")
	}

	c.add(30 * time.Second)
	allowed := 0
	for range 60 {
		if l.Allow("k") {
			allowed++
		}
	}
	if allowed != 30 {
		t.Errorf("30 seconds of refill bought %d requests, want 30", allowed)
	}
}

// A bucket that fills without a ceiling turns an idle hour into an hour's
// worth of burst.
func TestIdleTimeDoesNotAccumulateBeyondTheAllowance(t *testing.T) {
	c := newClock()
	l := httpx.NewLimiter(5, c.now)

	if !l.Allow("k") {
		t.Fatal("the first request of a fresh allowance was refused")
	}

	c.add(time.Hour)

	allowed := 0
	for range 100 {
		if l.Allow("k") {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("an idle hour bought %d requests, want the allowance of 5", allowed)
	}
}

// RemoteAddr is `host:port`, and the port is a new ephemeral number on every
// connection.
func TestClientKeyStripsThePortSoOneHostIsOneBucket(t *testing.T) {
	first := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
	first.RemoteAddr = "203.0.113.9:41235"
	second := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
	second.RemoteAddr = "203.0.113.9:52996"

	if httpx.ClientKey(first) != httpx.ClientKey(second) {
		t.Errorf("two connections from one host got two keys: %q and %q",
			httpx.ClientKey(first), httpx.ClientKey(second))
	}
	if got := httpx.ClientKey(first); got != "203.0.113.9" {
		t.Errorf("ClientKey = %q, want the host alone", got)
	}
}

// An address with no port at all is used whole rather than losing the key: an
// empty key would put every such client in one shared bucket.
func TestClientKeyHandlesIPv6AndAMissingPort(t *testing.T) {
	cases := map[string]string{
		"[2001:db8::1]:41235": "2001:db8::1",
		"203.0.113.9":         "203.0.113.9",
		"":                    "",
	}
	for addr, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := httpx.ClientKey(r); got != want {
			t.Errorf("ClientKey(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestTheMiddlewareRefusesWithTheEnvelopeAndDoesNotRunTheHandler(t *testing.T) {
	log, _ := testLogger()
	c := newClock()
	l := httpx.NewLimiter(1, c.now)

	var ran int
	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		w.WriteHeader(http.StatusOK)
	}), httpx.RateLimit(l, log))

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
		r.RemoteAddr = "203.0.113.9:41235"
		return r
	}

	first := httptest.NewRecorder()
	h.ServeHTTP(first, req())
	if first.Code != http.StatusOK {
		t.Fatalf("the first request was refused: %d", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, req())
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", second.Code)
	}
	if got := second.Body.String(); got != `{"code":"rate_limited"}` {
		t.Errorf("body = %s, want {\"code\":\"rate_limited\"}", got)
	}
	if ran != 1 {
		t.Errorf("the handler ran %d times; a refused request must not reach it", ran)
	}
}

// : reject rather than queue.
func TestARefusalIsImmediateRatherThanAWait(t *testing.T) {
	log, _ := testLogger()
	c := newClock()
	l := httpx.NewLimiter(1, c.now)
	h := httpx.Chain(http.HandlerFunc(ok), httpx.RateLimit(l, log))

	r := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
	r.RemoteAddr = "203.0.113.9:41235"
	h.ServeHTTP(httptest.NewRecorder(), r)

	start := time.Now()
	h.ServeHTTP(httptest.NewRecorder(), r)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("a refusal took %s; it must not queue", elapsed)
	}
}

// The limiter is shared by every request on the server, so its own
// bookkeeping is the race.
func TestConcurrentCallersSpendExactlyTheAllowanceBetweenThem(t *testing.T) {
	c := newClock()
	l := httpx.NewLimiter(50, c.now)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("k") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 50 {
		t.Errorf("200 concurrent callers spent %d of a 50 allowance", allowed.Load())
	}
}

// RateLimit the key is the client address.
func TestRateLimitBySpendsTheAllowancePerKeyTheFunctionReturns(t *testing.T) {
	log, _ := testLogger()
	c := newClock()
	l := httpx.NewLimiter(2, c.now)

	byHeader := httpx.RateLimitBy(l, log, "traveller", func(r *http.Request) (string, bool) {
		return r.Header.Get("X-Who"), true
	})
	h := httpx.Chain(http.HandlerFunc(ok), byHeader)

	call := func(who string) int {
		r := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)
		r.RemoteAddr = "203.0.113.9:41235"
		r.Header.Set("X-Who", who)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	if call("matt") != http.StatusOK || call("matt") != http.StatusOK {
		t.Fatal("the allowance of 2 was not served")
	}
	if got := call("matt"); got != http.StatusTooManyRequests {
		t.Errorf("the 3rd request for one key = %d, want 429", got)
	}
	if got := call("kit"); got != http.StatusOK {
		t.Errorf("a second key at the SAME address = %d, want 200 — the limiter is not "+
			"keyed on what the function returned", got)
	}
}

// A request it cannot key is refused, not waved through.
func TestARefusalWithNoLoggerIsStillARefusal(t *testing.T) {
	c := newClock()

	t.Run("the limited branch answers 429", func(t *testing.T) {
		l := httpx.NewLimiter(1, c.now)
		h := httpx.Chain(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			httpx.RateLimitBy(l, nil, "traveller", func(*http.Request) (string, bool) {
				return "one", true
			}),
		)

		first := httptest.NewRecorder()
		h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))
		if first.Code != http.StatusOK {
			t.Fatalf("the allowance = %d, want 200", first.Code)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("a refusal with no logger = %d, want 429", rec.Code)
		}
		if got := rec.Body.String(); got != `{"code":"rate_limited"}` {
			t.Errorf("body = %s, want the rate_limited envelope", got)
		}
	})

	t.Run("the unkeyable branch still answers 500, for its own reason", func(t *testing.T) {
		l := httpx.NewLimiter(60, c.now)
		h := httpx.Chain(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}),
			httpx.RateLimitBy(l, nil, "traveller", func(*http.Request) (string, bool) {
				return "", false
			}),
		)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("an unkeyable request with no logger = %d, want 500", rec.Code)
		}
	})
}

func TestRateLimitByRefusesARequestItCannotKey(t *testing.T) {
	log, _ := testLogger()
	c := newClock()
	l := httpx.NewLimiter(60, c.now)

	var ran int
	h := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		w.WriteHeader(http.StatusOK)
	}), httpx.RateLimitBy(l, log, "traveller", func(*http.Request) (string, bool) {
		return "", false
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("an unkeyable request = %d, want 500", rec.Code)
	}
	if got := rec.Body.String(); got != `{"code":"internal"}` {
		t.Errorf("body = %s, want the envelope", got)
	}
	if ran != 0 {
		t.Errorf("the handler ran %d times for a request the limiter could not key", ran)
	}
	if l.Len() != 0 {
		t.Errorf("the limiter took %d buckets for a request it could not key — an "+
			"empty key is one shared bucket for everybody", l.Len())
	}
}

// The two refusals mean different things to an operator, so they are not
// spelled the same in the log.
func TestTheKeyNameIsWhatTheRefusalIsLoggedUnder(t *testing.T) {
	log, logs := testLogger()
	c := newClock()
	l := httpx.NewLimiter(1, c.now)
	h := httpx.Chain(http.HandlerFunc(ok), httpx.RateLimitBy(l, log, "traveller",
		func(*http.Request) (string, bool) { return "traveller-7", true }))

	r := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	h.ServeHTTP(httptest.NewRecorder(), r)

	line := logs.String()
	if !strings.Contains(line, `"traveller":"traveller-7"`) {
		t.Errorf("the refusal is not logged under the key name it was given:\n%s", line)
	}
}
