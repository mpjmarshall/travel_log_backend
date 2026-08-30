// The panel's own sessions, held in memory for one process.
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// IdleTTL is how long a session survives without being used. A restart of the
// API ends every session, which is the cost of holding them in memory.
const IdleTTL = 12 * time.Hour

// tokenBytes is the length of both the session id and the CSRF token.
const tokenBytes = 32

type entry struct {
	csrf     string
	deadline time.Time
}

// Sessions is the live set. The clock is always a parameter, so a test never
// waits and the window is exact.
type Sessions struct {
	mu sync.Mutex
	m  map[string]entry
}

func NewSessions() *Sessions {
	return &Sessions{m: map[string]entry{}}
}

// New mints a session and its CSRF token, which are independently random so
// one can never be derived from the other.
func (s *Sessions) New(now time.Time) (id, csrf string, err error) {
	if id, err = token(); err != nil {
		return "", "", err
	}
	if csrf, err = token(); err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = entry{csrf: csrf, deadline: now.Add(IdleTTL)}
	return id, csrf, nil
}

// Get answers the session's CSRF token and slides its deadline. An expired
// session is deleted on the way past rather than left to accumulate.
func (s *Sessions) Get(id string, now time.Time) (csrf string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.m[id]
	if !found {
		return "", false
	}
	if !now.Before(e.deadline) {
		delete(s.m, id)
		return "", false
	}

	e.deadline = now.Add(IdleTTL)
	s.m[id] = e
	return e.csrf, true
}

func (s *Sessions) Drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

// Len is for tests; nothing in the panel counts sessions.
func (s *Sessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func token() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
