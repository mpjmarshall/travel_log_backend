// The read snapshot, and the one cache bug that never self-corrects.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const readVersionSQL = `SELECT logbook_version FROM travellers WHERE id = $1::uuid`

// snapshotOptions is the whole of the reader half.
var snapshotOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}

// WithReadSnapshot runs fn inside a repeatable-read, read-only transaction.
func WithReadSnapshot(ctx context.Context, db *sql.DB, travellerID string, fn func(context.Context, *sql.Tx, int64) error) error {
	tx, err := db.BeginTx(ctx, snapshotOptions)
	if err != nil {
		return fmt.Errorf("postgres: opening a read snapshot for %s: %w", travellerID, err)
	}
	defer tx.Rollback()

	var version int64
	switch err := tx.QueryRowContext(ctx, readVersionSQL, travellerID).Scan(&version); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", ErrNoTraveller, travellerID)
	case err != nil:
		return fmt.Errorf("postgres: reading logbook_version for %s: %w", travellerID, err)
	}

	return fn(ctx, tx, version)
}
