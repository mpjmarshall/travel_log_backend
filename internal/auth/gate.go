// The Argon2 concurrency cap (DEC-48).
//
// WHAT IT GUARDS, AND WHY THE RATE LIMITER IS NOT ENOUGH ON ITS OWN.
// AUTH_RATE_LIMIT_PER_MIN is per client address, so N addresses buy N times
// the quota; this counts calls rather than callers and is the only thing that
// bounds total Argon2 memory. At the UNTUNED 64 MiB of DEC-08, thirty-two
// simultaneous logins is 2 GiB on a box whose connection pool was sized to the
// megabyte. The two guards are independent and neither is load-bearing alone.
//
// IT REFUSES, IT NEVER QUEUES, and DEC-48 rejects queueing by name: making the
// caller wait converts memory exhaustion into timeout exhaustion, which is the
// same outage wearing a different error. Every acquisition is a non-blocking
// select with a default; there is no timeout to tune because there is no wait.
//
// ZERO IS REFUSED RATHER THAN READ AS UNLIMITED, in both directions of the
// mistake. A zero-capacity buffered channel is an UNBUFFERED one: the first
// login blocks for ever with no second party to hand off to. And a negative
// capacity is worse than either — measured, `make(chan struct{}, -1)` panics
// `makechan: size out of range` and takes the process down at construction.
// config.Load already floors ARGON2_MAX_CONCURRENT at 1; this is the half that
// holds for a caller who is not config.
package auth

import (
	"errors"
	"fmt"
)

// ErrBusy is the N+1th concurrent call. httpapi answers it 429, which is the
// same word the rate limiter uses — deliberately, because to a client they are
// one condition: come back in a moment.
var ErrBusy = errors.New("auth: as many passphrase hashes are running as this build allows")

// Gate is the semaphore. The channel is the whole state: its buffer is the
// ceiling and its length is the count in flight.
type Gate struct{ slots chan struct{} }

// NewGate builds a gate of n slots, and refuses anything below one.
func NewGate(n int) (*Gate, error) {
	if n < 1 {
		return nil, fmt.Errorf("auth: a gate of %d would %s, not refuse work", n,
			map[bool]string{true: "panic at construction", false: "block the first caller for ever"}[n < 0])
	}
	return &Gate{slots: make(chan struct{}, n)}, nil
}

// Enter takes a slot if there is one, and never waits for one.
func (g *Gate) Enter() bool {
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Leave returns a slot.
func (g *Gate) Leave() { <-g.slots }

// Capped is a Hasher wrapping a Hasher, so the cap is applied once here rather
// than remembered at every call site.
//
// BOTH METHODS ARE CAPPED. Verify calls argon2.IDKey exactly as Hash does and
// costs exactly the same 64 MiB; capping Hash alone caps register and leaves
// sign-in — the route an attacker can hit without creating anything — open.
type Capped struct {
	Hasher Hasher
	Gate   *Gate
}

// The `defer` in both methods is what a failing or panicking hasher needs. A
// run of malformed stored hashes, or one panic out of the KDF, would otherwise
// leak a slot per call until the gate is wedged shut and every login
// afterwards is a 429 with nothing anywhere to explain it.
func (c Capped) Hash(passphrase string) (string, error) {
	if !c.Gate.Enter() {
		return "", ErrBusy
	}
	defer c.Gate.Leave()
	return c.Hasher.Hash(passphrase)
}

func (c Capped) Verify(encoded, passphrase string) (bool, error) {
	if !c.Gate.Enter() {
		return false, ErrBusy
	}
	defer c.Gate.Leave()
	return c.Hasher.Verify(encoded, passphrase)
}
