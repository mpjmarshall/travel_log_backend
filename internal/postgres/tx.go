// The two write helpers, and the membership split between them.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNoTraveller is a write or a read for a traveller row that is not there.
var ErrNoTraveller = errors.New("postgres: no such traveller")

// travellerLockSQL is the key, and the cast chain is load-bearing twice.
const travellerLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text, 0))`

// bumpSQL is the one statement.
const bumpSQL = `UPDATE travellers SET logbook_version = logbook_version + 1
	WHERE id = $1::uuid
	RETURNING logbook_version`

const travellerExistsSQL = `SELECT 1 FROM travellers WHERE id = $1::uuid`

// WithTravellerTx runs fn inside one transaction holding this traveller's
// advisory lock, bumps logbook_version in the same transaction.
func WithTravellerTx(ctx context.Context, db *sql.DB, travellerID string, fn func(context.Context, *sql.Tx) error) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("postgres: beginning a write for %s: %w", travellerID, err)
	}
	defer tx.Rollback()

	if err := lockTraveller(ctx, tx, travellerID); err != nil {
		return 0, err
	}

	var version int64
	switch err := tx.QueryRowContext(ctx, bumpSQL, travellerID).Scan(&version); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("%w: %s", ErrNoTraveller, travellerID)
	case err != nil:
		return 0, fmt.Errorf("postgres: bumping logbook_version for %s: %w", travellerID, err)
	}

	if err := fn(ctx, tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("postgres: committing a write for %s: %w", travellerID, err)
	}
	return version, nil
}

// WithTravellerLock runs fn inside one transaction holding this traveller's
// advisory lock and moves no version.
func WithTravellerLock(ctx context.Context, db *sql.DB, travellerID string, fn func(context.Context, *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: beginning a locked write for %s: %w", travellerID, err)
	}
	defer tx.Rollback()

	if err := lockTraveller(ctx, tx, travellerID); err != nil {
		return err
	}
	if err := requireTraveller(ctx, tx, travellerID); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing a locked write for %s: %w", travellerID, err)
	}
	return nil
}

// lockTraveller takes the transaction-scoped lock.
func lockTraveller(ctx context.Context, tx *sql.Tx, travellerID string) error {
	if _, err := tx.ExecContext(ctx, travellerLockSQL, travellerID); err != nil {
		return fmt.Errorf("postgres: taking the write lock for %s: %w", travellerID, err)
	}
	return nil
}

func requireTraveller(ctx context.Context, tx *sql.Tx, travellerID string) error {
	var one int
	switch err := tx.QueryRowContext(ctx, travellerExistsSQL, travellerID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", ErrNoTraveller, travellerID)
	case err != nil:
		return fmt.Errorf("postgres: looking up %s: %w", travellerID, err)
	}
	return nil
}
