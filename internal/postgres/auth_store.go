// The storage half of auth.Store (DEC-62: the business rules own the contract
// and the storage implementation satisfies it).
//
// THE MEMBERSHIP SPLIT DEC-50 SPECIFIES IS ENFORCED HERE, AND IT IS THE WHOLE
// POINT OF THIS FILE:
//
//   - CreateSession and TouchSession take WithTravellerLock. They must move
//     logbook_version by ZERO. `last_used_at` is written on EVERY
//     authenticated request, so counting it invalidates the phone's whole
//     85 KB cached log every time it asks and GET /v1/logbook never once
//     answers 304 in real use.
//   - CreateTraveller takes NEITHER helper, and that is DEC-50's one named
//     exception: it INSERTs the traveller row the per-traveller advisory lock
//     is keyed on, so there is nothing to lock yet. It also opens no
//     transaction at all — one statement is already atomic — so the
//     transaction sweep in tx_sweep_test.go never sees it. VS5 predicted this
//     write would need an allowlist entry; it does not, and that entry has
//     been replaced by a leg that asserts the stronger thing.
//
// NOTHING HERE LOWERCASES AN EMAIL IN GO (DEC-65). The unique index is on
// `lower(email)` and every lookup says `lower(email) = lower($1)`, so the
// folding is the database's and is enforced rather than remembered. The
// address is stored exactly as typed.
//
// IDS ARE `gen_random_uuid()`, SERVER-SIDE. It has been core PostgreSQL since
// 13 and this schema's floor is 15 (DEC-66), so it needs no extension — which
// is the whole reason it is preferred to a uuid package. DEC-02's slug rule is
// about the CLIENT's ids and does not reach travellers or sessions.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"travellog/internal/auth"
)

// AuthStore is auth.Store over *sql.DB.
type AuthStore struct{ DB *sql.DB }

// createTravellerSQL relies on the functional unique index BY NAME OF ITS
// EXPRESSION. `ON CONFLICT (lower(email))` names DEC-65's index rather than a
// bare DO NOTHING, so a future second unique constraint on this table cannot
// be silently swallowed as "that address is taken".
const createTravellerSQL = `INSERT INTO travellers (id, email, passphrase_hash)
	VALUES (gen_random_uuid(), $1, $2)
	ON CONFLICT (lower(email)) DO NOTHING
	RETURNING id, email, name`

// travellerByEmailSQL is DEC-65's rule as a statement. `WHERE email = $1`
// compiles, runs, uses no index and returns zero rows against a
// differently-cased stored address — so the forgotten form does not error, it
// reports an address nobody registered.
const travellerByEmailSQL = `SELECT id, email, name, passphrase_hash
	FROM travellers WHERE lower(email) = lower($1)`

const createSessionSQL = `INSERT INTO sessions (id, traveller_id, token_hash, expires_at)
	VALUES (gen_random_uuid(), $1::uuid, $2, $3)
	RETURNING id`

// sessionByTokenHashSQL brings token_hash back out with the row. The row was
// FOUND by an indexed equality, which is not constant-time and cannot be; spec
// L24 asks for the comparison all the same, so the service re-checks it with
// subtle.ConstantTimeCompare and needs the bytes to do it.
const sessionByTokenHashSQL = `SELECT s.id, s.traveller_id, s.token_hash,
		s.last_used_at, s.expires_at, s.revoked_at,
		t.id, t.email, t.name
	FROM sessions s JOIN travellers t ON t.id = s.traveller_id
	WHERE s.token_hash = $1`

// travellerExistsAtAllSQL is DEC-86's question, and it is `SELECT 1 … LIMIT 1`
// rather than a count: the question is whether ANY row is there, and a count
// over a table that will hold one row is the same answer with a worse habit.
const travellerExistsAtAllSQL = `SELECT 1 FROM travellers LIMIT 1`

// touchSessionSQL names the traveller as well as the session, so one
// traveller's request cannot touch another's row even if the ids were crossed
// somewhere above.
//
// IT CARRIES NO `last_used_at < …` PREDICATE, and the granularity DEC-100 asks
// for is decided in internal/auth instead. Putting it here would be one fewer
// branch and would collapse two answers into one: this UPDATE reports
// ErrNoSession when it matches nothing, which is how a session deleted between
// the lookup and the write is noticed, and a FRESH session matches nothing
// too. See Service.Authenticate.
const touchSessionSQL = `UPDATE sessions SET last_used_at = $3
	WHERE id = $1::uuid AND traveller_id = $2::uuid`

// CreateTraveller inserts one, and answers auth.ErrEmailTaken when the address
// is already held in any casing.
//
// THE UNIQUE VIOLATION IS DETECTED BY THE ABSENCE OF A RETURNED ROW rather
// than by reading the driver's error code. Reading `23505` would mean
// importing pgconn, and cmd/api's import sweep asserts that pgx is imported
// exactly once, blank, in main — which is spec L20 ("solely as a blank import
// driver"). `ON CONFLICT … DO NOTHING RETURNING` answers the same question in
// SQL, so the constraint stays the database's and the driver stays a driver.
func (s AuthStore) CreateTraveller(ctx context.Context, email, passphraseHash string) (auth.Traveller, error) {
	var tr auth.Traveller
	var name sql.NullString

	switch err := s.DB.QueryRowContext(ctx, createTravellerSQL, email, passphraseHash).
		Scan(&tr.ID, &tr.Email, &name); {
	case errors.Is(err, sql.ErrNoRows):
		return auth.Traveller{}, fmt.Errorf("%w: %s", auth.ErrEmailTaken, email)
	case err != nil:
		return auth.Traveller{}, fmt.Errorf("postgres: creating a traveller: %w", err)
	}
	return named(tr, name), nil
}

// TravellerByEmail resolves an address in any casing, and answers the
// passphrase hash beside it.
func (s AuthStore) TravellerByEmail(ctx context.Context, email string) (auth.Traveller, string, error) {
	var tr auth.Traveller
	var name sql.NullString
	var hash string

	switch err := s.DB.QueryRowContext(ctx, travellerByEmailSQL, email).
		Scan(&tr.ID, &tr.Email, &name, &hash); {
	case errors.Is(err, sql.ErrNoRows):
		return auth.Traveller{}, "", auth.ErrNoTraveller
	case err != nil:
		return auth.Traveller{}, "", fmt.Errorf("postgres: looking up a traveller: %w", err)
	}
	return named(tr, name), hash, nil
}

// CreateSession writes a session under the traveller's lock and MOVES NO
// VERSION.
func (s AuthStore) CreateSession(ctx context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error) {
	var id string
	err := WithTravellerLock(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, createSessionSQL, travellerID, tokenHash, expiresAt).
			Scan(&id); err != nil {
			return fmt.Errorf("postgres: creating a session: %w", err)
		}
		return nil
	})
	if errors.Is(err, ErrNoTraveller) {
		return "", fmt.Errorf("%w: %s", auth.ErrNoTraveller, travellerID)
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// SessionByTokenHash is a READ and opens no transaction: one row, one
// statement, and nothing above it is deciding anything else.
func (s AuthStore) SessionByTokenHash(ctx context.Context, tokenHash []byte) (auth.Session, auth.Traveller, error) {
	var session auth.Session
	var tr auth.Traveller
	var name sql.NullString
	var revoked sql.NullTime

	switch err := s.DB.QueryRowContext(ctx, sessionByTokenHashSQL, tokenHash).Scan(
		&session.ID, &session.TravellerID, &session.TokenHash,
		&session.LastUsedAt, &session.ExpiresAt, &revoked,
		&tr.ID, &tr.Email, &name,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return auth.Session{}, auth.Traveller{}, auth.ErrNoSession
	case err != nil:
		return auth.Session{}, auth.Traveller{}, fmt.Errorf("postgres: looking up a session: %w", err)
	}
	if revoked.Valid {
		session.RevokedAt = &revoked.Time
	}
	return session, named(tr, name), nil
}

// TouchSession writes `last_used_at`, MOVES NO VERSION, and TAKES NO ADVISORY
// LOCK (DEC-100).
//
// IT USED TO GO THROUGH WithTravellerLock AND THAT COST FOUR ROUND TRIPS PER
// AUTHENTICATED REQUEST. Measured by the performance lens with
// pg_stat_statements reset around exactly one 304: NINE round trips, five of
// them to stamp this timestamp — `begin`, `pg_advisory_xact_lock`, `SELECT 1
// FROM travellers`, this UPDATE, `commit` — against 0.176 ms of total server
// exec time and a 3.0-4.0 ms wall clock. It also SERIALISED every authenticated
// request against the phone's own in-flight writes, through the same lock.
//
// WHAT THE LOCK WAS BUYING, AND WHY IT WAS NOTHING. tx.go argues at length
// that a session write must not bump logbook_version, which is correct and is
// a DIFFERENT QUESTION from whether it should take the write lock. What
// WithTravellerLock protects is MULTI-STATEMENT work — a delete-then-insert, a
// read-modify-write, DEC-02's six-way EXISTS check — and this is one UPDATE of
// one row keyed by session id. The row lock the UPDATE takes itself is the
// whole of the exclusion it needs.
//
// THE ROW COUNT IS STILL CHECKED, and it now does the job the wrapper's
// existence read used to do as well as its own: an UPDATE matching nothing
// reports success, so a session that has been deleted, that belongs to
// somebody else, or whose traveller row is gone would keep authenticating for
// as long as the caller believed the answer. `WHERE … AND traveller_id = $2`
// is what makes the third case a miss — the traveller's disappearance takes
// the session with it through sessions_traveller_fk's cascade — so
// WithTravellerLock's `SELECT 1 FROM travellers`, a whole extra row read per
// request, was asking a question this statement already answers.
//
// A TRAVELLER WHO IS GONE ANSWERS auth.ErrNoSession RATHER THAN
// auth.ErrNoTraveller, and the two are genuinely different answers here. This
// is reached from Authenticate, where the honest report is that the credential
// is not live — a 401. Reporting "no such traveller" would make a deleted
// account a 500.
func (s AuthStore) TouchSession(ctx context.Context, travellerID, sessionID string, at time.Time) error {
	result, err := s.DB.ExecContext(ctx, touchSessionSQL, sessionID, travellerID, at)
	if err != nil {
		return fmt.Errorf("postgres: touching a session: %w", err)
	}
	touched, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: counting the touched session: %w", err)
	}
	if touched == 0 {
		return fmt.Errorf("%w: %s", auth.ErrNoSession, sessionID)
	}
	return nil
}

// TravellerExists answers DEC-86's question: does this log already have a
// traveller?
//
// IT OPENS NO TRANSACTION AND TAKES NO LOCK. It is one indexed-free row read
// on a table that holds at most one row, and what it feeds is a decision the
// service makes before it hashes anything. The window between this answer and
// the INSERT that follows it is named in auth.Store's own comment.
func (s AuthStore) TravellerExists(ctx context.Context) (bool, error) {
	var one int
	switch err := s.DB.QueryRowContext(ctx, travellerExistsAtAllSQL).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("postgres: asking whether this log has a traveller: %w", err)
	}
	return true, nil
}

// named is the one place a NULL name becomes a nil pointer. DEC-61 leaves the
// column NULL until PATCH /v1/me, and the client reads a missing name as "a
// log nobody has named yet" — a different thing from the empty string.
func named(tr auth.Traveller, name sql.NullString) auth.Traveller {
	if name.Valid {
		tr.Name = &name.String
	}
	return tr
}
