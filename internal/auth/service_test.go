// Register and sign-in as behaviour, against a fake store. Test-first.
//
// THE FIRST LEG IN THIS FILE IS THE ENUMERATION ORACLE, deliberately: it is
// the one most easily lost, because every later change to sign-in has an
// obvious, helpful, wrong version of itself — "no account with that address"
// — and the leg is the only thing in the system that says no.
//
// IT RUNS WITHOUT A DATABASE, and that is the point of the Store interface
// rather than a consequence of it. A leg that only runs behind a
// TEST_DATABASE_URL skip is a leg that stops being run.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeStore is the Store interface with a map behind it. It does NOT reproduce
// the database's rules: lower(email) uniqueness is enforced here in Go, which
// the real store leaves to travellers_email_lower_key. The two are asserted
// separately and on purpose — this file is about the service's decisions and
// internal/postgres is about the schema's.
type fakeStore struct {
	travellers map[string]storedTraveller // keyed by lower(email)
	sessions   map[string]storedSession   // keyed by string(tokenHash)
	nextID     int
	failWith   error
	touched    []string

	// clock is what `sessions.last_used_at DEFAULT now()` is in the fake, and
	// it is not decoration: DEC-100 decides whether to touch by comparing the
	// STORED value against the clock, so a fake that left LastUsedAt at the
	// zero time would report every session as infinitely stale and touch on
	// every request — which is exactly the behaviour the granularity leg
	// exists to refuse, passing.
	clock func() time.Time
}

func (f *fakeStore) now() time.Time {
	if f.clock == nil {
		return time.Time{}
	}
	return f.clock()
}

type storedTraveller struct {
	Traveller
	hash string
}

type storedSession struct {
	Session
	traveller Traveller
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		travellers: map[string]storedTraveller{},
		sessions:   map[string]storedSession{},
	}
}

func (f *fakeStore) CreateTraveller(_ context.Context, email, hash string) (Traveller, error) {
	if f.failWith != nil {
		return Traveller{}, f.failWith
	}
	key := strings.ToLower(email)
	if _, held := f.travellers[key]; held {
		return Traveller{}, ErrEmailTaken
	}
	f.nextID++
	tr := Traveller{ID: string(rune('a' + f.nextID)), Email: email}
	f.travellers[key] = storedTraveller{Traveller: tr, hash: hash}
	return tr, nil
}

func (f *fakeStore) TravellerByEmail(_ context.Context, email string) (Traveller, string, error) {
	if f.failWith != nil {
		return Traveller{}, "", f.failWith
	}
	held, ok := f.travellers[strings.ToLower(email)]
	if !ok {
		return Traveller{}, "", ErrNoTraveller
	}
	return held.Traveller, held.hash, nil
}

func (f *fakeStore) CreateSession(_ context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error) {
	if f.failWith != nil {
		return "", f.failWith
	}
	var owner Traveller
	for _, held := range f.travellers {
		if held.ID == travellerID {
			owner = held.Traveller
		}
	}
	id := "s" + string(rune('a'+len(f.sessions)))
	f.sessions[string(tokenHash)] = storedSession{
		Session: Session{
			ID: id, TravellerID: travellerID,
			TokenHash:  append([]byte(nil), tokenHash...),
			LastUsedAt: f.now(),
			ExpiresAt:  expiresAt,
		},
		traveller: owner,
	}
	return id, nil
}

func (f *fakeStore) SessionByTokenHash(_ context.Context, tokenHash []byte) (Session, Traveller, error) {
	if f.failWith != nil {
		return Session{}, Traveller{}, f.failWith
	}
	held, ok := f.sessions[string(tokenHash)]
	if !ok {
		return Session{}, Traveller{}, ErrNoSession
	}
	return held.Session, held.traveller, nil
}

func (f *fakeStore) TouchSession(_ context.Context, travellerID, sessionID string, at time.Time) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.touched = append(f.touched, travellerID+"/"+sessionID)
	for key, held := range f.sessions {
		if held.ID == sessionID {
			held.LastUsedAt = at
			f.sessions[key] = held
		}
	}
	return nil
}

// RevokeSession and RevokeEverySession mirror `UPDATE … WHERE revoked_at IS
// NULL`: a second revoke moves nothing and reports so, which is what makes the
// bool and the count mean anything.
func (f *fakeStore) RevokeSession(_ context.Context, travellerID string, tokenHash []byte) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	held, ok := f.sessions[string(tokenHash)]
	if !ok || held.TravellerID != travellerID || held.RevokedAt != nil {
		return false, nil
	}
	at := f.now()
	held.RevokedAt = &at
	f.sessions[string(tokenHash)] = held
	return true, nil
}

func (f *fakeStore) RevokeEverySession(_ context.Context, travellerID string) (int64, error) {
	if f.failWith != nil {
		return 0, f.failWith
	}
	var moved int64
	at := f.now()
	for key, held := range f.sessions {
		if held.TravellerID != travellerID || held.RevokedAt != nil {
			continue
		}
		held.RevokedAt = &at
		f.sessions[key] = held
		moved++
	}
	return moved, nil
}

// TravellerExists is DEC-86's question, and the fake answers it the way the
// table does: any row at all, whatever its address.
func (f *fakeStore) TravellerExists(context.Context) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	return len(f.travellers) > 0, nil
}

const testNow = "2027-10-12T09:00:00Z"

func at(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed
}

func newTestService(t *testing.T, store Store) *Service {
	t.Helper()
	return newServiceAtClock(t, store, func() time.Time { return at(t, testNow) })
}

// newServiceAtClock is newTestService with a clock a leg can move, AND IT HANDS
// THE SAME CLOCK TO THE FAKE STORE.
//
// That second half is load-bearing rather than tidy. `sessions.last_used_at`
// is `NOT NULL DEFAULT now()` in the schema, so a session is born fresh; a
// fake whose sessions are born at the zero time reports every one of them as
// infinitely stale, and DEC-100's granularity leg would pass against an
// implementation that touched on every single request.
func newServiceAtClock(t *testing.T, store Store, now func() time.Time) *Service {
	t.Helper()
	if fake, held := store.(*fakeStore); held {
		fake.clock = now
	}
	return &Service{
		Store:  store,
		Hasher: Argon2id{Params: cheap.Params},
		Now:    now,
	}
}

// === THE ORACLE ===

func TestAWrongPassphraseAndAnUnknownEmailAreTheSameAnswer(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, wrongPassphrase := s.SignIn(ctx, "matt@example.com", "not the passphrase")
	_, unknownEmail := s.SignIn(ctx, "nobody@example.com", "not the passphrase")

	if wrongPassphrase == nil || unknownEmail == nil {
		t.Fatalf("one of the two failures did not fail: wrong=%v unknown=%v", wrongPassphrase, unknownEmail)
	}
	if !errors.Is(wrongPassphrase, ErrBadCredentials) {
		t.Errorf("a wrong passphrase answered %v, want ErrBadCredentials", wrongPassphrase)
	}
	if !errors.Is(unknownEmail, ErrBadCredentials) {
		t.Errorf("an unknown email answered %v, want ErrBadCredentials.\n"+
			"    An address that is merely UNREGISTERED must not be distinguishable\n"+
			"    from one whose passphrase was mistyped — the difference is a list of\n"+
			"    who has an account here.", unknownEmail)
	}
	if wrongPassphrase.Error() != unknownEmail.Error() {
		t.Errorf("the two failures carry different text, so a handler that logs or\n"+
			"    renders it distinguishes them after all:\n  wrong:   %q\n  unknown: %q",
			wrongPassphrase.Error(), unknownEmail.Error())
	}
	if errors.Is(unknownEmail, ErrNoTraveller) {
		t.Errorf("the unknown-email failure still wraps ErrNoTraveller, so errors.Is\n" +
			"    tells the two apart even though the text matches")
	}
}

// The other half of the oracle, and the half a status-code assertion cannot
// see: an unknown address must cost the same WORK as a wrong passphrase, or
// the two are told apart with a stopwatch instead of a message.
func TestAnUnknownEmailStillVerifiesAgainstSomething(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	counted := &countingHasher{Hasher: Argon2id{Params: cheap.Params}}
	s.Hasher = counted

	if _, err := s.SignIn(context.Background(), "nobody@example.com", "a long enough passphrase"); err == nil {
		t.Fatalf("SignIn against an empty store succeeded")
	}
	if counted.verifies != 1 {
		t.Errorf("an unknown address cost %d Verify calls, want 1.\n"+
			"    Returning early skips ~64 MiB and tens of milliseconds of Argon2, and\n"+
			"    that difference is measurable from the outside on every attempt.",
			counted.verifies)
	}
}

type countingHasher struct {
	Hasher
	hashes   int
	verifies int
}

func (c *countingHasher) Hash(p string) (string, error) { c.hashes++; return c.Hasher.Hash(p) }
func (c *countingHasher) Verify(e, p string) (bool, error) {
	c.verifies++
	return c.Hasher.Verify(e, p)
}

// === REGISTER ===

func TestRegisterMintsNoSession(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	if _, err := s.Register(context.Background(), "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(store.sessions) != 0 {
		t.Errorf("Register created %d sessions, want 0.\n"+
			"    DEC-61: register does not sign you in, so the sign-in path is exercised\n"+
			"    from the FIRST launch rather than being a branch only the second reaches.",
			len(store.sessions))
	}
}

func TestRegisterLeavesTheNameUnset(t *testing.T) {
	tr, err := newTestService(t, newFakeStore()).
		Register(context.Background(), "matt@example.com", "a long enough passphrase")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tr.Name != nil {
		t.Errorf("Register set the name to %q; DEC-61 leaves it NULL until PATCH /v1/me,\n"+
			"    because two writers of one field drift", *tr.Name)
	}
}

func TestRegisterStoresAHashAndNeverThePassphrase(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	const passphrase = "a long enough passphrase"

	if _, err := s.Register(context.Background(), "matt@example.com", passphrase); err != nil {
		t.Fatalf("Register: %v", err)
	}
	held := store.travellers["matt@example.com"]
	if held.hash == passphrase {
		t.Fatalf("the passphrase was stored in the clear")
	}
	if strings.Contains(held.hash, passphrase) {
		t.Fatalf("the stored value contains the passphrase: %q", held.hash)
	}
	if !strings.HasPrefix(held.hash, "$argon2id$") {
		t.Errorf("the stored value is not an argon2id encoding: %q", held.hash)
	}
	ok, err := s.Hasher.Verify(held.hash, passphrase)
	if err != nil || !ok {
		t.Errorf("the stored hash does not verify the passphrase it was made from: (%v, %v)", ok, err)
	}
}

func TestRegisterStoresTheAddressAsTypedAndDoesNotLowercaseIt(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	tr, err := s.Register(context.Background(), "Matt.Marshall@Example.COM", "a long enough passphrase")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tr.Email != "Matt.Marshall@Example.COM" {
		t.Errorf("Register answered %q, want the address as typed.\n"+
			"    DEC-65: the unique index on lower(email) makes lowercasing on write\n"+
			"    unnecessary, and RFC 5321 says the LOCAL part is case-sensitive.", tr.Email)
	}
}

// DEC-86. REGISTRATION CLOSES AFTER THE FIRST TRAVELLER, AND THE ADDRESS HAS
// STOPPED MATTERING — which is the whole of the change and is why this leg
// replaces TestRegisterRefusesAnAddressThatIsAlreadyHeld rather than sitting
// beside it.
//
// The old leg asserted a second registration of ONE address was refused, and
// its name promised that the address was the reason. It is not the reason any
// more: a second registration of ANY address is refused, so a leg that only
// tried the same address would pass against a build that still handed a
// stranger an account.
//
// THE ORACLE SHRINKS RATHER THAN GROWS. Before, a 409 told a caller that THAT
// ADDRESS is registered here. Now it tells them the instance is in use, which
// the sign-in page already tells them. The two cases are asserted to answer
// the SAME sentinel for that reason.
func TestRegistrationClosesAfterTheFirstTraveller(t *testing.T) {
	for _, second := range []string{
		"matt@example.com",
		"MATT@EXAMPLE.COM",
		"a-total-stranger@example.com",
	} {
		t.Run(second, func(t *testing.T) {
			store := newFakeStore()
			s := newTestService(t, store)
			ctx := context.Background()

			if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
				t.Fatalf("first Register: %v", err)
			}
			_, err := s.Register(ctx, second, "a different long passphrase")
			if !errors.Is(err, ErrRegistrationClosed) {
				t.Errorf("registering %q second answered %v, want ErrRegistrationClosed.\n"+
					"    Ruling 3 is single-user. A stranger who registers on a deployed\n"+
					"    instance gets an authenticated account carrying a 600/min budget\n"+
					"    and, from R6, a `?photos=delete`.", second, err)
			}
		})
	}
}

// AND THE REFUSAL COSTS NO ARGON2, which is why the rule is asked of the store
// rather than left to the INSERT to answer.
//
// Hashing is 64 MiB and tens of milliseconds by design (DEC-08). A closed
// instance that pays that on every attempt is an unauthenticated memory sink
// behind a route on which nobody can ever succeed — DEC-48's per-address
// ceiling bounds it, and a bound on work nobody should be doing at all is the
// weaker of the two guards.
//
// IT COUNTS CALLS RATHER THAN TIMING THEM, which is the shape PD-12 asks for
// everywhere in this package: a timing assertion on a machine running a test
// suite is a flake with a comment over it.
func TestAClosedRegistrationRefusesBeforeItHashesAnything(t *testing.T) {
	store := newFakeStore()
	counting := &countingHasher{Hasher: Argon2id{Params: cheap.Params}}
	s := newServiceAtClock(t, store, func() time.Time { return at(t, testNow) })
	s.Hasher = counting
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if counting.hashes != 1 {
		t.Fatalf("the first Register hashed %d times, want 1 — the fixture is wrong", counting.hashes)
	}

	for range 5 {
		if _, err := s.Register(ctx, "stranger@example.com", "a long enough passphrase"); !errors.Is(err, ErrRegistrationClosed) {
			t.Fatalf("Register on a closed instance = %v", err)
		}
	}
	if counting.hashes != 1 {
		t.Errorf("five refused registrations hashed %d more times, want 0.\n"+
			"    Argon2 is 64 MiB a call. A refusal taken after the hash is an\n"+
			"    unauthenticated memory sink behind a route nobody can succeed on.",
			counting.hashes-1)
	}
}

func TestRegisterRefusesTheFieldsItCannotUse(t *testing.T) {
	s := newTestService(t, newFakeStore())
	ctx := context.Background()

	cases := map[string]struct{ email, passphrase, field string }{
		"no email":           {"", "a long enough passphrase", "email"},
		"whitespace email":   {"   ", "a long enough passphrase", "email"},
		"no at sign":         {"matt.example.com", "a long enough passphrase", "email"},
		"two at signs":       {"matt@ex@ample.com", "a long enough passphrase", "email"},
		"no local part":      {"@example.com", "a long enough passphrase", "email"},
		"no domain":          {"matt@", "a long enough passphrase", "email"},
		"no dot in domain":   {"matt@example", "a long enough passphrase", "email"},
		"a space inside":     {"matt marshall@example.com", "a long enough passphrase", "email"},
		"longer than 254":    {strings.Repeat("a", 250) + "@example.com", "a long enough passphrase", "email"},
		"no passphrase":      {"matt@example.com", "", "passphrase"},
		"a short passphrase": {"matt@example.com", "1234567", "passphrase"},
		"an absurd passphrase": {"matt@example.com",
			strings.Repeat("x", int(MaxPassphraseBytes)+1), "passphrase"},
	}
	for name, c := range cases {
		_, err := s.Register(ctx, c.email, c.passphrase)
		var invalid InvalidFieldError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: Register answered %v, want an InvalidFieldError", name, err)
			continue
		}
		if invalid.Field != c.field {
			t.Errorf("%s: the field is %q, want %q", name, invalid.Field, c.field)
		}
	}
}

func TestRegisterNeverReachesTheStoreWithAFieldItRefused(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	if _, err := s.Register(context.Background(), "not an address", "a long enough passphrase"); err == nil {
		t.Fatalf("Register accepted a malformed address")
	}
	if len(store.travellers) != 0 {
		t.Errorf("a refused registration reached the store: %v", store.travellers)
	}
}

// === SIGN IN ===

func TestSignInAnswersATokenAndStoresOnlyItsHash(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	issued, err := s.SignIn(ctx, "matt@example.com", "a long enough passphrase")
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if issued.Token == "" {
		t.Fatalf("SignIn answered no token")
	}
	if _, err := HashToken(issued.Token); err != nil {
		t.Errorf("the token is not the shape this package mints: %v", err)
	}
	for key, held := range store.sessions {
		if key == issued.Token || string(held.TokenHash) == issued.Token {
			t.Errorf("the PLAINTEXT token reached the store")
		}
	}
	if len(store.sessions) != 1 {
		t.Fatalf("SignIn created %d sessions, want 1", len(store.sessions))
	}
}

func TestSignInResolvesAnAddressWhateverItsCasing(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	if _, err := s.Register(ctx, "A@B.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, spelling := range []string{"a@b.com", "A@B.COM", "a@B.Com", "A@B.com"} {
		if _, err := s.SignIn(ctx, spelling, "a long enough passphrase"); err != nil {
			t.Errorf("SignIn as %q against a registration of A@B.com answered %v", spelling, err)
		}
	}
}

func TestTheSessionExpiresATTLFromNow(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	issued, err := s.SignIn(ctx, "matt@example.com", "a long enough passphrase")
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	want := at(t, testNow).Add(DefaultSessionTTL)
	if !issued.ExpiresAt.Equal(want) {
		t.Errorf("the session expires at %s, want %s (now + DefaultSessionTTL)", issued.ExpiresAt, want)
	}
	for _, held := range store.sessions {
		if !held.ExpiresAt.Equal(want) {
			t.Errorf("the STORED expiry is %s, want %s", held.ExpiresAt, want)
		}
	}
}

// The assertion is on the ERROR TYPE and not merely on "an error", because an
// empty store answers an error for every address anyway: a leg that only
// checked `err == nil` would pass against a SignIn that validates nothing and
// would go on passing after somebody deleted the checks.
func TestSignInRefusesTheFieldsItCannotUse(t *testing.T) {
	s := newTestService(t, newFakeStore())
	ctx := context.Background()

	cases := map[string]struct{ email, passphrase, field string }{
		"no email":           {"", "a long enough passphrase", "email"},
		"not an address":     {"matt.example.com", "a long enough passphrase", "email"},
		"longer than 254":    {strings.Repeat("a", 250) + "@example.com", "a long enough passphrase", "email"},
		"no passphrase":      {"matt@example.com", "", "passphrase"},
		"a short passphrase": {"matt@example.com", "1234567", "passphrase"},
		"an absurd passphrase": {"matt@example.com",
			strings.Repeat("x", int(MaxPassphraseBytes)+1), "passphrase"},
	}
	for name, c := range cases {
		_, err := s.SignIn(ctx, c.email, c.passphrase)
		var invalid InvalidFieldError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: SignIn answered %v, want an InvalidFieldError", name, err)
			continue
		}
		if invalid.Field != c.field {
			t.Errorf("%s: the field is %q, want %q", name, invalid.Field, c.field)
		}
	}
}

// === AUTHENTICATE ===

func signedIn(t *testing.T) (*Service, *fakeStore, Issued) {
	t.Helper()
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	issued, err := s.SignIn(ctx, "matt@example.com", "a long enough passphrase")
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	return s, store, issued
}

func TestAuthenticateResolvesTheTraveller(t *testing.T) {
	s, _, issued := signedIn(t)

	tr, err := s.Authenticate(context.Background(), issued.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if tr.Email != "matt@example.com" {
		t.Errorf("Authenticate answered %q", tr.Email)
	}
}

// DEC-100. THE TOUCH IS PER TouchInterval AND NOT PER REQUEST, and this leg
// asserts BOTH directions because only the pair is falsifiable: an
// implementation that never touches passes the first half, and one that
// touches every time passes the second.
//
// THE FRESH-SESSION HALF IS THE ONE THAT MEASURES THE CHANGE. A sign-in writes
// `last_used_at` — the column defaults to now() — so the very next request is
// against a session seconds old, which is the state EVERY request after the
// first is in for a phone that syncs. Before DEC-100 that state cost a
// transaction, an advisory lock, an existence read, an UPDATE and a commit:
// five of the nine round trips one 304 was measured to make.
func TestTheSessionTouchIsPerIntervalAndNotPerRequest(t *testing.T) {
	store := newFakeStore()
	clock := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return clock })
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com", "a long enough passphrase"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	issued, err := s.SignIn(ctx, "matt@example.com", "a long enough passphrase")
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if len(store.touched) != 0 {
		t.Fatalf("signing in touched %d sessions; the fixture is wrong", len(store.touched))
	}

	// A phone that syncs: many requests, all inside the interval.
	clock = clock.Add(TouchInterval - time.Second)
	for range 20 {
		if _, err := s.Authenticate(ctx, issued.Token); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
	}
	if len(store.touched) != 0 {
		t.Errorf("twenty requests inside TouchInterval wrote last_used_at %d times, want 0.\n"+
			"    Measured with pg_stat_statements around exactly one 304: stamping this\n"+
			"    timestamp was FIVE of NINE database round trips, against 0.176ms of\n"+
			"    total server work.", len(store.touched))
	}

	// And it is not "never": once the stored value is stale, one write.
	clock = clock.Add(2 * time.Second)
	for range 20 {
		if _, err := s.Authenticate(ctx, issued.Token); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
	}
	if len(store.touched) != 1 {
		t.Errorf("twenty requests spanning TouchInterval wrote last_used_at %d times, want 1.\n"+
			"    A touch that never happens is not granular, it is absent — and the\n"+
			"    column would then say a live session was last used at sign-in.",
			len(store.touched))
	}
}

func TestAuthenticateRefusesEveryShapeOfWrongToken(t *testing.T) {
	s, _, issued := signedIn(t)
	ctx := context.Background()

	other, _, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	// THE "one character changed" CASE HAS TO CHANGE A CHARACTER, and spelling
	// it as a fixed letter did not. `"Z" + issued.Token[1:]` IS the issued
	// token whenever the token already begins with Z, and the leg then asserts
	// that a VALID token is refused — which fails. base64url's alphabet is 64
	// characters, so it fired on 1.620% of runs, measured over 64,000 tokens
	// against the 1/64 = 1.5625% the alphabet predicts. It fired twice during
	// R6's mutation runs, and `make check` is the only gate this project has.
	//
	// Derive the substitute from the character it replaces instead of naming
	// one, so the case cannot degenerate no matter what the token starts with.
	changedFirst := "Z"
	if strings.HasPrefix(issued.Token, "Z") {
		changedFirst = "Y"
	}
	changed := changedFirst + issued.Token[1:]
	if changed == issued.Token {
		t.Fatal("the \"one character changed\" case did not change a character")
	}

	for name, token := range map[string]string{
		"empty":                   "",
		"not base64":              "!!!!",
		"the right shape, unheld": other,
		"one character removed":   issued.Token[:len(issued.Token)-1],
		"one character changed":   changed,
		"padded":                  issued.Token + "=",
		"the whole Authorization": "Bearer " + issued.Token,
	} {
		if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: Authenticate answered %v, want ErrNoSession", name, err)
		}
	}
}

func TestAuthenticateRefusesASessionThatHasExpired(t *testing.T) {
	s, store, issued := signedIn(t)

	s.Now = func() time.Time { return at(t, testNow).Add(DefaultSessionTTL) }
	if _, err := s.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("Authenticate at exactly expires_at answered %v, want ErrNoSession", err)
	}
	if len(store.touched) != 0 {
		t.Errorf("an expired session was touched, which keeps a dead credential looking alive")
	}
}

func TestAuthenticateRefusesASessionThatWasRevoked(t *testing.T) {
	s, store, issued := signedIn(t)

	hash, err := HashToken(issued.Token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	revoked := at(t, testNow).Add(-time.Minute)
	held := store.sessions[string(hash)]
	held.RevokedAt = &revoked
	store.sessions[string(hash)] = held

	if _, err := s.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("Authenticate on a revoked session answered %v, want ErrNoSession", err)
	}
}

// The store finds the row by an indexed equality on token_hash, which is not a
// constant-time operation and cannot be. Spec L24 asks for the COMPARISON to
// be constant-time all the same, so the service re-checks what came back
// against what was presented — which is also the only thing standing between a
// store that returns the wrong row and a session handed to the wrong person.
func TestAuthenticateRefusesARowWhoseHashDoesNotMatchWhatWasPresented(t *testing.T) {
	s, store, issued := signedIn(t)

	hash, err := HashToken(issued.Token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	held := store.sessions[string(hash)]
	held.TokenHash[0] ^= 0xff
	store.sessions[string(hash)] = held

	if _, err := s.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("Authenticate answered %v for a row whose token_hash is not the one presented, want ErrNoSession", err)
	}
}

func TestABusyGateIsReportedAndNotSwallowed(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	g, err := NewGate(1)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	g.Enter()
	s.Hasher = Capped{Hasher: Argon2id{Params: cheap.Params}, Gate: g}

	if _, err := s.Register(context.Background(), "matt@example.com", "a long enough passphrase"); !errors.Is(err, ErrBusy) {
		t.Errorf("Register under a full gate answered %v, want ErrBusy", err)
	}
	if _, err := s.SignIn(context.Background(), "matt@example.com", "a long enough passphrase"); !errors.Is(err, ErrBusy) {
		t.Errorf("SignIn under a full gate answered %v, want ErrBusy.\n"+
			"    A busy gate must not be reported as bad credentials: it is not the\n"+
			"    traveller's mistake and retrying with a different passphrase is the\n"+
			"    wrong advice.", err)
	}
}

func TestAStoreFailureIsNotReportedAsBadCredentials(t *testing.T) {
	store := newFakeStore()
	store.failWith = errors.New("the database went away")
	s := newTestService(t, store)

	_, err := s.SignIn(context.Background(), "matt@example.com", "a long enough passphrase")
	if errors.Is(err, ErrBadCredentials) {
		t.Errorf("a store failure was reported as bad credentials, which tells somebody\n" +
			"    their passphrase is wrong when it is not")
	}
	if err == nil {
		t.Errorf("a store failure was swallowed")
	}
}
