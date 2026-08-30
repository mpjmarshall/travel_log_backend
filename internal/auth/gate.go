// The Argon2 concurrency cap.
package auth

import (
	"errors"
	"fmt"
)

// ErrBusy is the N+1th concurrent call.
var ErrBusy = errors.New("auth: as many passphrase hashes are running as this build allows")

// Gate is the semaphore.
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

// Capped is a Hasher wrapping a Hasher, so the cap is applied once here
// Than remembered at every call site.
type Capped struct {
	Hasher Hasher
	Gate   *Gate
}

// The `defer` in both methods is what a failing or panicking hasher needs.
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
