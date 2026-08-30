// The read snapshot.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"travellog/internal/postgres/testdb"
)

func TestWithReadSnapshotHandsTheCurrentVersionToTheBody(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	var seen int64 = -1
	if err := WithReadSnapshot(ctx, db, tid, func(context.Context, *sql.Tx, int64) error {
		return nil
	}); err != nil {
		t.Fatalf("WithReadSnapshot: %v", err)
	}

	if _, err := WithTravellerTx(ctx, db, tid, func(ctx context.Context, tx *sql.Tx) error {
		return insertTrip(ctx, tx, "kyoto")
	}); err != nil {
		t.Fatalf("the write: %v", err)
	}

	if err := WithReadSnapshot(ctx, db, tid, func(_ context.Context, _ *sql.Tx, v int64) error {
		seen = v
		return nil
	}); err != nil {
		t.Fatalf("WithReadSnapshot: %v", err)
	}
	if seen != 1 {
		t.Errorf("the body was handed version %d after one write, want 1", seen)
	}
}

// The version is read INSIDE the snapshot, so a write that commits after the
// snapshot opened is invisible to both halves of the answer.
func TestTheSnapshotSeesNeitherHalfOfAWriteThatCommittedAfterItOpened(t *testing.T) {
	db, schema := withTravellers(t)
	other := testdb.Second(t, schema)
	ctx := context.Background()

	inside := make(chan struct{})
	written := make(chan struct{})
	type answer struct {
		version int64
		trips   int64
	}
	got := make(chan answer, 1)

	go func() {
		_ = WithReadSnapshot(ctx, db, tid, func(ctx context.Context, tx *sql.Tx, v int64) error {
			close(inside)
			<-written
			var trips int64
			if err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM trips WHERE traveller_id = $1`, tid).Scan(&trips); err != nil {
				return err
			}
			got <- answer{version: v, trips: trips}
			return nil
		})
	}()

	<-inside
	if _, err := WithTravellerTx(ctx, other, tid, func(ctx context.Context, tx *sql.Tx) error {
		return insertTrip(ctx, tx, "kyoto")
	}); err != nil {
		t.Fatalf("the concurrent write: %v", err)
	}
	close(written)

	a := <-got
	if a.version != 0 || a.trips != 0 {
		t.Errorf("the snapshot saw version %d and %d trips, want 0 and 0 — "+
			"a write that committed after it opened reached inside it", a.version, a.trips)
	}

	if err := WithReadSnapshot(ctx, db, tid, func(ctx context.Context, tx *sql.Tx, v int64) error {
		if v != 1 {
			t.Errorf("the NEXT snapshot saw version %d, want 1 — the write never became visible", v)
		}
		return nil
	}); err != nil {
		t.Fatalf("the second read: %v", err)
	}
}

// The read transaction is READ only.
func TestTheReadSnapshotRefusesToWrite(t *testing.T) {
	db := withTraveller(t)
	ctx := context.Background()

	err := WithReadSnapshot(ctx, db, tid, func(ctx context.Context, tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO trips (traveller_id, id, name) VALUES ($1,'kyoto','Kyoto')`, tid)
		return err
	})
	if err == nil {
		t.Fatalf("the read snapshot accepted an INSERT")
	}
	if n := count(t, db, `SELECT count(*) FROM trips WHERE traveller_id = $1`, tid); n != 0 {
		t.Errorf("trips = %d, want 0", n)
	}
}

func TestWithReadSnapshotRefusesATravellerThatIsNotThere(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	ran := 0

	err := WithReadSnapshot(ctx, db, tid, func(context.Context, *sql.Tx, int64) error {
		ran++
		return nil
	})
	if !errors.Is(err, ErrNoTraveller) {
		t.Errorf("WithReadSnapshot on an unknown traveller = %v, want ErrNoTraveller", err)
	}
	if ran != 0 {
		t.Errorf("the body ran %d times for a traveller that does not exist, want 0", ran)
	}
}

// A read must not take the advisory lock.
func TestAReadTakesNoAdvisoryLockAndDoesNotBlockAWrite(t *testing.T) {
	db, schema := withTravellers(t)
	other := testdb.Second(t, schema)
	ctx := context.Background()

	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithReadSnapshot(ctx, db, tid, func(context.Context, *sql.Tx, int64) error {
			close(inside)
			<-release
			return nil
		})
	}()

	select {
	case <-inside:
	case <-timeout():
		t.Fatalf("the read never reached its body")
	}
	if !lockIsFree(t, other, tid) {
		t.Errorf("the read took the per-traveller advisory lock")
	}

	wrote := make(chan error, 1)
	go func() {
		_, err := WithTravellerTx(ctx, other, tid, func(ctx context.Context, tx *sql.Tx) error {
			return insertTrip(ctx, tx, "kyoto")
		})
		wrote <- err
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Errorf("the write beside an open read: %v", err)
		}
	case <-timeout():
		t.Fatalf("a write blocked behind an open read snapshot")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("WithReadSnapshot: %v", err)
	}
}
