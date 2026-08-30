// Register, sign in, and resolve a bearer token to a traveller.
package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The three limits, and what each is for.
const (
	MaxEmailBytes      = 254
	MinPassphraseBytes = 8
	MaxPassphraseBytes = 1024
)

// DefaultSessionTTL is untuned in the same sense's Argon2 parameters are:
// nothing has measured it against anything.
const DefaultSessionTTL = 30 * 24 * time.Hour

// TouchInterval is how stale `last_used_at` has to be before a request writes
// it, and the whole of what it buys is round trips.
const TouchInterval = 5 * time.Minute

// The sentinels.
var (
	ErrEmailTaken = errors.New("auth: that address is already registered")

	ErrRegistrationClosed = errors.New("auth: this log already has a traveller")

	ErrNoTraveller = errors.New("auth: no traveller with that address")

	ErrBadCredentials = errors.New("auth: that address and passphrase do not match")

	ErrNoSession = errors.New("auth: that is not a live session")
)

// InvalidFieldError is what the one additive key, `field`, is built from.
type InvalidFieldError struct{ Field, Why string }

func (e InvalidFieldError) Error() string { return "auth: " + e.Field + ": " + e.Why }

// Traveller is the part of a traveller this package knows.
type Traveller struct {
	ID    string
	Email string
	Name  *string
}

// Session is a row of `sessions`.
type Session struct {
	ID          string
	TravellerID string
	TokenHash   []byte
	LastUsedAt  time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// Issued is what a sign-in hands back.
type Issued struct {
	Token     string
	ExpiresAt time.Time
	Traveller Traveller
}

// Store is the storage this package needs, declared here and satisfied by
// internal/postgres.
type Store interface {
	CreateTraveller(ctx context.Context, email string) (Traveller, error)
	TravellerByEmail(ctx context.Context, email string) (Traveller, error)
	CreateSession(ctx context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error)
	SessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, Traveller, error)

	TouchSession(ctx context.Context, travellerID, sessionID string, at time.Time) error

	RevokeSession(ctx context.Context, travellerID string, tokenHash []byte) (bool, error)

	RevokeEverySession(ctx context.Context, travellerID string) (int64, error)

	TravellerExists(ctx context.Context) (bool, error)

	MintInvite(ctx context.Context, hash []byte, note string) error

	ClaimInvite(ctx context.Context, hash []byte, travellerID string) error

	IssueCode(ctx context.Context, travellerID string, hash []byte, expiresAt time.Time) error

	CodeFor(ctx context.Context, travellerID string) (SignInCode, error)

	CountAttempt(ctx context.Context, travellerID string) (int, error)

	BurnCode(ctx context.Context, travellerID string) error
}

// Service is the one real addition: the business rules, so a handler
// translates HTTP and nothing more.
type Service struct {
	Store Store

	Now func() time.Time

	SessionTTL time.Duration
}

// dummyHash is a real argon2id encoding, at the shipped parameters, of a
// passphrase nobody holds.

// emailPattern is deliberately small.
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

// Register creates a traveller and mints no session, and refuses once this
// log has one.
func (s *Service) Register(ctx context.Context, email string) (Traveller, error) {
	if err := checkEmail(email); err != nil {
		return Traveller{}, err
	}
	return s.Store.CreateTraveller(ctx, email)
}

// RegisterWithInvite is Register behind a single-use invite. The claim comes
// after the create, because the claim records who spent it.
func (s *Service) RegisterWithInvite(ctx context.Context, email, invite string) (Traveller, error) {
	if err := checkEmail(email); err != nil {
		return Traveller{}, err
	}
	if strings.TrimSpace(invite) == "" {
		return Traveller{}, InvalidFieldError{Field: "invite", Why: "an invite is required"}
	}

	tr, err := s.Register(ctx, email)
	if err != nil {
		return Traveller{}, err
	}
	if err := s.Store.ClaimInvite(ctx, HashInvite(invite), tr.ID); err != nil {
		return Traveller{}, err
	}
	return tr, nil
}

// Authenticate resolves a bearer token, and writes `last_used_at` at most
// once per TouchInterval.
func (s *Service) Authenticate(ctx context.Context, token string) (Traveller, error) {
	hash, err := HashToken(token)
	if err != nil {
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

	if now.Sub(session.LastUsedAt) >= TouchInterval {
		if err := s.Store.TouchSession(ctx, tr.ID, session.ID, now); err != nil {
			return Traveller{}, err
		}
	}
	return tr, nil
}

// RevokeSession kills the token the caller presented.
func (s *Service) RevokeSession(ctx context.Context, travellerID, token string) (bool, error) {
	hash, err := HashToken(token)
	if err != nil {
		return false, nil
	}
	return s.Store.RevokeSession(ctx, travellerID, hash)
}

// RevokeEverySession is the sibling, and the security lens the argument for it
// is short and right.
func (s *Service) RevokeEverySession(ctx context.Context, travellerID string) (int64, error) {
	return s.Store.RevokeEverySession(ctx, travellerID)
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
