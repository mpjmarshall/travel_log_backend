// Register and sign-in as behaviour, against a fake store.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeStore is the Store interface with a map behind it.
type fakeStore struct {
	codes      map[string]*SignInCode
	invites    map[string]bool
	travellers map[string]storedTraveller // keyed by lower(email)
	sessions   map[string]storedSession   // keyed by string(tokenHash)
	nextID     int
	failWith   error
	touched    []string

	clock func() time.Time
}

// fakeUUID is a valid uuid shape that encodes its counter, so a failure
// message still says which traveller it was about.
func fakeUUID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func (f *fakeStore) now() time.Time {
	if f.clock == nil {
		return time.Time{}
	}
	return f.clock()
}

type storedTraveller struct {
	Traveller
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

func (f *fakeStore) CreateTraveller(_ context.Context, email string) (Traveller, error) {
	if f.failWith != nil {
		return Traveller{}, f.failWith
	}
	key := strings.ToLower(email)
	if _, held := f.travellers[key]; held {
		return Traveller{}, ErrEmailTaken
	}
	f.nextID++
	tr := Traveller{ID: fakeUUID(f.nextID), Email: email}
	f.travellers[key] = storedTraveller{Traveller: tr}
	return tr, nil
}

func (f *fakeStore) TravellerByEmail(_ context.Context, email string) (Traveller, error) {
	if f.failWith != nil {
		return Traveller{}, f.failWith
	}
	held, ok := f.travellers[strings.ToLower(email)]
	if !ok {
		return Traveller{}, ErrNoTraveller
	}
	return held.Traveller, nil
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

func (f *fakeStore) TouchSession(_ context.Context, travellerID, sessionID string, at, expiresAt time.Time) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.touched = append(f.touched, travellerID+"/"+sessionID)
	for key, held := range f.sessions {
		if held.ID == sessionID {
			held.LastUsedAt = at
			held.ExpiresAt = expiresAt
			f.sessions[key] = held
		}
	}
	return nil
}

// RevokeSession and RevokeEverySession mirror `update … where revoked_at is
// NULL`.
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

// TravellerExists is the question, and the fake answers it the way the table
// does: any row at all, whatever its address.
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

// newServiceAtClock is newTestService with a clock a leg can move, and it
// hands the same clock to the fake store.
func newServiceAtClock(t *testing.T, store Store, now func() time.Time) *Service {
	t.Helper()
	if fake, held := store.(*fakeStore); held {
		fake.clock = now
	}
	return &Service{
		Store: store,
		Now:   now,
	}
}

func TestRegisterMintsNoSession(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	if _, err := s.Register(context.Background(), "matt@example.com"); err != nil {
		t.Fatal("Register")
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
		Register(context.Background(), "matt@example.com")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if tr.Name != nil {
		t.Errorf("Register set the name to %q; DEC-61 leaves it NULL until PATCH /v1/me,\n"+
			"    because two writers of one field drift", *tr.Name)
	}
}

func TestRegisterStoresTheAddressAsTypedAndDoesNotLowercaseIt(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	tr, err := s.Register(context.Background(), "Matt.Marshall@Example.COM")
	if err != nil {
		t.Fatal("Register")
	}
	if tr.Email != "Matt.Marshall@Example.COM" {
		t.Errorf("Register answered %q, want the address as typed.\n"+
			"    DEC-65: the unique index on lower(email) makes lowercasing on write\n"+
			"    unnecessary, and RFC 5321 says the LOCAL part is case-sensitive.", tr.Email)
	}
}

func TestRegisterWithInviteRefusesWithoutOne(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	_, err := s.RegisterWithInvite(ctx, "matt@example.com", "")
	var invalid InvalidFieldError
	if !errors.As(err, &invalid) || invalid.Field != "invite" {
		t.Fatalf("answered %v, want a 422 naming invite", err)
	}
}

func TestRegisterWithInviteRefusesASpentOne(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	if err := store.MintInvite(ctx, HashInvite("ONCE"), ""); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RegisterWithInvite(ctx, "first@example.com", "ONCE"); err != nil {
		t.Fatalf("the first use: %v", err)
	}
	if _, err := s.RegisterWithInvite(ctx, "second@example.com", "ONCE"); !errors.Is(err, ErrInviteSpent) {
		t.Fatalf("the second use answered %v, want ErrInviteSpent", err)
	}
}

func TestTheAddressIsCheckedBeforeTheInvite(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)

	_, err := s.RegisterWithInvite(context.Background(), "not an address", "")
	var invalid InvalidFieldError
	if !errors.As(err, &invalid) || invalid.Field != "email" {
		t.Fatalf("answered %v, want a 422 naming email: a caller with two bad "+
			"fields must be sent to fix the address, not the invite", err)
	}
}

func TestRegisterRefusesTheFieldsItCannotUse(t *testing.T) {
	s := newTestService(t, newFakeStore())
	ctx := context.Background()

	cases := map[string]struct{ email, passphrase, field string }{
		"no email":         {"", "a long enough passphrase", "email"},
		"whitespace email": {"   ", "a long enough passphrase", "email"},
		"no at sign":       {"matt.example.com", "a long enough passphrase", "email"},
		"two at signs":     {"matt@ex@ample.com", "a long enough passphrase", "email"},
		"no local part":    {"@example.com", "a long enough passphrase", "email"},
		"no domain":        {"matt@", "a long enough passphrase", "email"},
		"no dot in domain": {"matt@example", "a long enough passphrase", "email"},
		"a space inside":   {"matt marshall@example.com", "a long enough passphrase", "email"},
		"longer than 254":  {strings.Repeat("a", 250) + "@example.com", "a long enough passphrase", "email"},
	}
	for name, c := range cases {
		_, err := s.Register(ctx, c.email)
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

	if _, err := s.Register(context.Background(), "not an address"); err == nil {
		t.Fatalf("Register accepted a malformed address")
	}
	if len(store.travellers) != 0 {
		t.Errorf("a refused registration reached the store: %v", store.travellers)
	}
}

func signedIn(t *testing.T) (*Service, *fakeStore, Issued) {
	t.Helper()
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()
	if _, err := s.Register(ctx, "matt@example.com"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	code, _, _, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	issued, err := s.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatalf("SignInWithCode: %v", err)
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

func TestAuthenticateRefusesEveryShapeOfWrongToken(t *testing.T) {
	s, _, issued := signedIn(t)
	ctx := context.Background()

	other, _, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

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

// The store finds the row by an indexed equality on token_hash, which is not
// a constant-time operation and cannot be.
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

func (f *fakeStore) MintInvite(_ context.Context, hash []byte, _ string) error {
	if f.invites == nil {
		f.invites = map[string]bool{}
	}
	f.invites[string(hash)] = false
	return nil
}

func (f *fakeStore) ClaimInvite(_ context.Context, hash []byte, _ string) error {
	spent, held := f.invites[string(hash)]
	if !held || spent {
		return ErrInviteSpent
	}
	f.invites[string(hash)] = true
	return nil
}

func TestTheSessionExpiresATTLFromNow(t *testing.T) {
	store := newFakeStore()
	s := newTestService(t, store)
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	code, _, _, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	issued, err := s.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatalf("SignInWithCode: %v", err)
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

// The assertion is on the error type and not merely on "an error", because an
// empty store answers an error for every address anyway.
func TestTheSessionTouchIsPerIntervalAndNotPerRequest(t *testing.T) {
	store := newFakeStore()
	clock := at(t, testNow)
	s := newServiceAtClock(t, store, func() time.Time { return clock })
	ctx := context.Background()

	if _, err := s.Register(ctx, "matt@example.com"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	code, _, _, err := s.RequestCode(ctx, "matt@example.com")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	issued, err := s.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatalf("SignInWithCode: %v", err)
	}
	if len(store.touched) != 0 {
		t.Fatalf("signing in touched %d sessions; the fixture is wrong", len(store.touched))
	}

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

func TestAStoreFailureIsNotReportedAsBadCredentials(t *testing.T) {
	store := newFakeStore()
	store.failWith = errors.New("the database went away")
	s := newTestService(t, store)

	_, err := s.SignInWithCode(context.Background(), "matt@example.com", "123456")
	if errors.Is(err, ErrBadCredentials) {
		t.Errorf("a store failure was reported as bad credentials, which tells somebody\n" +
			"    their code is wrong when it is not")
	}
	if err == nil {
		t.Errorf("a store failure was swallowed")
	}
}
