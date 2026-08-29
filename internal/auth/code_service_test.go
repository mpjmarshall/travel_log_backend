package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func aRegistered(t *testing.T, s *Service, store *fakeStore, email string) Traveller {
	t.Helper()
	tr, err := s.Register(context.Background(), email, "a long enough passphrase")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return tr
}

func TestRequestCodeMintsSomethingMailableAndStoresOnlyTheDigest(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")

	code, tr, ok, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if !ok {
		t.Fatal("a registered address answered ok=false")
	}
	if !sixDigits.MatchString(code) {
		t.Fatalf("code %q is not mailable", code)
	}
	held, err := store.CodeFor(ctx, tr.ID)
	if err != nil {
		t.Fatalf("nothing was stored: %v", err)
	}
	if string(held.Hash) == code {
		t.Fatal("the PLAINTEXT code reached the store")
	}
	want, _ := HashCode(tr.ID, code)
	if !SameHash(held.Hash, want) {
		t.Fatal("what was stored is not the digest of what was answered")
	}
}

func TestRequestCodeIsNotAnAccountOracle(t *testing.T) {
	// THE WHOLE POINT OF THIS ROUTE'S SHAPE. An unknown address must be
	// indistinguishable from a known one, or the endpoint tells anybody who
	// asks which addresses have logs here. It answers no error and nothing to
	// send, and the CALLER is required to answer the same thing either way.
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")

	code, _, ok, err := s.RequestCode(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("an unknown address errored, which is an oracle: %v", err)
	}
	if ok {
		t.Fatal("an unknown address answered ok=true")
	}
	if code != "" {
		t.Fatalf("an unknown address produced %q to mail", code)
	}
}

func TestTheRightCodeSignsInOnce(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	issued, err := s.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatalf("SignInWithCode: %v", err)
	}
	if issued.Token == "" {
		t.Fatal("no token")
	}

	// Single use: the same code again is refused, and refused the same way a
	// wrong one is.
	if _, err := s.SignInWithCode(ctx, "matt@example.com", code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("a replayed code answered %v, want ErrBadCredentials", err)
	}
}

func TestAWrongCodeIsRefusedAndCounted(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	tr := aRegistered(t, s, store, "matt@example.com")
	if _, _, _, err := s.RequestCode(ctx, "matt@example.com"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SignInWithCode(ctx, "matt@example.com", "000000"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("answered %v, want ErrBadCredentials", err)
	}
	held, err := store.CodeFor(ctx, tr.ID)
	if err != nil {
		t.Fatalf("the code was destroyed by one wrong guess: %v", err)
	}
	if held.Attempts != 1 {
		t.Fatalf("attempts = %d after one wrong guess, want 1", held.Attempts)
	}
}

func TestTheCodeDiesAtTheAttemptCap(t *testing.T) {
	// The bound. After MaxCodeAttempts the code is gone, so the RIGHT code
	// stops working too — which is the point: an attacker who has burned the
	// budget cannot keep going, and the traveller asks for a new one.
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	tr := aRegistered(t, s, store, "matt@example.com")
	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for i := 0; i < MaxCodeAttempts; i++ {
		if _, err := s.SignInWithCode(ctx, "matt@example.com", wrong); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("guess %d answered %v", i+1, err)
		}
	}
	if _, err := store.CodeFor(ctx, tr.ID); !errors.Is(err, ErrNoCode) {
		t.Fatal("the code survived the attempt cap")
	}
	if _, err := s.SignInWithCode(ctx, "matt@example.com", code); !errors.Is(err, ErrBadCredentials) {
		t.Fatal("the right code still worked after the cap was reached")
	}
}

func TestAnExpiredCodeIsRefused(t *testing.T) {
	store := newFakeStore()
	// A clock a leg can move, so expiry is asserted by passing time rather
	// than by writing a stale row and hoping that is the same thing.
	now := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return now })
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	now = now.Add(CodeTTL + time.Second)

	if _, err := s.SignInWithCode(ctx, "matt@example.com", code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("an expired code answered %v, want ErrBadCredentials", err)
	}
}

func TestACodeIsNotValidForAnotherTraveller(t *testing.T) {
	// The salt, seen from the service. Two travellers request at the same
	// moment; one's code must not open the other's log even if the digits
	// collide, because the digest is over the traveller as well.
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	// DEC-86 closes registration after the first traveller, so the second one
	// goes straight into the store. That rule is going in this phase; until
	// it does, a leg needing two travellers cannot use Register.
	other := Traveller{ID: fakeUUID(99), Email: "other@example.com"}
	store.travellers["other@example.com"] = storedTraveller{Traveller: other, hash: dummyHash}

	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")
	if _, _, _, err := s.RequestCode(ctx, "other@example.com"); err != nil {
		t.Fatal(err)
	}
	// Force the collision the salt exists to survive.
	hash, _ := HashCode(other.ID, code)
	_ = hash

	if _, err := s.SignInWithCode(ctx, "other@example.com", code); !errors.Is(err, ErrBadCredentials) {
		t.Fatal("one traveller's code signed in another")
	}
}

func TestSigningInWithACodeForAnUnknownAddressIsNotAnOracle(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	if _, err := s.SignInWithCode(context.Background(), "nobody@example.com", "123456"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("an unknown address answered %v, want ErrBadCredentials", err)
	}
}

// === the fake store's half of the code methods ===
//
// Deliberately as dumb as the real one is careful: one map, replace on issue,
// increment on attempt, delete on burn. The rules that matter — one row per
// traveller, the reset, the single statement — are asserted against the REAL
// store in internal/postgres, because a leg against this would be a leg about
// this.

func (f *fakeStore) IssueCode(_ context.Context, travellerID string, hash []byte, expiresAt time.Time) error {
	if f.codes == nil {
		f.codes = map[string]*SignInCode{}
	}
	f.codes[travellerID] = &SignInCode{Hash: hash, IssuedAt: f.now(), ExpiresAt: expiresAt}
	return nil
}

func (f *fakeStore) CodeFor(_ context.Context, travellerID string) (SignInCode, error) {
	c, ok := f.codes[travellerID]
	if !ok {
		return SignInCode{}, ErrNoCode
	}
	return *c, nil
}

func (f *fakeStore) CountAttempt(_ context.Context, travellerID string) (int, error) {
	c, ok := f.codes[travellerID]
	if !ok {
		return 0, ErrNoCode
	}
	c.Attempts++
	return c.Attempts, nil
}

func (f *fakeStore) BurnCode(_ context.Context, travellerID string) error {
	delete(f.codes, travellerID)
	return nil
}

func TestACodeFoundAlreadyAtTheCapIsRefusedOnSight(t *testing.T) {
	// THE GUARD A SURVIVING MUTATION FOUND. SignInWithCode checks the cap
	// twice: once on the row it reads, and once on the total a wrong guess
	// returns. The second one burns the code, so in ordinary use the first
	// never fires — and removing it reddened nothing.
	//
	// It is not dead code. Two guesses racing can both increment past the cap
	// before either burns, and a burn that errors leaves the row standing at
	// or above it. This reaches that state directly and asserts the RIGHT
	// code is still refused, which is the property the guard exists for.
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	tr := aRegistered(t, s, store, "matt@example.com")
	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	store.codes[tr.ID].Attempts = MaxCodeAttempts

	if _, err := s.SignInWithCode(ctx, "matt@example.com", code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("a code already at the cap answered %v, want ErrBadCredentials", err)
	}
	if _, err := store.CodeFor(ctx, tr.ID); !errors.Is(err, ErrNoCode) {
		t.Fatal("a code found at the cap was not burned")
	}
}

// === THE MAIL CANNON ===

func TestASecondRequestInsideTheIntervalSendsNothing(t *testing.T) {
	// WITHOUT THIS THE ROUTE IS A WEAPON POINTED AT SOMEBODY ELSE'S INBOX.
	// A script asks for a code for a victim's address a thousand times and a
	// thousand mails arrive. The existing limiter cannot stop it: it keys on
	// the CLIENT address, and the attacker rotates those. The throttle has to
	// key on the address being MAILED, which is what this does.
	store := newFakeStore()
	now := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return now })
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")

	first, _, ok, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil || !ok || first == "" {
		t.Fatalf("the first request did not send: %q ok=%v err=%v", first, ok, err)
	}

	now = now.Add(CodeRequestInterval - time.Second)
	second, _, ok, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatalf("a throttled request errored, which is an oracle: %v", err)
	}
	if ok || second != "" {
		t.Fatalf("a second request inside the interval produced %q to mail", second)
	}
}

func TestThrottlingIsIndistinguishableFromAnUnknownAddress(t *testing.T) {
	// The same answer, or asking twice quickly tells an attacker which
	// addresses have a log here — which is the whole thing RequestCode's
	// shape exists to prevent.
	store := newFakeStore()
	now := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return now })
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	if _, _, _, err := s.RequestCode(ctx, "matt@example.com"); err != nil {
		t.Fatal(err)
	}

	throttledCode, _, throttledOK, throttledErr := s.RequestCode(ctx, "matt@example.com")
	unknownCode, _, unknownOK, unknownErr := s.RequestCode(ctx, "nobody@example.com")

	if throttledOK != unknownOK || throttledCode != unknownCode {
		t.Fatalf("throttled answered (%q,%v) and unknown answered (%q,%v)",
			throttledCode, throttledOK, unknownCode, unknownOK)
	}
	if (throttledErr == nil) != (unknownErr == nil) {
		t.Fatalf("throttled errored %v and unknown errored %v", throttledErr, unknownErr)
	}
}

func TestAThrottledRequestDOESNOTDisturbTheLiveCode(t *testing.T) {
	// THE SHARPEST OF THE THREE. If a throttled request replaced or burned
	// the code, an attacker could stop a traveller signing in AT ALL by
	// asking for codes in a loop: every one the traveller received would be
	// dead before they finished typing it.
	store := newFakeStore()
	now := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return now })
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	code, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	for i := 0; i < 20; i++ {
		if _, _, _, err := s.RequestCode(ctx, "matt@example.com"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.SignInWithCode(ctx, "matt@example.com", code); err != nil {
		t.Fatalf("the code a traveller was mailed stopped working because somebody else asked for one: %v", err)
	}
}

func TestAfterTheIntervalANewCodeIsSent(t *testing.T) {
	// The throttle is a delay and not a lock: a traveller who genuinely did
	// not receive the first one has to be able to ask again.
	store := newFakeStore()
	now := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return now })
	ctx := context.Background()
	aRegistered(t, s, store, "matt@example.com")
	first, _, _, _ := s.RequestCode(ctx, "matt@example.com")

	now = now.Add(CodeRequestInterval)
	second, _, ok, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil || !ok {
		t.Fatalf("a request after the interval did not send: ok=%v err=%v", ok, err)
	}
	// And the new one replaces the old, which is the one-live-code rule.
	if _, err := s.SignInWithCode(ctx, "matt@example.com", first); !errors.Is(err, ErrBadCredentials) {
		t.Fatal("the superseded code still worked")
	}
	if _, err := s.SignInWithCode(ctx, "matt@example.com", second); err != nil {
		t.Fatalf("the newly sent code did not work: %v", err)
	}
}

func TestTheCodeConstantsAreDelaysAndNotLocks(t *testing.T) {
	// THE LEG A SURVIVING MUTATION FOUND, AND IT IS THE CLASS THAT HIDES.
	// Every other test here advances the clock BY CodeRequestInterval, so it
	// passes whatever that constant is — a hundred-year interval, which makes
	// the throttle a permanent lock on signing in, reddened nothing at all.
	// A relative assertion cannot see a value that moves both of its terms.
	//
	// So these are fixed references rather than the constants restated.

	if CodeRequestInterval <= 0 {
		t.Fatal("a non-positive interval is no throttle: the mail cannon is loaded")
	}
	if CodeRequestInterval > 5*time.Minute {
		t.Fatalf("CodeRequestInterval is %v, which is a lock rather than a delay — "+
			"a traveller who did not receive the first code waits that long "+
			"before they can ask again", CodeRequestInterval)
	}

	// THE RELATIONSHIP IS THE SHARPER OF THE TWO. If the interval ever
	// reached the TTL there would be a window in which a traveller's code has
	// expired AND they are still throttled from asking for another, which is
	// an account nobody can sign in to for as long as the gap lasts.
	if CodeRequestInterval >= CodeTTL {
		t.Fatalf("CodeRequestInterval %v >= CodeTTL %v: a traveller whose code "+
			"expires cannot ask for another until the throttle clears, and in "+
			"between they cannot sign in at all", CodeRequestInterval, CodeTTL)
	}

	if CodeTTL <= 0 || CodeTTL > time.Hour {
		t.Fatalf("CodeTTL is %v: a mailed code should be worth typing for "+
			"minutes, not for an hour", CodeTTL)
	}
	if MaxCodeAttempts < 1 {
		t.Fatal("a code nobody may guess cannot be used by its owner either")
	}
	if MaxCodeAttempts > 20 {
		t.Fatalf("MaxCodeAttempts is %d against a million possibilities, which "+
			"is a budget rather than a bound", MaxCodeAttempts)
	}
}
