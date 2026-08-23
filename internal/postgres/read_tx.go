// The read snapshot (DEC-06, DEC-31), and the one cache bug that never
// self-corrects.
//
// GET /v1/logbook is the only read, and it emits six lists plus the traveller
// as one document under one ETag. Two things therefore have to be true of it
// at once: the six lists must agree with each other, and the version number the
// phone stores the body under must be the version that describes THAT body.
//
// A repeatable-read snapshot gives both, and nothing cheaper does. Under READ
// COMMITTED each statement sees a newer database than the last, so a write
// landing mid-read is in the photographs and not in the trips; and a version
// read outside the snapshot is a number describing a different moment than the
// bytes. The phone stores the pair, believes it, and stops asking — a torn
// document under a version that has already moved on is served for ever, and
// nothing on either side can discover it.
//
// THE VERSION IS READ FIRST, INSIDE THE SNAPSHOT, AND HANDED TO THE BODY. That
// order is what makes a 304 cost one indexed row read: the handler compares the
// tag before it assembles anything, and a conditional request that has not
// changed never touches the six lists at all.
//
// READ ONLY IS THE DATABASE ENFORCING WHAT THE COMMENT ASKS FOR. A read-only
// repeatable-read transaction in PostgreSQL also cannot raise a serialization
// failure, so the snapshot costs a held view and nothing else — there is no
// retry loop here and none is needed.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const readVersionSQL = `SELECT logbook_version FROM travellers WHERE id = $1::uuid`

// snapshotOptions is the whole of DEC-06's reader half.
var snapshotOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}

// WithReadSnapshot runs fn inside a repeatable-read, read-only transaction,
// having first read this traveller's logbook_version inside that same
// transaction and handed it over.
//
// It takes NO advisory lock, deliberately. Reads are the primary path, the
// snapshot already gives the reader a consistent view, and a reader queuing
// behind every write would buy nothing — the writes it would wait for are
// exactly the ones its snapshot is defined not to see.
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
