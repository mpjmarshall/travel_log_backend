// Sign-in codes: six decimal digits, mailed, typed into the app.
//
// WHY A TYPED CODE AND NOT A TAPPED LINK. A link has to land back inside the
// app signed in, which needs Universal Links, a verified domain and an
// apple-app-site-association file — and the client has no deep linking at all.
// A code works identically on every platform and has far less that can break
// silently, which matters when a broken sign-in means nobody can get in.
//
// THE DIGEST IS NOT WHAT PROTECTS THIS, AND SAYING OTHERWISE WOULD BE WORSE
// THAN NOT HASHING. A six-digit code is a million possibilities; anyone
// holding the column can exhaust it in under a second whatever it is hashed
// with. What protects a code is that it dies in ten minutes, dies on first
// use, and dies after five wrong guesses. The digest buys two narrower
// things: a code does not sit in plaintext in a backup or a log, and the salt
// below means the table an attacker builds is per traveller rather than once
// for everybody.
//
// SO THE ATTEMPT CAP IS LOAD-BEARING AND MUST NOT BE PER ADDRESS. The
// existing limiter keys on RemoteAddr, and an attacker rotating addresses is
// not slowed by it; five guesses against a million is safe, five guesses per
// IP is not a bound at all.
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
// The two are the same shape on purpose and neither is load-bearing alone.
var travellerRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	// CodeDigits is the length on the wire and in the mail, leading zeroes
	// included: 000000 is as valid a code as 999999.
	CodeDigits = 6

	// codeSpace is 10^CodeDigits, and the draw is over all of it.
	codeSpace = 1000000

	// CodeTTL is how long a mailed code is worth typing.
	CodeTTL = 10 * time.Minute

	// MaxCodeAttempts is how many wrong guesses a single code survives before
	// it is dead and a new one must be requested.
	MaxCodeAttempts = 5
)

// ErrMalformedCode is a presented code that is not the shape this package
// mints. Deliberately indistinguishable, to a caller, from a well-formed code
// that is simply wrong — the same call ErrMalformedToken makes.
var ErrMalformedCode = errors.New("auth: that is not the shape of a sign-in code")

// ErrNoCode is a traveller who holds no live sign-in code. It is not an
// error condition: it is the ordinary answer for somebody who has not asked
// for one, and for somebody whose code has just been burned.
var ErrNoCode = errors.New("auth: that traveller holds no sign-in code")

// SignInCode is the row, as the service reads it. The plaintext is not here
// and never was: NewCode returned it once, to be mailed.
type SignInCode struct {
	Hash      []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
	Attempts  int
}

// NewCode mints one for a traveller. It answers the plaintext for the mail
// and the digest for the column, and never both again.
//
// THE DRAW IS crypto/rand.Int AND NOT A MODULO. Reducing a uint32 into a
// million buckets biases the low 4,294,967,296 mod 1,000,000 of them, and
// that bias is far too small for any non-flaky test to catch — so it is
// prevented by construction rather than guarded by a leg that cannot fail.
// rand.Int rejects and redraws, and is uniform by documented contract.
func NewCode(travellerID string) (plaintext string, hash []byte, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "", nil, fmt.Errorf("auth: drawing a sign-in code: %w", err)
	}
	// %0*d, not %d: one draw in ten is below 100000 and would otherwise be
	// mailed five characters long.
	plaintext = fmt.Sprintf("%0*d", CodeDigits, n.Int64())
	hash, err = HashCode(travellerID, plaintext)
	if err != nil {
		return "", nil, err
	}
	return plaintext, hash, nil
}

// HashCode turns a presented code back into the digest a row is found by.
//
// The traveller is the salt, and it is required for the reason
// media.Address requires it: a caller passing an empty string would salt
// every traveller's codes identically, with nothing anywhere to say so.
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
//
// AN UNKNOWN ADDRESS ANSWERS NO ERROR AND NOTHING TO SEND, and that is the
// whole shape of this method. Erroring would make the route an account
// oracle: anybody could ask it which addresses have a log here. THE CALLER IS
// REQUIRED TO ANSWER THE SAME THING EITHER WAY — this method makes that
// possible and cannot enforce it.
func (s *Service) RequestCode(ctx context.Context, email string) (code string, tr Traveller, ok bool, err error) {
	// The shape of the address is a field error and not a credentials one,
	// the same call SignIn makes: it says nothing about who has an account.
	if err := checkEmail(email); err != nil {
		return "", Traveller{}, false, err
	}
	// TravellerByEmail resolves any casing itself, through DEC-65's index on
	// lower(email); there is no normalisation to do here.
	tr, _, err = s.Store.TravellerByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNoTraveller):
		return "", Traveller{}, false, nil
	case err != nil:
		return "", Traveller{}, false, err
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
//
// EVERY REFUSAL IS ErrBadCredentials AND THE CALLER CANNOT TELL THEM APART:
// an unknown address, a traveller who never asked, an expired code, a code
// whose budget is spent, and simply the wrong digits. Distinguishing any of
// them hands an attacker a step of the answer.
//
// THE CAP BURNS THE CODE RATHER THAN LOCKING THE ACCOUNT. After
// MaxCodeAttempts the row goes, so the right code stops working too and the
// traveller asks for another — which resets the budget. Locking the account
// instead would make the endpoint a way to lock anybody out of theirs.
func (s *Service) SignInWithCode(ctx context.Context, email, code string) (Issued, error) {
	if err := checkEmail(email); err != nil {
		return Issued{}, err
	}
	tr, _, err := s.Store.TravellerByEmail(ctx, email)
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

	// Expiry and an exhausted budget are both "this code is finished", and
	// both take the row with them so the next guess finds nothing.
	if !s.now().Before(held.ExpiresAt) || held.Attempts >= MaxCodeAttempts {
		if err := s.Store.BurnCode(ctx, tr.ID); err != nil {
			return Issued{}, err
		}
		return Issued{}, ErrBadCredentials
	}

	// HashCode refuses a malformed code, and a malformed one still costs an
	// attempt: not charging for it would let an attacker probe the shape for
	// free and would make the budget depend on what they sent.
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

	// Right. Burn before issuing: a session that exists while its code still
	// does is a code somebody can use twice.
	if err := s.Store.BurnCode(ctx, tr.ID); err != nil {
		return Issued{}, err
	}

	// The same three lines SignIn ends on. NO DECOY IS NEEDED HERE and that
	// is a difference worth naming: SignIn runs a throwaway Argon2 verify for
	// an unknown address so the timing does not answer the question. A code
	// is a SHA-256 compare, which is far too cheap for the missing work to be
	// measurable over a network.
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
