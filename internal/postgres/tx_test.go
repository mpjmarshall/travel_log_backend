// The two helpers, and the membership split exists for.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

const (
	sessionID   = "33333333-3333-3333-3333-333333333333"
	noTraveller = "44444444-4444-4444-4444-444444444444"
)

// blockedFor is how long a leg waits before calling a block a deadlock.
const blockedFor = 15 * time.Second

func timeout() <-chan time.Time { return time.After(blockedFor) }

// withTraveller is `migrated` plus one traveller at logbook_version 0.
func withTraveller(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := withTravellers(t)
	return db
}

// withTravellers answers a migrated schema holding both travellers.
func withTravellers(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("applying 0001: %v", err)
	}
	mustExec(t, db, `INSERT INTO travellers (id, email, passphrase_hash)
		VALUES ($1,'matt@example.com','x'), ($2,'other@example.com','x')`, tid, otherT)
	return db, schema
}

// lockIsFree asks a separate session whether the per-traveller lock can be
// taken, deriving the key from the formula independently of tx.go.
func lockIsFree(t *testing.T, other *sql.DB, travellerID string) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("second session, begin: %v", err)
	}
	defer tx.Rollback()

	var got bool
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtextextended($1::uuid::text, 0))`,
		travellerID).Scan(&got); err != nil {
		t.Fatalf("second session, try lock: %v", err)
	}
	return got
}

func version(t *testing.T, db *sql.DB, id string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT logbook_version FROM travellers WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatalf("reading logbook_version: %v", err)
	}
	return v
}

// insertTrip is the shape of the write will make: a row in the payload,
// written inside whatever transaction it is handed.
func insertTrip(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO trips (traveller_id, id, name) VALUES ($1,$2,$3)`, tid, id, "Trip "+id)
	return err
}

func TestWithTravellerTxBumpsTheVersionByOneAndAnswersIt(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	got, err := WithTravellerTx(ctx, db, tid, func(context.Context, *sql.Tx) error { return nil })
	if err != nil {
		t.Fatalf("WithTravellerTx: %v", err)
	}
	if got != 1 {
		t.Errorf("WithTravellerTx answered version %d, want 1", got)
	}
	if v := version(t, db, tid); v != 1 {
		t.Errorf("logbook_version = %d after one write, want 1", v)
	}

	got, err = WithTravellerTx(ctx, db, tid, func(context.Context, *sql.Tx) error { return nil })
	if err != nil {
		t.Fatalf("WithTravellerTx, second: %v", err)
	}
	if got != 2 {
		t.Errorf("the second write answered version %d, want 2", got)
	}
}

func TestWithTravellerTxCommitsTheDataAndTheNumberTogether(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	if _, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		return insertTrip(ctx, tx, "kyoto")
	}); err != nil {
		t.Fatalf("WithTravellerTx: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM trips WHERE traveller_id = $1`, tid); n != 1 {
		t.Errorf("trips = %d, want 1", n)
	}
	if v := version(t, db, tid); v != 1 {
		t.Errorf("logbook_version = %d beside one committed trip, want 1", v)
	}
}

func TestWithTravellerTxRollsBackTheBumpWhenTheBodyFails(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()
	boom := errors.New("the body refused")

	_, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertTrip(ctx, tx, "kyoto"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTravellerTx = %v, want the body's own error", err)
	}
	if n := count(t, db, `SELECT count(*) FROM trips WHERE traveller_id = $1`, tid); n != 0 {
		t.Errorf("trips = %d after a failed body, want 0", n)
	}
	if v := version(t, db, tid); v != 0 {
		t.Errorf("logbook_version = %d after a failed body, want 0 — the bump rode the rollback", v)
	}
}

// A panicking body must not leave the transaction open.
func TestWithTravellerTxRollsBackAPanickingBodyAndDoesNotStrandTheLock(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("the panic did not reach the caller")
			}
		}()
		_, _ = WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
			if err := insertTrip(ctx, tx, "kyoto"); err != nil {
				return err
			}
			panic("the body panicked")
		})
	}()

	if n := count(t, db, `SELECT count(*) FROM trips WHERE traveller_id = $1`, tid); n != 0 {
		t.Errorf("trips = %d after a panicking body, want 0", n)
	}

	done := make(chan error, 1)
	go func() {
		_, err := WithTravellerTx(context.Background(), db, tid, func(context.Context, *sql.Tx) error { return nil })
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the write after the panic: %v", err)
		}
	case <-timeout():
		t.Fatalf("the write after the panic blocked — the panicking transaction still holds the lock")
	}
}

func TestWithTravellerLockCommitsItsBodyAndMovesNoVersion(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	if err := WithTravellerLock(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, traveller_id, token_hash, expires_at)
			 VALUES ($1,$2,$3, now() + interval '30 days')`,
			sessionID, tid, make([]byte, 32))
		return err
	}); err != nil {
		t.Fatalf("WithTravellerLock: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM sessions WHERE traveller_id = $1`, tid); n != 1 {
		t.Errorf("sessions = %d, want 1 — the body was not committed", n)
	}
	if v := version(t, db, tid); v != 0 {
		t.Errorf("logbook_version = %d after a session write, want 0 (DEC-50: sessions do NOT bump)", v)
	}
}

func TestWithTravellerLockRollsBackWhenTheBodyFails(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()
	boom := errors.New("the body refused")

	err := WithTravellerLock(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, traveller_id, token_hash, expires_at)
			 VALUES ($1,$2,$3, now() + interval '30 days')`,
			sessionID, tid, make([]byte, 32)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTravellerLock = %v, want the body's own error", err)
	}
	if n := count(t, db, `SELECT count(*) FROM sessions WHERE traveller_id = $1`, tid); n != 0 {
		t.Errorf("sessions = %d after a failed body, want 0", n)
	}
}

// At the store's own level: a session write moves no version and a trip write
// does.
func TestASessionWriteDoesNotMoveTheVersionAndATripWriteDoes(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	if _, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		return insertTrip(ctx, tx, "kyoto")
	}); err != nil {
		t.Fatalf("the trip write: %v", err)
	}
	after := version(t, db, tid)
	if after != 1 {
		t.Fatalf("logbook_version = %d after the trip write, want 1", after)
	}

	for i := range 3 {
		if err := WithTravellerLock(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx,
				`UPDATE sessions SET last_used_at = now() WHERE traveller_id = $1`, tid)
			return err
		}); err != nil {
			t.Fatalf("session touch %d: %v", i, err)
		}
	}

	if v := version(t, db, tid); v != after {
		t.Errorf("logbook_version = %d after three authenticated requests, want %d.\n"+
			"    Every one of them would invalidate the phone's whole cached log, and\n"+
			"    GET /v1/logbook would never once answer 304 in real use (DEC-50a).", v, after)
	}
}

func TestBothHelpersRefuseATravellerThatIsNotThere(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()
	ran := 0

	if _, err := WithTravellerTx(ctx, db, noTraveller, func(ctx context.Context, tx *sql.Tx) error {
		ran++
		return nil
	}); !errors.Is(err, ErrNoTraveller) {
		t.Errorf("WithTravellerTx on an unknown traveller = %v, want ErrNoTraveller", err)
	}
	if err := WithTravellerLock(ctx, db, noTraveller, func(ctx context.Context, tx *sql.Tx) error {
		ran++
		return nil
	}); !errors.Is(err, ErrNoTraveller) {
		t.Errorf("WithTravellerLock on an unknown traveller = %v, want ErrNoTraveller", err)
	}
	if ran != 0 {
		t.Errorf("the body ran %d times for a traveller that does not exist, want 0", ran)
	}
}

// The::text cast in is not decoration, and here it is structural: the id is
// cast to uuid first, so `hashtextextended` can only be reached through text.
func TestATravellerIdThatIsNotAUuidIsRefusedRatherThanHashed(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	_, err := WithTravellerTx(ctx, db, "kyoto", func(context.Context, *sql.Tx) error { return nil })
	if err == nil {
		t.Fatalf("WithTravellerTx accepted a traveller id that is not a uuid")
	}
	if !strings.Contains(err.Error(), "uuid") {
		t.Errorf("the error does not name the type it refused: %v", err)
	}
}

// The lock is per traveller, and both halves are asserted: held against the
// same id, free against another.
func TestTheLockIsHeldForOneTravellerAndNotForAnother(t *testing.T) {
	db, schema := withTravellers(t)
	other := testdb.Second(t, schema)
	ctx := context.Background()

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := WithTravellerTx(ctx, db, tid, func(context.Context, *sql.Tx) error {
			close(held)
			<-release
			return nil
		})
		done <- err
	}()

	select {
	case <-held:
	case <-timeout():
		t.Fatalf("the write never reached its body")
	}

	if lockIsFree(t, other, tid) {
		t.Errorf("another session took the lock while a write held it — writes are not serialised")
	}
	if !lockIsFree(t, other, otherT) {
		t.Errorf("a second traveller's lock was taken too — the lock is not per traveller")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("WithTravellerTx: %v", err)
	}
	if !lockIsFree(t, other, tid) {
		t.Errorf("the lock outlived its transaction — pg_advisory_xact_lock was not what was taken")
	}
}

func TestWithTravellerLockTakesTheSameLockAsWithTravellerTx(t *testing.T) {
	db, schema := withTravellers(t)
	other := testdb.Second(t, schema)
	ctx := context.Background()

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithTravellerLock(ctx, db, tid, func(context.Context, *sql.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()

	select {
	case <-held:
	case <-timeout():
		t.Fatalf("the session write never reached its body")
	}
	if lockIsFree(t, other, tid) {
		t.Errorf("WithTravellerLock did not take the lock — DEC-50 splits the BUMP off, not the lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("WithTravellerLock: %v", err)
	}
}

func TestConcurrentWritersEachGetTheirOwnVersion(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()
	const writers = 8

	got := make([]int64, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
				return insertTrip(ctx, tx, fmt.Sprintf("trip-%d", i))
			})
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
				return
			}
			got[i] = v
		}()
	}
	wg.Wait()

	if v := version(t, db, tid); v != writers {
		t.Errorf("logbook_version = %d after %d concurrent writes, want %d", v, writers, writers)
	}
	seen := map[int64]bool{}
	for _, v := range got {
		if seen[v] {
			t.Errorf("two writers were both told they were version %d", v)
		}
		seen[v] = true
	}
}

// The writer-ordering leg.
func TestAReaderNeverSeesATripTheVersionDoesNotCount(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()
	const writers = 4

	stop := make(chan struct{})
	bad := make(chan string, 1)
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := WithReadSnapshot(ctx, db, tid, func(ctx context.Context, tx *sql.Tx, v int64) error {
				var trips int64
				if err := tx.QueryRowContext(ctx,
					`SELECT count(*) FROM trips WHERE traveller_id = $1`, tid).Scan(&trips); err != nil {
					return err
				}
				if trips != v {
					select {
					case bad <- fmt.Sprintf("a snapshot held version %d beside %d trips", v, trips):
					default:
					}
				}
				return nil
			})
			if err != nil {
				select {
				case bad <- fmt.Sprintf("the reader failed: %v", err):
				default:
				}
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
				return insertTrip(ctx, tx, fmt.Sprintf("trip-%d", i))
			}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	select {
	case msg := <-bad:
		t.Errorf("%s — the number and the data did not commit together", msg)
	default:
	}
}

// found at, and it is a blocker rather than A tidy-UP.
func TestAFailedWriteChecksItsConnectionBackIn(t *testing.T) {
	db, schema := withTravellers(t)
	ctx := context.Background()
	boom := errors.New("the body refused")

	if _, err := WithTravellerTx(ctx, db, tid, func(context.Context, *sql.Tx) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("WithTravellerTx = %v, want the body's own error", err)
	}

	if inUse := db.Stats().InUse; inUse != 0 {
		t.Errorf("%d connection(s) still checked out after a failed write, want 0 — "+
			"the transaction was never rolled back", inUse)
		freeTheStrandedTransaction(t, schema)
	}

	if _, err := WithTravellerTx(ctx, db, tid, func(context.Context, *sql.Tx) error {
		return nil
	}); err != nil {
		t.Errorf("the write after a failed one: %v", err)
	}
}

func TestAWriteForATravellerWhoIsNotThereChecksItsConnectionBackIn(t *testing.T) {
	db, schema := withTravellers(t)

	if _, err := WithTravellerTx(context.Background(), db, noTraveller,
		func(context.Context, *sql.Tx) error { return nil }); !errors.Is(err, ErrNoTraveller) {
		t.Fatalf("WithTravellerTx = %v, want ErrNoTraveller", err)
	}
	if inUse := db.Stats().InUse; inUse != 0 {
		t.Errorf("%d connection(s) still checked out after the bump found no traveller, want 0", inUse)
		freeTheStrandedTransaction(t, schema)
	}
}

// freeTheStrandedTransaction is called only on the failing path.
func freeTheStrandedTransaction(t *testing.T, schema string) {
	t.Helper()
	other := testdb.Second(t, schema)
	if _, err := other.ExecContext(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = current_database() AND state = 'idle in transaction'
		   AND pid <> pg_backend_pid()`); err != nil {
		t.Logf("could not free the stranded transaction: %v", err)
	}
}
