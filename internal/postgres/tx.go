// The two write helpers, and the membership split between them (DEC-50).
//
// EVERY TRAVELLER-SCOPED WRITE IN THIS SYSTEM GOES THROUGH ONE OF THESE TWO,
// and which one is not a judgement call — it is a list:
//
//	BUMPS logbook_version (WithTravellerTx): trips, cities, places, visits,
//	photos, walks, share_links, and the traveller row itself. Everything the
//	emitted logbook document contains.
//
//	DOES NOT BUMP (WithTravellerLock): sessions — create, `last_used_at` on
//	every authenticated request, revoke — and media_objects. Nothing in the
//	payload.
//
//	NEITHER: POST /v1/auth/register. It INSERTs the traveller row that the
//	per-traveller lock is keyed on, so it cannot lock one. It is the only write
//	outside both helpers, and tx_sweep_test.go allowlists it by name.
//
// BOTH LISTS ARE THE SPEC RATHER THAN A DESCRIPTION, because both obvious
// answers to "does a session write bump?" are broken. Count it, and
// `last_used_at` moves the number on EVERY authenticated request, so the
// phone's whole 85 KB cache is invalidated every time it asks and
// GET /v1/logbook never once answers 304 in real use. Do not count it and go
// outside the helper instead, and the write loses the advisory lock that
// DEC-02's cross-kind existence check and DEC-38's begin-upsert both declare
// themselves race-free under. The helper was doing two jobs with two different
// memberships; splitting the jobs is smaller than any rule about when to skip.
//
// WHY THE LOCK IS NOT REMOVABLE, stated because the obvious reading says it is.
// `UPDATE travellers SET logbook_version = logbook_version + 1` is ALREADY
// atomic under READ COMMITTED — Postgres re-reads the row it blocked on — so
// the counter is NOT what the lock protects, and DEC-30's original wording
// suggesting otherwise is corrected here. Measured on 17.11: eight concurrent
// writers through this helper leave logbook_version at 8, and eight doing the
// same bump as a read-modify-write (SELECT, then UPDATE ... = $1) WITHOUT the
// lock leave it at 1. WHAT THE LOCK PROTECTS IS THE MULTI-STATEMENT WORK the
// body does: PUT /v1/places/{id} replaces a whole ordered visits array as
// delete-then-insert, POST /refile is a read-modify-write across two tables,
// and DEC-02's six-way EXISTS check is only race-free while nothing else is
// writing this traveller. Delete the lock and every one of those tears, with
// the counter still incrementing correctly the whole time.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNoTraveller is a write or a read for a traveller row that is not there.
// It is separated from a driver error because the caller's answer differs: an
// unknown traveller is a 401 or a 404, and a driver error is a 500.
var ErrNoTraveller = errors.New("postgres: no such traveller")

// travellerLockSQL is DEC-06's key, and THE CAST CHAIN IS LOAD-BEARING TWICE.
//
// `hashtextextended` has no uuid overload — measured on 17.11:
// `PREPARE p(uuid) AS SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
// answers `function hashtextextended(uuid, integer) does not exist` at PREPARE
// time. With an untyped `$1` the server infers `text` and the cast looks
// removable, which is exactly how it gets removed.
//
// So the id is cast to `uuid` FIRST and to `text` after it. That puts the cast
// somewhere it cannot be dropped: without the `::text` the statement no longer
// prepares at all, so the mutation fails every leg in the package rather than
// waiting for a caller that happens to pass a uuid-typed parameter. The
// `::uuid` half earns its own place — it refuses a traveller id that is not a
// uuid instead of quietly locking the hash of some other string.
const travellerLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text, 0))`

// bumpSQL is DEC-30's one statement. RETURNING answers two questions at once —
// whether the traveller exists, and what version this transaction will commit
// under — so neither needs a second round trip.
const bumpSQL = `UPDATE travellers SET logbook_version = logbook_version + 1
	WHERE id = $1::uuid
	RETURNING logbook_version`

const travellerExistsSQL = `SELECT 1 FROM travellers WHERE id = $1::uuid`

// WithTravellerTx runs fn inside one transaction holding this traveller's
// advisory lock, bumps logbook_version in the SAME transaction, and answers
// the version the write committed under. Nothing is committed if fn returns an
// error, and the bump rides that rollback with everything else.
//
// THE BUMP IS TAKEN BEFORE fn RUNS, WHICH DIVERGES FROM DEC-30'S WORDING
// ("ends with") AND FROM NOTHING IT PROMISES. Both statements are in one
// transaction, so the order between them is invisible to every reader — the
// number and the data still become visible together or not at all. What the
// order DOES decide is whether fn runs at all for a traveller that is not
// there: bumping last means the body writes rows, discovers nobody owns them,
// and rolls back, and any side effect fn had outside the database has already
// happened. Bumping first turns that into ErrNoTraveller before the body is
// called. It also hands the body the version its own write will carry, which
// is what a write response's ETag needs.
func WithTravellerTx(ctx context.Context, db *sql.DB, travellerID string, fn func(context.Context, *sql.Tx) error) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("postgres: beginning a write for %s: %w", travellerID, err)
	}

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
// advisory lock and moves no version. It is the same exclusion with the bump
// taken off, for the writes that are not in the logbook payload: sessions and
// media objects.
//
// The existence check is a whole extra row read on every authenticated
// request, and it is here rather than left to the foreign keys because not
// every write in this class inserts. `UPDATE sessions SET last_used_at` on a
// traveller who has been deleted matches nothing and reports success, which is
// a sign-in that keeps working against a row that is gone.
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

// lockTraveller takes the transaction-scoped lock. It is xact-scoped rather
// than session-scoped on purpose: there is no unlock to forget, and the
// migration runner's own comment records what forgetting one costs — the lock
// survives on a pooled connection that may never close, and the next boot
// blocks for ever with nothing in any log.
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
