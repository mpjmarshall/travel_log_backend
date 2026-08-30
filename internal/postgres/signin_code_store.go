package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"travellog/internal/auth"
)

// The sign_in_codes table, on AuthStore.
func (s AuthStore) IssueCode(ctx context.Context, travellerID string, hash []byte, expiresAt time.Time) error {
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
func (s AuthStore) CodeFor(ctx context.Context, travellerID string) (auth.SignInCode, error) {
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
func (s AuthStore) CountAttempt(ctx context.Context, travellerID string) (int, error) {
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
func (s AuthStore) BurnCode(ctx context.Context, travellerID string) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM sign_in_codes WHERE traveller_id = $1`, travellerID); err != nil {
		return fmt.Errorf("postgres: burning a sign-in code: %w", err)
	}
	return nil
}
