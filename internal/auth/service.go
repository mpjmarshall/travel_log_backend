// Register, sign in, and resolve a bearer token to a traveller.
//
// THE STORE IS AN INTERFACE THIS PACKAGE DECLARES AND internal/postgres
// SATISFIES (DEC-62): the business rules own the contract and the storage
// implementation meets it. That is not layering for its own sake here — it is
// what lets the enumeration-oracle leg, which is the one most easily lost, run
// with no database at all rather than behind a TEST_DATABASE_URL skip.
//
// NOTHING IN THIS PACKAGE LOWERCASES AN EMAIL ADDRESS, and that is DEC-65
// rather than an omission. The unique index is on `lower(email)` and the
// lookup is `WHERE lower(email) = lower($1)`, so the case-folding happens in
// SQL, in one place, enforced by the database. DEC-60's two-call-site rule is
// superseded: an application rule that two sites must remember is exactly the
// rule one of them eventually forgets. The address is stored AS TYPED, because
// RFC 5321 makes the local part case-sensitive even though no real provider
// treats it so.
//
// THERE IS NO ENUMERATION ORACLE ON SIGN-IN, IN EITHER OF ITS TWO FORMS.
// The obvious one is the message, and both branches answer the same sentinel
// with the same text. The other is the clock: returning early on an unknown
// address skips ~64 MiB and tens of milliseconds of Argon2, which is
// measurable from the outside on every attempt — so an unknown address is
// verified against a real hash of a passphrase nobody holds, and pays exactly
// what a wrong passphrase pays.
//
// REGISTER IS DELIBERATELY NOT LIKE THAT. DEC-60's surviving leg asks for a
// 409 on a second registration of the same address in different casing, so
// register tells you an address is taken BY DESIGN. The two are not
// inconsistent: an address's availability is a fact a sign-up form has to
// report, and whether somebody's passphrase was right is not.
package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// The three limits, and what each is for.
//
// 254 is the longest address SMTP can carry (RFC 5321's 256-octet path minus
// the angle brackets), so it is the wire's limit rather than a policy. The
// passphrase ceiling is a DoS bound and nothing else — Argon2's cost is
// independent of input length, but a 10 MB field is still 10 MB to read and
// copy. The floor of 8 IS a policy, it is this build's own, and it is the
// weakest thing on this list: it is written here so that raising it later is
// one constant and one leg.
const (
	MaxEmailBytes      = 254
	MinPassphraseBytes = 8
	MaxPassphraseBytes = 1024
)

// DefaultSessionTTL is UNTUNED in the same sense DEC-08's Argon2 parameters
// are: nothing has measured it against anything. Thirty days suits a phone
// that keeps its token in the platform keychain (DEC-45) and has no refresh
// flow, and there is no revocation UI yet — H1's 'Stop sharing' is about share
// links, not sessions.
const DefaultSessionTTL = 30 * 24 * time.Hour

// The sentinels. DEC-62: the sentinel is the domain's word and the wire code
// is httpapi's; this package names no HTTP status and imports no httpx.
var (
	// ErrEmailTaken is a second registration of one address, in any casing.
	// It comes from the unique index rather than from a check-then-insert.
	ErrEmailTaken = errors.New("auth: that address is already registered")

	// ErrNoTraveller is a lookup that matched nothing. IT MUST NOT REACH A
	// HANDLER FROM SIGN-IN — SignIn converts it to ErrBadCredentials, and
	// service_test.go asserts the conversion with errors.Is rather than only
	// with the text, because two sentinels with one string still tell a caller
	// which is which.
	ErrNoTraveller = errors.New("auth: no traveller with that address")

	// ErrBadCredentials is BOTH sign-in failures, and the whole of the answer.
	ErrBadCredentials = errors.New("auth: that address and passphrase do not match")

	// ErrNoSession is every reason a bearer token does not authenticate:
	// malformed, unheld, expired, revoked, or a row whose stored hash is not
	// the one presented. One answer, because the differences are the server's
	// business and telling them apart tells a holder of a stolen token which
	// kind of stolen it is.
	ErrNoSession = errors.New("auth: that is not a live session")
)

// InvalidFieldError is what DEC-12's one additive key, `field`, is built from.
type InvalidFieldError struct{ Field, Why string }

func (e InvalidFieldError) Error() string { return "auth: " + e.Field + ": " + e.Why }

// Traveller is the part of a traveller this package knows. Name is a pointer
// because DEC-61 leaves it NULL until PATCH /v1/me sets it, and the client's
// own model reads a missing name as "a log nobody has named yet" — which is a
// different thing from the empty string.
type Traveller struct {
	ID    string
	Email string
	Name  *string
}

// Session is a row of `sessions`. TokenHash is carried out of the store so the
// service can re-compare it in constant time: the row was FOUND by an indexed
// equality, which is not constant-time and cannot be, and spec L24 asks for
// the comparison all the same.
type Session struct {
	ID          string
	TravellerID string
	TokenHash   []byte
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// Issued is what a sign-in hands back. Token is the ONLY place the plaintext
// exists after NewToken; it goes into one response body and is never stored.
type Issued struct {
	Token     string
	ExpiresAt time.Time
	Traveller Traveller
}

// Store is the storage this package needs, declared here and satisfied by
// internal/postgres.
//
// TravellerByEmail's contract carries DEC-65 with it: the implementation must
// match on `lower(email) = lower($1)` or the functional unique index is
// skipped — and under a shadowing `lower` both sides collapse to one constant
// and the predicate matches EVERY row, so a forgotten rule is not a slow query
// but an unregistered address resolving to somebody else's traveller.
type Store interface {
	CreateTraveller(ctx context.Context, email, passphraseHash string) (Traveller, error)
	TravellerByEmail(ctx context.Context, email string) (Traveller, string, error)
	CreateSession(ctx context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error)
	SessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, Traveller, error)
	TouchSession(ctx context.Context, travellerID, sessionID string, at time.Time) error
}

// Service is DEC-62's one real addition: the business rules, so a handler
// translates HTTP and nothing more.
type Service struct {
	Store  Store
	Hasher Hasher

	// Now is the clock. A parameter for the reason the client's
	// logbookNowProvider is one: a session expiry asserted against the wall
	// clock is either a test that sleeps for thirty days or a test that
	// asserts nothing about expiry.
	Now func() time.Time

	// SessionTTL is DefaultSessionTTL when zero.
	SessionTTL time.Duration
}

// dummyHash is a real argon2id encoding, at the SHIPPED parameters, of a
// passphrase nobody holds. Verifying against it is what makes an unknown
// address cost what a wrong passphrase costs.
//
// IT IS COMPUTED EAGERLY, AT PACKAGE INIT, AND THAT IS THE DECISION. Computing
// it on first use would leave exactly one attempt per process paying an extra
// Argon2 call — a one-shot oracle rather than a per-attempt one, which is
// better and is still not none. The cost is one 64 MiB allocation and a few
// tens of milliseconds at boot, once.
//
// IT TRACKS DefaultParams RATHER THAN BEING A LITERAL, so that DEC-21's
// deferred tuning does not silently leave the decoy cheaper than the real
// thing — which would put the timing oracle back with every leg still green.
var dummyHash = func() string {
	encoded, err := (Argon2id{Params: DefaultParams}).Hash(
		"a passphrase nobody holds, hashed so an unknown address costs what a wrong one does")
	if err != nil {
		panic("auth: the decoy hash could not be computed: " + err.Error())
	}
	return encoded
}()

// emailPattern is deliberately small. Validating an address properly is
// impossible on paper and pointless in practice — the only proof an address
// works is mail arriving at it, and this build sends none. What it rejects is
// the shapes that are certainly wrong and would otherwise reach the database
// as a row nobody can ever sign in as.
var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

func (s *Service) ttl() time.Duration {
	if s.SessionTTL <= 0 {
		return DefaultSessionTTL
	}
	return s.SessionTTL
}

// Register creates a traveller and MINTS NO SESSION (DEC-61).
func (s *Service) Register(ctx context.Context, email, passphrase string) (Traveller, error) {
	if err := checkEmail(email); err != nil {
		return Traveller{}, err
	}
	if err := checkPassphrase(passphrase); err != nil {
		return Traveller{}, err
	}

	encoded, err := s.Hasher.Hash(passphrase)
	if err != nil {
		return Traveller{}, err
	}
	return s.Store.CreateTraveller(ctx, email, encoded)
}

// SignIn resolves an address in any casing and answers a fresh opaque token.
func (s *Service) SignIn(ctx context.Context, email, passphrase string) (Issued, error) {
	if err := checkEmail(email); err != nil {
		return Issued{}, err
	}
	if err := checkPassphrase(passphrase); err != nil {
		return Issued{}, err
	}

	tr, encoded, err := s.Store.TravellerByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNoTraveller):
		// The decoy. Its RESULT is discarded and cannot be anything but false;
		// its cost is the point. An error here is a real failure — a full
		// Argon2 gate, most likely — and is reported rather than folded into
		// the credentials answer, because a busy server is not a wrong
		// passphrase and "try again with a different one" is the wrong advice.
		if _, err := s.Hasher.Verify(dummyHash, passphrase); err != nil {
			return Issued{}, err
		}
		return Issued{}, ErrBadCredentials
	case err != nil:
		return Issued{}, err
	}

	ok, err := s.Hasher.Verify(encoded, passphrase)
	if err != nil {
		return Issued{}, err
	}
	if !ok {
		return Issued{}, ErrBadCredentials
	}

	plaintext, hash, err := NewToken()
	if err != nil {
		return Issued{}, err
	}
	expires := s.now().Add(s.ttl())
	if _, err := s.Store.CreateSession(ctx, tr.ID, hash, expires); err != nil {
		return Issued{}, err
	}
	return Issued{Token: plaintext, ExpiresAt: expires, Traveller: tr}, nil
}

// Authenticate resolves a bearer token, and writes `last_used_at`.
//
// THE TOUCH IS A WRITE THAT MUST NOT BUMP logbook_version, and the store is
// where that is enforced — through WithTravellerLock rather than
// WithTravellerTx. Count it, and the phone's whole cached log is invalidated
// on every authenticated request, so GET /v1/logbook never once answers 304 in
// real use.
func (s *Service) Authenticate(ctx context.Context, token string) (Traveller, error) {
	hash, err := HashToken(token)
	if err != nil {
		// A malformed credential and a well-formed one nobody holds are the
		// same answer: the difference is only useful to somebody guessing.
		return Traveller{}, ErrNoSession
	}

	session, tr, err := s.Store.SessionByTokenHash(ctx, hash)
	switch {
	case errors.Is(err, ErrNoSession):
		return Traveller{}, ErrNoSession
	case err != nil:
		return Traveller{}, err
	}

	if !SameHash(hash, session.TokenHash) {
		return Traveller{}, ErrNoSession
	}

	now := s.now()
	if session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
		return Traveller{}, ErrNoSession
	}

	if err := s.Store.TouchSession(ctx, tr.ID, session.ID, now); err != nil {
		return Traveller{}, err
	}
	return tr, nil
}

func checkEmail(email string) error {
	switch {
	case email == "":
		return InvalidFieldError{Field: "email", Why: "an address is required"}
	case len(email) > MaxEmailBytes:
		return InvalidFieldError{Field: "email",
			Why: fmt.Sprintf("%d bytes, and the longest address SMTP carries is %d", len(email), MaxEmailBytes)}
	case !emailPattern.MatchString(email):
		return InvalidFieldError{Field: "email", Why: "that is not the shape of an address"}
	}
	return nil
}

func checkPassphrase(passphrase string) error {
	switch {
	case len(passphrase) < MinPassphraseBytes:
		return InvalidFieldError{Field: "passphrase",
			Why: fmt.Sprintf("%d bytes, and this build asks for at least %d", len(passphrase), MinPassphraseBytes)}
	case len(passphrase) > MaxPassphraseBytes:
		return InvalidFieldError{Field: "passphrase",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(passphrase), MaxPassphraseBytes)}
	}
	return nil
}
