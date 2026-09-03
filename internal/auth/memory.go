// The in-memory twin of the storage this package needs, exported so one
// implementation serves every package that drives a Service.
package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

var _ Store = (*Memory)(nil)

// Memory answers what postgres.AuthStore answers, in a map, and refuses what
// it refuses. It is safe for concurrent use.
type Memory struct {
	mu         sync.Mutex
	travellers map[string]Traveller
	sessions   map[string]memorySession
	codes      map[string]*SignInCode
	invites    map[string]bool
	standing   map[string]bool
	touched    []string
	next       int
	failWith   error
	clock      func() time.Time
}

type memorySession struct {
	Session
	traveller Traveller
}

// NewMemory builds an empty twin whose clock reads the zero time until
// SetClock is called.
func NewMemory() *Memory {
	return &Memory{
		travellers: map[string]Traveller{},
		sessions:   map[string]memorySession{},
		codes:      map[string]*SignInCode{},
		invites:    map[string]bool{},
		standing:   map[string]bool{},
	}
}

// SetClock replaces the twin's clock, which is what dates a session's
// last_used_at and a revocation.
func (m *Memory) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = now
}

// FailWith makes every method answer err, which is how a caller reaches the
// branch that separates a dependency being down from a credential being wrong.
func (m *Memory) FailWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failWith = err
}

// Touches names every traveller/session TouchSession has been called for.
func (m *Memory) Touches() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.touched...)
}

// ClearCode drops the live code for an address, so a second sign-in asks for
// one rather than meeting the throttle.
func (m *Memory) ClearCode(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.travellers[strings.ToLower(email)]
	if !ok {
		return
	}
	delete(m.codes, held.ID)
}

// TravellerIDs answers every traveller the twin holds, in no order.
func (m *Memory) TravellerIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.travellers))
	for _, tr := range m.travellers {
		out = append(out, tr.ID)
	}
	return out
}

// StoredTokenHashes answers what the twin holds for every session, which is
// how a leg asserts a plaintext token never reached storage.
func (m *Memory) StoredTokenHashes() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, 0, len(m.sessions))
	for _, held := range m.sessions {
		out = append(out, append([]byte(nil), held.TokenHash...))
	}
	return out
}

func (m *Memory) now() time.Time {
	if m.clock == nil {
		return time.Time{}
	}
	return m.clock()
}

// memoryUUID is a valid uuid shape that encodes its counter, so a failure
// message still says which traveller it was about.
func memoryUUID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func (m *Memory) CreateTraveller(_ context.Context, email string) (Traveller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return Traveller{}, m.failWith
	}
	key := strings.ToLower(email)
	if _, held := m.travellers[key]; held {
		return Traveller{}, ErrEmailTaken
	}
	m.next++
	tr := Traveller{ID: memoryUUID(m.next), Email: email}
	m.travellers[key] = tr
	return tr, nil
}

func (m *Memory) TravellerByEmail(_ context.Context, email string) (Traveller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return Traveller{}, m.failWith
	}
	held, ok := m.travellers[strings.ToLower(email)]
	if !ok {
		return Traveller{}, ErrNoTraveller
	}
	return held, nil
}

func (m *Memory) CreateSession(_ context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return "", m.failWith
	}
	var owner Traveller
	for _, held := range m.travellers {
		if held.ID == travellerID {
			owner = held
		}
	}
	id := "s" + string(rune('a'+len(m.sessions)))
	m.sessions[string(tokenHash)] = memorySession{
		Session: Session{
			ID: id, TravellerID: travellerID,
			TokenHash:  append([]byte(nil), tokenHash...),
			LastUsedAt: m.now(),
			ExpiresAt:  expiresAt,
		},
		traveller: owner,
	}
	return id, nil
}

func (m *Memory) SessionByTokenHash(_ context.Context, tokenHash []byte) (Session, Traveller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return Session{}, Traveller{}, m.failWith
	}
	held, ok := m.sessions[string(tokenHash)]
	if !ok {
		return Session{}, Traveller{}, ErrNoSession
	}
	return held.Session, held.traveller, nil
}

func (m *Memory) TouchSession(_ context.Context, travellerID, sessionID string, at, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return m.failWith
	}
	m.touched = append(m.touched, travellerID+"/"+sessionID)
	for key, held := range m.sessions {
		if held.ID == sessionID {
			held.LastUsedAt = at
			held.ExpiresAt = expiresAt
			m.sessions[key] = held
		}
	}
	return nil
}

// RevokeSession and RevokeEverySession mirror `update … where revoked_at is
// NULL`, so a second revocation moves nothing.
func (m *Memory) RevokeSession(_ context.Context, travellerID string, tokenHash []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return false, m.failWith
	}
	held, ok := m.sessions[string(tokenHash)]
	if !ok || held.TravellerID != travellerID || held.RevokedAt != nil {
		return false, nil
	}
	at := m.now()
	held.RevokedAt = &at
	m.sessions[string(tokenHash)] = held
	return true, nil
}

func (m *Memory) RevokeEverySession(_ context.Context, travellerID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWith != nil {
		return 0, m.failWith
	}
	var moved int64
	at := m.now()
	for key, held := range m.sessions {
		if held.TravellerID != travellerID || held.RevokedAt != nil {
			continue
		}
		held.RevokedAt = &at
		m.sessions[key] = held
		moved++
	}
	return moved, nil
}

// MintInvite is not on the Store port: nothing reaches it through one. It is
// here because a registration leg has to put an invite in the table first.
func (m *Memory) MintInvite(_ context.Context, hash []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invites[string(hash)] = false
	return nil
}

// AcceptInvite makes one code claimable any number of times. No real adapter
// behaves this way; a harness whose every leg registers somebody needs it.
func (m *Memory) AcceptInvite(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.standing[string(HashInvite(code))] = true
}

func (m *Memory) ClaimInvite(_ context.Context, hash []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.standing[string(hash)] {
		return nil
	}
	spent, held := m.invites[string(hash)]
	if !held || spent {
		return ErrInviteSpent
	}
	m.invites[string(hash)] = true
	return nil
}

func (m *Memory) IssueCode(_ context.Context, travellerID string, hash []byte, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[travellerID] = &SignInCode{Hash: hash, IssuedAt: m.now(), ExpiresAt: expiresAt}
	return nil
}

func (m *Memory) CodeFor(_ context.Context, travellerID string) (SignInCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.codes[travellerID]
	if !ok {
		return SignInCode{}, ErrNoCode
	}
	return *held, nil
}

func (m *Memory) CountAttempt(_ context.Context, travellerID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	held, ok := m.codes[travellerID]
	if !ok {
		return 0, ErrNoCode
	}
	held.Attempts++
	return held.Attempts, nil
}

func (m *Memory) BurnCode(_ context.Context, travellerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.codes, travellerID)
	return nil
}
