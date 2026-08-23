package httpx

import (
	"strconv"
	"testing"
	"time"
)

// Unbounded map growth is the one failure this type can have that no external
// behaviour reveals until the process is killed: an attacker with a large
// address space adds a bucket per address and never comes back for it.
//
// The sweep is safe because a FULL bucket and an ABSENT bucket are the same
// thing — Allow creates an absent one full — so this leg also asserts the
// answers do not change across a prune.
func TestIdleBucketsArePrunedRatherThanKeptForever(t *testing.T) {
	original := limiterPruneAbove
	limiterPruneAbove = 8
	t.Cleanup(func() { limiterPruneAbove = original })

	at := time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)
	l := NewLimiter(60, func() time.Time { return at })

	for i := range 8 {
		l.Allow("idle-" + strconv.Itoa(i))
	}
	if l.Len() != 8 {
		t.Fatalf("Len = %d, want 8 before any sweep", l.Len())
	}

	// A minute later every one of them has refilled to full, so all eight are
	// indistinguishable from clients that were never seen.
	at = at.Add(time.Minute)
	l.Allow("a-new-client")

	if got := l.Len(); got != 1 {
		t.Errorf("Len = %d after a sweep of eight idle buckets, want 1", got)
	}

	// And the sweep changed no answer: the pruned client still has its whole
	// allowance, exactly as a full bucket would have given it.
	allowed := 0
	for range 100 {
		if l.Allow("idle-0") {
			allowed++
		}
	}
	if allowed != 60 {
		t.Errorf("a pruned client got %d requests, want its full 60", allowed)
	}
}

// A bucket that is still spending is not swept. Pruning one would hand a client
// back an allowance it had spent, which is the limiter failing open.
func TestABusyBucketSurvivesTheSweep(t *testing.T) {
	original := limiterPruneAbove
	limiterPruneAbove = 4
	t.Cleanup(func() { limiterPruneAbove = original })

	at := time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)
	l := NewLimiter(60, func() time.Time { return at })

	for range 60 {
		l.Allow("busy")
	}
	for i := range 4 {
		l.Allow("idle-" + strconv.Itoa(i))
	}

	at = at.Add(time.Second) // one token back for busy, full for the idle four
	l.Allow("trigger-the-sweep")

	if l.Allow("busy") && l.Allow("busy") {
		t.Error("the busy bucket was pruned and got its whole allowance back")
	}
}
