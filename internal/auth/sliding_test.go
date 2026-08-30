package auth

import (
	"context"
	"testing"
	"time"
)

func aSession(t *testing.T, clock *time.Time) (*Service, *fakeStore, Issued) {
	t.Helper()
	store := newFakeStore()
	s := newServiceAtClock(t, store, func() time.Time { return *clock })
	ctx := context.Background()
	if _, err := s.Register(ctx, "matt@example.com"); err != nil {
		t.Fatal(err)
	}
	code, _, _, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, issued
}

func expiryOf(t *testing.T, store *fakeStore) time.Time {
	t.Helper()
	for _, held := range store.sessions {
		return held.ExpiresAt
	}
	t.Fatal("no session")
	return time.Time{}
}

func TestUsingASessionPushesItsExpiryOut(t *testing.T) {
	clock := at(t, testNow)
	s, store, issued := aSession(t, &clock)
	first := expiryOf(t, store)

	clock = clock.Add(20 * 24 * time.Hour)
	if _, err := s.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	got := expiryOf(t, store)
	want := clock.Add(DefaultSessionTTL)
	if !got.Equal(want) {
		t.Fatalf("the expiry is %s, want %s — an active traveller is asked for a "+
			"new code every thirty days whether or not they have been away", got, want)
	}
	if !got.After(first) {
		t.Fatal("the expiry did not move")
	}
}

func TestTheExpiryIsPushedFromNowAndNotFromWhereItWas(t *testing.T) {
	clock := at(t, testNow)
	s, store, issued := aSession(t, &clock)

	clock = clock.Add(time.Hour)
	if _, err := s.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatal(err)
	}

	got := expiryOf(t, store)
	if want := clock.Add(DefaultSessionTTL); !got.Equal(want) {
		t.Fatalf("the expiry is %s, want %s: adding the TTL to the old expiry "+
			"instead of to now makes a session live for ever", got, want)
	}
}

func TestASessionIsExtendedAtMostOncePerInterval(t *testing.T) {
	clock := at(t, testNow)
	s, store, issued := aSession(t, &clock)
	ctx := context.Background()

	clock = clock.Add(TouchInterval - time.Second)
	for range 20 {
		if _, err := s.Authenticate(ctx, issued.Token); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(store.touched); n != 0 {
		t.Fatalf("twenty requests inside the interval wrote %d times; extending "+
			"on every request is a write per read", n)
	}
}

func TestAnExpiredSessionIsNotRevivedByUsingIt(t *testing.T) {
	clock := at(t, testNow)
	s, _, issued := aSession(t, &clock)

	clock = clock.Add(DefaultSessionTTL + time.Second)
	if _, err := s.Authenticate(context.Background(), issued.Token); err == nil {
		t.Fatal("an expired session authenticated, so the sliding window has no far edge")
	}
}
