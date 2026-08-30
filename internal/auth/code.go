// Sign-in codes: six decimal digits, mailed, typed into the app.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"
)

// travellerRe is deliberately narrow, and it is this package's own rather
// than media's: auth must not depend on the media layer to validate a uuid.
var travellerRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	CodeDigits = 6

	codeSpace = 1000000

	CodeTTL = 10 * time.Minute

	MaxCodeAttempts = 5

	CodeRequestInterval = 60 * time.Second
)

// ErrMalformedCode is a presented code that is not the shape this package
// mints.
var ErrMalformedCode = errors.New("auth: that is not the shape of a sign-in code")

// ErrNoCode is a traveller who holds no live sign-in code.
var ErrNoCode = errors.New("auth: that traveller holds no sign-in code")

// SignInCode is the row, as the service reads it.
type SignInCode struct {
	Hash      []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
	Attempts  int
}

// NewCode mints one for a traveller.
func NewCode(travellerID string) (plaintext string, hash []byte, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "", nil, fmt.Errorf("auth: drawing a sign-in code: %w", err)
	}
	plaintext = fmt.Sprintf("%0*d", CodeDigits, n.Int64())
	hash, err = HashCode(travellerID, plaintext)
	if err != nil {
		return "", nil, err
	}
	return plaintext, hash, nil
}

// HashCode turns a presented code back into the digest a row is found by.
func HashCode(travellerID, plaintext string) ([]byte, error) {
	if !travellerRe.MatchString(travellerID) {
		return nil, fmt.Errorf("auth: %q is not a traveller uuid, and the traveller is the salt", travellerID)
	}
	if len(plaintext) != CodeDigits {
		return nil, fmt.Errorf("%w: %d characters, want %d", ErrMalformedCode, len(plaintext), CodeDigits)
	}
	for i := 0; i < len(plaintext); i++ {
		if plaintext[i] < '0' || plaintext[i] > '9' {
			return nil, fmt.Errorf("%w: %q is not decimal digits", ErrMalformedCode, plaintext)
		}
	}
	sum := sha256.Sum256([]byte(travellerID + ":" + plaintext))
	return sum[:], nil
}

// RequestCode mints a sign-in code for an address and answers the plaintext
// to mail, the traveller to mail it to, and whether there is anybody to mail.
func (s *Service) RequestCode(ctx context.Context, email string) (code string, tr Traveller, ok bool, err error) {
	if err := checkEmail(email); err != nil {
		return "", Traveller{}, false, err
	}
	tr, err = s.Store.TravellerByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNoTraveller):
		return "", Traveller{}, false, nil
	case err != nil:
		return "", Traveller{}, false, err
	}

	switch held, err := s.Store.CodeFor(ctx, tr.ID); {
	case errors.Is(err, ErrNoCode):
	case err != nil:
		return "", Traveller{}, false, err
	case s.now().Sub(held.IssuedAt) < CodeRequestInterval:
		return "", Traveller{}, false, nil
	}

	code, hash, err := NewCode(tr.ID)
	if err != nil {
		return "", Traveller{}, false, err
	}
	if err := s.Store.IssueCode(ctx, tr.ID, hash, s.now().Add(CodeTTL)); err != nil {
		return "", Traveller{}, false, err
	}
	return code, tr, true, nil
}

// SignInWithCode exchanges a mailed code for a session.
func (s *Service) SignInWithCode(ctx context.Context, email, code string) (Issued, error) {
	if err := checkEmail(email); err != nil {
		return Issued{}, err
	}
	tr, err := s.Store.TravellerByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNoTraveller):
		return Issued{}, ErrBadCredentials
	case err != nil:
		return Issued{}, err
	}

	held, err := s.Store.CodeFor(ctx, tr.ID)
	switch {
	case errors.Is(err, ErrNoCode):
		return Issued{}, ErrBadCredentials
	case err != nil:
		return Issued{}, err
	}

	if !s.now().Before(held.ExpiresAt) || held.Attempts >= MaxCodeAttempts {
		if err := s.Store.BurnCode(ctx, tr.ID); err != nil {
			return Issued{}, err
		}
		return Issued{}, ErrBadCredentials
	}

	presented, hashErr := HashCode(tr.ID, code)
	if hashErr != nil || !SameHash(presented, held.Hash) {
		n, err := s.Store.CountAttempt(ctx, tr.ID)
		if err != nil && !errors.Is(err, ErrNoCode) {
			return Issued{}, err
		}
		if n >= MaxCodeAttempts {
			if err := s.Store.BurnCode(ctx, tr.ID); err != nil {
				return Issued{}, err
			}
		}
		return Issued{}, ErrBadCredentials
	}

	if err := s.Store.BurnCode(ctx, tr.ID); err != nil {
		return Issued{}, err
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
