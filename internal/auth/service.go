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
// flow.
//
// IT HAS A REVOCATION SURFACE SINCE R5 AND THIS COMMENT USED TO SAY IT DID
// NOT. `DELETE /v1/auth/session` revokes the presented token and
// `?scope=all` revokes every one the traveller holds. The old sentence
// ("there is no revocation UI yet — H1's 'Stop sharing' is about share links,
// not sessions") was true when it was written and is the kind of line that
// stays true-looking after the thing it describes has changed. The TTL is
// still untuned; what has changed is that thirty days is no longer the only
// bound on a stolen token.
const DefaultSessionTTL = 30 * 24 * time.Hour

// TouchInterval is how stale `last_used_at` has to be before a request writes
// it (DEC-100), and the whole of what it buys is round trips.
//
// MEASURED by the performance lens with pg_stat_statements reset around
// exactly ONE 304: NINE database round trips, FIVE of them to stamp this
// timestamp — `begin`, `pg_advisory_xact_lock`, `SELECT 1 FROM travellers`,
// `UPDATE sessions SET last_used_at`, `commit` — against 0.176 ms of total
// server exec time and a 3.0-4.0 ms wall clock. So the cost was round trips
// and not work, and four of those five are gone whenever this interval has
// not elapsed.
//
// FIVE MINUTES IS THIS BUILD'S OWN POLICY AND NOTHING DEPENDS ON THE VALUE.
// `last_used_at` is read by nothing in this system: no route answers it, no
// sweep keys off it, and expiry is `expires_at`. It exists so that a human
// looking at the table can tell a live session from an abandoned one, and for
// that a five-minute granularity and a per-request one are the same answer.
// The day something reads it — an idle timeout, a "last seen" line — this
// constant becomes a bound on that feature's accuracy and needs deciding with
// it.
const TouchInterval = 5 * time.Minute

// The sentinels. DEC-62: the sentinel is the domain's word and the wire code
// is httpapi's; this package names no HTTP status and imports no httpx.
var (
	// ErrEmailTaken is a second registration of one address, in any casing.
	// It comes from the unique index rather than from a check-then-insert.
	//
	// SINCE DEC-86 IT IS UNREACHABLE THROUGH THE ROUTE, and it is kept anyway.
	// Registration closes after the first traveller, so a second registration
	// of ANY address — the same one included — is refused by
	// ErrRegistrationClosed before the INSERT is ever attempted. What still
	// produces this is the STORE, called directly, which is what
	// auth_store_test.go's casing leg does and is the only thing in this
	// repository that exercises DEC-65's `lower(email)` index in the write
	// direction. Deleting the branch would turn a unique violation into a 500
	// the day ruling 3 wants a second traveller.
	ErrEmailTaken = errors.New("auth: that address is already registered")

	// ErrRegistrationClosed is DEC-86: this instance already has a traveller.
	//
	// IT IS A 409, THE SAME STATUS AND THE SAME WORD ErrEmailTaken WEARS, AND
	// THE ORACLE SHRINKS RATHER THAN GROWS. The security lens flagged the
	// 409-on-duplicate as an enumeration surface — it tells a caller whether a
	// particular address is registered here. A 409 on ANY second registration
	// tells them only that the instance is in use, which the sign-in page
	// already tells them. So the two answers becoming indistinguishable on the
	// wire is the improvement and not a compromise.
	ErrRegistrationClosed = errors.New("auth: this log already has a traveller")

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
//
// LastUsedAt IS CARRIED OUT FOR DEC-100, AND IT COSTS NOTHING TO CARRY. The
// row is already being read and scanned; adding one column to that SELECT is
// not a round trip. What it buys is the granularity decision being made HERE,
// with the value in hand, rather than inside the UPDATE — where `WHERE
// last_used_at < $4` would make a matched-nothing row indistinguishable from
// a session that has been deleted, and TouchSession's row-count check is the
// only thing that notices the second.
type Session struct {
	ID          string
	TravellerID string
	TokenHash   []byte
	LastUsedAt  time.Time
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

	// TouchSession stamps `last_used_at` and answers ErrNoSession when it
	// matched no row — which is a session that has been deleted or that
	// belongs to somebody else, and is why the row count is checked rather
	// than trusted. It is called at most once per TouchInterval per session
	// (DEC-100), so an implementation may assume it is not on the hot path.
	TouchSession(ctx context.Context, travellerID, sessionID string, at time.Time) error

	// TravellerExists answers whether ANY traveller row is present, which is
	// DEC-86's question.
	//
	// IT IS A QUESTION AND NOT A GUARD ON CreateTraveller, and that is a
	// decision with a cost on both sides. Putting `WHERE NOT EXISTS (SELECT 1
	// FROM travellers)` into the INSERT would make the database enforce it —
	// and it would also make EVERY second CreateTraveller answer "no row",
	// which is the same answer a duplicate address gives. DEC-65's unique
	// index on `lower(email)` would then be exercised by nothing at all:
	// auth_store_test.go's casing leg is the only thing that reaches it, and
	// it reaches it by calling this method twice. So the rule lives in
	// Service.Register, where it can also be answered BEFORE Argon2 runs, and
	// the store keeps the constraint the database owns.
	//
	// WHAT THAT LEAVES OPEN, STATED RATHER THAN SILENT: check-then-insert is
	// not atomic. Two registrations whose statements overlap can both find an
	// empty table — under READ COMMITTED each takes its snapshot at statement
	// start, so the second does not see the first's uncommitted row, and
	// putting the predicate inside the INSERT would not close it either.
	// Closing it needs a transaction with an advisory lock (which is DEC-50's
	// one named exception giving up its exception) or a unique index on a
	// constant expression (which is a fifth migration). The window is between
	// the owner's first registration and a stranger's, on a fresh instance,
	// and the loser of that race is refused; it is written down here because a
	// security control with a race nobody named is worse than one with a race
	// somebody did.
	TravellerExists(ctx context.Context) (bool, error)
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

// Register creates a traveller and MINTS NO SESSION (DEC-61), and REFUSES
// ONCE THIS LOG HAS ONE (DEC-86).
//
// RULING 3 HAS ALWAYS BEEN SINGLE-USER AND THE ROUTE WAS NOT. A stranger who
// registered first on a deployed instance got an authenticated account — and
// since d5be39c that account carries a 600/min traveller budget, and from R6 a
// `?photos=delete`. Closing the route removes the question rather than
// bounding it.
//
// THE REFUSAL COMES BEFORE THE ARGON2 CALL, AND THAT ORDERING IS THE POINT OF
// ASKING THE STORE RATHER THAN LETTING THE INSERT ANSWER. Hashing is 64 MiB
// and tens of milliseconds by design (DEC-08); a closed instance that pays it
// on every attempt is an unauthenticated memory sink behind a route nobody can
// ever succeed on. DEC-48's per-address ceiling still bounds it, and a bound
// on work nobody should be doing at all is the weaker of the two guards.
//
// AND IT IS NOT A TIMING ORACLE, because there is nothing left to distinguish.
// SignIn pays Argon2 for an unknown address precisely so that two outcomes
// cost the same; here BOTH outcomes of a closed instance are the same outcome
// — every registration is refused — so a caller learns from the clock exactly
// what the status code already told them.
func (s *Service) Register(ctx context.Context, email, passphrase string) (Traveller, error) {
	if err := checkEmail(email); err != nil {
		return Traveller{}, err
	}
	if err := checkPassphrase(passphrase); err != nil {
		return Traveller{}, err
	}

	// THE FIELD CHECKS COME FIRST AND THAT IS DELIBERATE. A malformed address
	// is a 422 naming the field whether or not this instance is open, and
	// answering 409 to it would tell a client to stop trying when what it
	// needs to do is fix the body. Neither check touches the database.
	switch held, err := s.Store.TravellerExists(ctx); {
	case err != nil:
		return Traveller{}, err
	case held:
		return Traveller{}, ErrRegistrationClosed
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

// Authenticate resolves a bearer token, and writes `last_used_at` AT MOST ONCE
// PER TouchInterval (DEC-100).
//
// THE TOUCH IS A WRITE THAT MUST NOT BUMP logbook_version, and the store is
// where that is enforced. Count it, and the phone's whole cached log is
// invalidated on every authenticated request, so GET /v1/logbook never once
// answers 304 in real use.
//
// THE GRANULARITY DECISION IS HERE AND NOT IN THE STATEMENT, and that is not
// where it would first be reached for. `UPDATE … WHERE last_used_at < $4` is
// one fewer branch and it destroys something: TouchSession answers
// ErrNoSession when its UPDATE matches nothing, which is what notices a
// session deleted between the lookup and the write, and under that predicate a
// FRESH session matches nothing too. The two states become one answer, and the
// one that is a 401 wins. So the decision is taken with the value in hand,
// where a fresh session simply does not call the store at all.
//
// AND THE VALUE COMES OUT OF THE LOOKUP THAT HAS ALREADY HAPPENED. There is no
// extra read: `SessionByTokenHash` was scanning the row anyway.
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

	if now.Sub(session.LastUsedAt) >= TouchInterval {
		if err := s.Store.TouchSession(ctx, tr.ID, session.ID, now); err != nil {
			return Traveller{}, err
		}
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
