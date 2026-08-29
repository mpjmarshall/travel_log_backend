package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"travellog/internal/auth"
)

// SignInCodeStore is the sign_in_codes table.
//
// EVERY METHOD HERE IS A SINGLE STATEMENT AND NONE TAKES THE TRAVELLER LOCK.
// DEC-50 exempts auth from `pg_advisory_xact_lock` because a sign-in is not a
// change to the log, and these are the same path: the one-row-per-traveller
// primary key means the upsert needs no read-then-write, so there is nothing
// for a lock to protect.
//
// AND NOTHING HERE TOUCHES logbook_version, which is the other half of DEC-50
// and is asserted rather than trusted — a bump would make every device
// re-fetch the whole log because somebody typed a code.
type SignInCodeStore struct{ DB *sql.DB }

// IssueCode stores a freshly minted code, REPLACING whatever that traveller
// held.
//
// THE UPSERT IS THE SECURITY CONTROL AND NOT A CONVENIENCE. Appending would
// give a traveller as many live codes as they asked for, each with its own
// five-guess budget, which is the bound gone. `ON CONFLICT … DO UPDATE` is
// what makes "request another code" mean "the last one stops working".
//
// IT ALSO RESETS `attempts`, deliberately and in the same statement: a
// traveller who mistyped five times has to be able to ask for a new code and
// use it, or the cap locks them out of their own account permanently.
func (s SignInCodeStore) IssueCode(ctx context.Context, travellerID string, hash []byte, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO sign_in_codes (traveller_id, code_hash, issued_at, expires_at, attempts)
		VALUES ($1, $2, now(), $3, 0)
		ON CONFLICT (traveller_id) DO UPDATE
		SET code_hash  = EXCLUDED.code_hash,
		    issued_at  = EXCLUDED.issued_at,
		    expires_at = EXCLUDED.expires_at,
		    attempts   = 0`,
		travellerID, hash, expiresAt)
	if err != nil {
		return fmt.Errorf("postgres: issuing a sign-in code: %w", err)
	}
	return nil
}

// CodeFor reads the traveller's live code, or auth.ErrNoCode.
//
// IT DOES NOT FILTER ON expires_at, and that is deliberate. An expired code
// and no code at all are different facts to the service — one of them means
// "ask for another", the other means "you never asked" — and a store that
// hid the difference would make the service unable to say which. Expiry is
// decided where the clock is injected.
func (s SignInCodeStore) CodeFor(ctx context.Context, travellerID string) (auth.SignInCode, error) {
	var c auth.SignInCode
	err := s.DB.QueryRowContext(ctx, `
		SELECT code_hash, issued_at, expires_at, attempts
		FROM sign_in_codes WHERE traveller_id = $1`, travellerID).
		Scan(&c.Hash, &c.IssuedAt, &c.ExpiresAt, &c.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.SignInCode{}, auth.ErrNoCode
	}
	if err != nil {
		return auth.SignInCode{}, fmt.Errorf("postgres: reading a sign-in code: %w", err)
	}
	return c, nil
}

// CountAttempt records one wrong guess and answers the new total.
//
// IT INCREMENTS AND READS IN ONE STATEMENT. Read-then-write would let two
// concurrent guesses both read four and both write five, which is six guesses
// against a cap of five — the exact shape the cap exists to prevent.
func (s SignInCodeStore) CountAttempt(ctx context.Context, travellerID string) (int, error) {
	var attempts int
	err := s.DB.QueryRowContext(ctx, `
		UPDATE sign_in_codes SET attempts = attempts + 1
		WHERE traveller_id = $1 RETURNING attempts`, travellerID).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, auth.ErrNoCode
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: counting a sign-in attempt: %w", err)
	}
	return attempts, nil
}

// BurnCode removes the code, which is what single-use means here.
//
// THE ROW GOES RATHER THAN BEING MARKED. A consumed row kept around lets a
// replay be told apart from a code that never existed, and the caller must
// not be able to tell those apart — the same call Service.Authenticate makes
// about a token nobody holds.
func (s SignInCodeStore) BurnCode(ctx context.Context, travellerID string) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM sign_in_codes WHERE traveller_id = $1`, travellerID); err != nil {
		return fmt.Errorf("postgres: burning a sign-in code: %w", err)
	}
	return nil
}
