// The Argon2 concurrency cap, test-first.
package auth

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blocking is a Hasher that parks inside Hash and Verify until it is
// released, so a test can hold N calls open and ask what the N+1th does.
type blocking struct {
	entered chan struct{}
	release chan struct{}
}

func newBlocking(n int) *blocking {
	return &blocking{entered: make(chan struct{}, n+1), release: make(chan struct{})}
}

func (b *blocking) park() {
	b.entered <- struct{}{}
	<-b.release
}

func (b *blocking) Hash(string) (string, error)         { b.park(); return "encoded", nil }
func (b *blocking) Verify(string, string) (bool, error) { b.park(); return true, nil }

func TestNewGateRefusesZeroRatherThanTreatingItAsUnlimited(t *testing.T) {
	for _, n := range []int{0, -1, -8} {
		if _, err := NewGate(n); err == nil {
			t.Errorf("NewGate(%d) = nil error.\n"+
				"    DEC-48: a zero-capacity semaphore blocks the first login FOR EVER\n"+
				"    rather than refusing it, and 'unlimited' is the one reading that\n"+
				"    undoes the whole decision. config.Load already floors\n"+
				"    ARGON2_MAX_CONCURRENT at 1; this is the other half.", n)
		}
	}
}

func TestNewGateAcceptsOne(t *testing.T) {
	if _, err := NewGate(1); err != nil {
		t.Errorf("NewGate(1) = %v, want a gate", err)
	}
}

func TestTheNPlusOnethConcurrentCallIsRefusedAndNotMadeToWait(t *testing.T) {
	const n = 3
	inner := newBlocking(n)
	g, err := NewGate(n)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	capped := Capped{Hasher: inner, Gate: g}

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = capped.Hash("x") }()
	}
	for range n {
		select {
		case <-inner.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only some of the %d calls reached the hasher", n)
		}
	}

	done := make(chan error, 1)
	go func() { _, err := capped.Hash("x"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Errorf("the %dth concurrent call answered %v, want ErrBusy", n+1, err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("the %dth concurrent call is still waiting after 2s.\n"+
			"    DEC-48 rejects queueing BY NAME: it converts memory exhaustion into\n"+
			"    timeout exhaustion, which is the same outage wearing a different error.", n+1)
	}

	close(inner.release)
	wg.Wait()
}

func TestVerifyIsCappedToo(t *testing.T) {
	inner := newBlocking(1)
	g, err := NewGate(1)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	capped := Capped{Hasher: inner, Gate: g}

	go func() { _, _ = capped.Verify("encoded", "x") }()
	select {
	case <-inner.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("the first Verify never reached the hasher")
	}

	second := make(chan error, 1)
	go func() { _, err := capped.Verify("encoded", "x"); second <- err }()
	select {
	case err := <-second:
		if !errors.Is(err, ErrBusy) {
			t.Errorf("a second concurrent Verify answered %v, want ErrBusy", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("a second concurrent Verify is still running after 2s, so it was not capped.\n" +
			"    Verify calls argon2.IDKey exactly as Hash does and costs the same\n" +
			"    64 MiB. Capping only Hash caps register and leaves sign-in — the\n" +
			"    route an attacker can hit without creating anything — uncapped.")
	}
	close(inner.release)
}

func TestASlotIsReturnedWhenTheCallFinishes(t *testing.T) {
	g, err := NewGate(1)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	capped := Capped{Hasher: Argon2id{Params: cheap.Params}, Gate: g}

	for i := range 5 {
		if _, err := capped.Hash("x"); err != nil {
			t.Fatalf("sequential call %d answered %v — the slot was not returned", i, err)
		}
	}
}

// A hasher that fails must still return its slot, or malformed stored hashes
// drain the gate permanently.
func TestASlotIsReturnedEvenWhenTheHasherFails(t *testing.T) {
	g, err := NewGate(1)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	capped := Capped{Hasher: Argon2id{Params: cheap.Params}, Gate: g}

	for range 3 {
		if _, err := capped.Verify("not a hash", "x"); !errors.Is(err, ErrHashEncoding) {
			t.Fatalf("Verify answered %v, want the encoding error", err)
		}
	}
	if _, err := capped.Hash("x"); err != nil {
		t.Errorf("after three failed Verifies the gate answers %v — a failing call kept its slot", err)
	}
}

// A panicking hasher must return its slot too.
func TestASlotIsReturnedWhenTheHasherPanics(t *testing.T) {
	g, err := NewGate(1)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	capped := Capped{Hasher: panicking{}, Gate: g}

	func() {
		defer func() { _ = recover() }()
		_, _ = capped.Hash("x")
	}()

	probe := Capped{Hasher: Argon2id{Params: cheap.Params}, Gate: g}
	if _, err := probe.Hash("x"); errors.Is(err, ErrBusy) {
		t.Errorf("the gate is wedged after a panicking call: its slot was never returned")
	}
}

type panicking struct{}

func (panicking) Hash(string) (string, error)         { panic("argon2: number of rounds too small") }
func (panicking) Verify(string, string) (bool, error) { panic("argon2: parallelism degree too low") }

// Capped satisfies Hasher.
func TestCappedIsTheHasher(t *testing.T) {
	g, err := NewGate(2)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	var h Hasher = Capped{Hasher: Argon2id{Params: cheap.Params}, Gate: g}
	encoded, err := h.Hash("x")
	if err != nil {
		t.Fatalf("Hash through the interface: %v", err)
	}
	if ok, err := h.Verify(encoded, "x"); err != nil || !ok {
		t.Errorf("Verify through the interface = (%v, %v), want (true, nil)", ok, err)
	}
}
