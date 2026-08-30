// The admin session store: its window, and the two tokens it mints.
package admin_test

import (
	"testing"
	"time"

	"travellog/internal/admin"
)

var at = time.Unix(1_700_000_000, 0).UTC()

func newSession(t *testing.T, s *admin.Sessions, now time.Time) (string, string) {
	t.Helper()
	id, csrf, err := s.New(now)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return id, csrf
}

func TestASessionExpiresAfterTheIdleWindow(t *testing.T) {
	s := admin.NewSessions()

	live, _ := newSession(t, s, at)
	if _, ok := s.Get(live, at.Add(admin.IdleTTL-time.Second)); !ok {
		t.Error("a session one second inside the window must still be live")
	}

	stale, _ := newSession(t, s, at)
	if _, ok := s.Get(stale, at.Add(admin.IdleTTL+time.Second)); ok {
		t.Error("a session past the window must be gone")
	}
}

func TestReadingASessionExtendsIt(t *testing.T) {
	s := admin.NewSessions()
	id, _ := newSession(t, s, at)

	halfway := at.Add(admin.IdleTTL / 2)
	if _, ok := s.Get(id, halfway); !ok {
		t.Fatal("the session must be live halfway through its window")
	}

	later := halfway.Add(admin.IdleTTL - time.Second)
	if _, ok := s.Get(id, later); !ok {
		t.Error("reading the session must slide its deadline, so a session used " +
			"steadily never expires under an operator who is still working")
	}
}

func TestAnExpiredSessionIsForgottenRatherThanKept(t *testing.T) {
	s := admin.NewSessions()
	id, _ := newSession(t, s, at)

	s.Get(id, at.Add(admin.IdleTTL+time.Second))
	if s.Len() != 0 {
		t.Errorf("Len() = %d after an expired read, want 0: an expired session "+
			"left in the map is a token that outlives its own window", s.Len())
	}
}

func TestTwoSessionsNeverShareATokenOrACSRF(t *testing.T) {
	s := admin.NewSessions()
	id1, csrf1 := newSession(t, s, at)
	id2, csrf2 := newSession(t, s, at)

	for _, pair := range [][2]string{{id1, id2}, {csrf1, csrf2}, {id1, csrf1}} {
		if pair[0] == pair[1] {
			t.Errorf("%q and %q are equal; ids and CSRF tokens are independently random",
				pair[0], pair[1])
		}
	}
	if len(id1) < 32 || len(csrf1) < 32 {
		t.Errorf("id %d chars, csrf %d chars: both are guessing targets and want 32 or more",
			len(id1), len(csrf1))
	}
}

func TestTheCSRFTokenComesBackWithTheSession(t *testing.T) {
	s := admin.NewSessions()
	id, csrf := newSession(t, s, at)

	got, ok := s.Get(id, at)
	if !ok || got != csrf {
		t.Errorf("Get() = %q, %v, want %q, true", got, ok, csrf)
	}
}

func TestDropEndsTheSession(t *testing.T) {
	s := admin.NewSessions()
	id, _ := newSession(t, s, at)

	s.Drop(id)
	if _, ok := s.Get(id, at); ok {
		t.Error("a dropped session must not answer, or signing out does nothing")
	}
}

func TestAnUnknownIdIsNotASession(t *testing.T) {
	s := admin.NewSessions()
	if _, ok := s.Get("nothing-like-a-session", at); ok {
		t.Error("an unknown id answered as a live session")
	}
}
