// The storage half of auth.Store (: the business rules own the contract and
// the storage implementation satisfies it).
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

// createTravellerSQL relies on the functional unique index by name of its
// expression.
const createTravellerSQL = `INSERT INTO travellers (id, email, passphrase_hash)
	VALUES (gen_random_uuid(), $1, $2)
	ON CONFLICT (lower(email)) DO NOTHING
	RETURNING id, email, name`

// travellerByEmailSQL is the rule as a statement.
const travellerByEmailSQL = `SELECT id, email, name, passphrase_hash
	FROM travellers WHERE lower(email) = lower($1)`

const createSessionSQL = `INSERT INTO sessions (id, traveller_id, token_hash, expires_at)
	VALUES (gen_random_uuid(), $1::uuid, $2, $3)
	RETURNING id`

// sessionByTokenHashSQL brings token_hash back out with the row.
const sessionByTokenHashSQL = `SELECT s.id, s.traveller_id, s.token_hash,
		s.last_used_at, s.expires_at, s.revoked_at,
		t.id, t.email, t.name
	FROM sessions s JOIN travellers t ON t.id = s.traveller_id
	WHERE s.token_hash = $1`

// travellerExistsAtAllSQL is the question, and it is `SELECT 1 … LIMIT 1`
// Than a count.
const travellerExistsAtAllSQL = `SELECT 1 FROM travellers LIMIT 1`

// touchSessionSQL names the traveller as well as the session.
const touchSessionSQL = `UPDATE sessions SET last_used_at = $3
	WHERE id = $1::uuid AND traveller_id = $2::uuid`

// CreateTraveller inserts one, and answers auth.ErrEmailTaken when the
// address is already held in any casing.
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

// CreateSession writes a session under the traveller's lock and moves no
// version.
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

// TouchSession writes `last_used_at`, moves no version, and takes no advisory
// lock.
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

// revokeSessionSQL names the traveller as well as the digest.
const revokeSessionSQL = `UPDATE sessions SET revoked_at = now()
	WHERE traveller_id = $1::uuid AND token_hash = $2 AND revoked_at IS NULL`

const revokeEverySessionSQL = `UPDATE sessions SET revoked_at = now()
	WHERE traveller_id = $1::uuid AND revoked_at IS NULL`

// RevokeSession kills one session and moves no version.
func (s AuthStore) RevokeSession(ctx context.Context, travellerID string, tokenHash []byte) (bool, error) {
	result, err := s.DB.ExecContext(ctx, revokeSessionSQL, travellerID, tokenHash)
	if err != nil {
		return false, fmt.Errorf("postgres: revoking a session: %w", err)
	}
	moved, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("postgres: counting the revoked session: %w", err)
	}
	return moved > 0, nil
}

// RevokeEverySession kills all of this traveller's live sessions and moves no
// version.
func (s AuthStore) RevokeEverySession(ctx context.Context, travellerID string) (int64, error) {
	result, err := s.DB.ExecContext(ctx, revokeEverySessionSQL, travellerID)
	if err != nil {
		return 0, fmt.Errorf("postgres: revoking every session: %w", err)
	}
	moved, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("postgres: counting the revoked sessions: %w", err)
	}
	return moved, nil
}

// TravellerExists answers the question: does this log already have a
// traveller?
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

// named is the one place a NULL name becomes a nil pointer.
func named(tr auth.Traveller, name sql.NullString) auth.Traveller {
	if name.Valid {
		tr.Name = &name.String
	}
	return tr
}
