// The runner, against a real PostgreSQL.
//
// Every leg here skips, saying so, when TEST_DATABASE_URL is unset.
package postgres

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// tiny is a two-file migration set the runner legs can apply quickly. The real
// 0001 is applied by the schema legs; these are about the RUNNER.
func tiny(body string) fstest.MapFS {
	return fstest.MapFS{
		"0001_one.up.sql":   &fstest.MapFile{Data: []byte(body)},
		"0001_one.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE IF EXISTS one;\n")},
	}
}

func TestTheRunnerAppliesEachFileExactlyOnceAcrossTwoRuns(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	// EVERY VERSION IN THE DIRECTORY, IN ORDER, DERIVED RATHER THAN LISTED.
	// This used to read `want [0001]` and went red the moment 0002 landed —
	// correctly, but for the wrong reason: the claim is "each file exactly
	// once, in lexical order", and a literal turns that into "there is one
	// file". Deriving it means the next migration reddens this leg only if it
	// is applied twice, out of order, or not at all.
	want := versionsIn(t, migrations.FS)
	if len(want) < 2 {
		t.Fatalf("the migrations directory holds %v — this leg is about ORDER and "+
			"needs at least two files to be about anything", want)
	}

	first, err := m.Migrate(ctx, db, migrations.FS)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if strings.Join(first, ",") != strings.Join(want, ",") {
		t.Fatalf("first run applied %v, want %v in that order", first, want)
	}

	second, err := m.Migrate(ctx, db, migrations.FS)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want nothing", second)
	}
	for _, version := range want {
		if n := count(t, db,
			`SELECT count(*) FROM schema_migrations WHERE version='`+version+`'`); n != 1 {
			t.Errorf("schema_migrations holds %d rows for %s, want 1", n, version)
		}
	}
}

// versionsIn is every .up.sql version in an fs.FS, in the lexical order the
// runner promises to apply them in.
func versionsIn(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	names, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		t.Fatalf("globbing the migrations: %v", err)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = strings.SplitN(name, "_", 2)[0]
	}
	return out
}

// A file edited after it was applied fails LOUDLY: neither silently re-run nor
// silently ignored. Forward-only means there is no third answer.
func TestAFileEditedAfterItWasAppliedFailsLoudly(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	if _, err := m.Migrate(ctx, db, tiny("CREATE TABLE one (x int);\n")); err != nil {
		t.Fatalf("first run: %v", err)
	}
	_, err := m.Migrate(ctx, db, tiny("CREATE TABLE one (x int, y int);\n"))
	if err == nil {
		t.Fatal("an edited migration was accepted — it was neither re-run nor reported")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("error = %v, want it to wrap ErrChecksumMismatch", err)
	}
	for _, want := range []string{"0001_one.up.sql", "was applied with checksum", "now hashes to"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='one'`); n != 1 {
		t.Errorf("the edited file was partly applied: table `one` has %d columns, want 1", n)
	}
}

// A file that fails half way leaves NOTHING behind, ledger row included.
func TestAFailingFileLeavesNoHalfAppliedSchema(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}

	_, err := m.Migrate(context.Background(), db,
		tiny("CREATE TABLE one (x int);\nCREATE TABLE one (x int);\n"))
	if err == nil {
		t.Fatal("a migration whose second statement fails was reported as applied")
	}
	if !strings.Contains(err.Error(), "statement 2") {
		t.Errorf("the failure does not name the statement that failed: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='one'`); n != 0 {
		t.Errorf("table `one` survives a failed migration — the file was not atomic")
	}
	if n := count(t, db, `SELECT count(*) FROM schema_migrations`); n != 0 {
		t.Errorf("schema_migrations holds %d rows after a failed migration, want 0", n)
	}
}

// THE ESCAPE HATCH, PROVEN FROM BOTH SIDES. The same file with and without the
// directive: without it, PostgreSQL refuses the statement outright.
func TestTheNoTransactionDirectiveIsWhatMakesConcurrentlyPossible(t *testing.T) {
	const body = "CREATE TABLE one (x int);\nCREATE INDEX CONCURRENTLY one_x_idx ON one (x);\n"

	t.Run("without the directive it is refused", func(t *testing.T) {
		db, schema := testdb.Open(t)
		_, err := (Migrator{Schema: schema, Logger: quietLogger()}).
			Migrate(context.Background(), db, tiny(body))
		if err == nil {
			t.Fatal("CREATE INDEX CONCURRENTLY succeeded inside a transaction block")
		}
		if !strings.Contains(err.Error(), "cannot run inside a transaction block") {
			t.Errorf("error = %v, want PostgreSQL's refusal", err)
		}
	})

	t.Run("with it the file applies", func(t *testing.T) {
		db, schema := testdb.Open(t)
		applied, err := (Migrator{Schema: schema, Logger: quietLogger()}).
			Migrate(context.Background(), db, tiny(noTransactionDirective+"\n"+body))
		if err != nil {
			t.Fatalf("the escape hatch did not work: %v", err)
		}
		if len(applied) != 1 {
			t.Fatalf("applied %v, want [0001]", applied)
		}
		if n := count(t, db, `SELECT count(*) FROM pg_indexes
			WHERE schemaname=current_schema() AND indexname='one_x_idx'`); n != 1 {
			t.Errorf("one_x_idx was not created")
		}
		if n := count(t, db, `SELECT count(*) FROM schema_migrations WHERE version='0001'`); n != 1 {
			t.Errorf("the no-transaction file was not recorded")
		}
	})
}

// The whole run is behind one advisory lock, so a second boot waits rather
// than racing. Asserted by holding the lock from another session and watching
// Migrate fail to finish inside its context.
func TestASecondRunWaitsWhileTheLockIsHeldElsewhere(t *testing.T) {
	db, schema := testdb.Open(t)
	other := testdb.Second(t, schema)

	held, err := other.Conn(context.Background())
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer held.Close()
	if _, err := held.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		t.Fatalf("taking the lock from elsewhere: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = (Migrator{Schema: schema, Logger: quietLogger()}).Migrate(ctx, db, migrations.FS)
	if err == nil {
		t.Fatal("the migration ran while another session held the lock")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the context deadline (i.e. it was WAITING)", err)
	}
	if time.Since(start) < time.Second {
		t.Errorf("it gave up after %v; the lock should have made it wait", time.Since(start))
	}

	if _, err := held.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLockKey); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("after the lock was released: %v", err)
	}
}

// THE PINNED-CONNECTION LEG. If the lock and the unlock landed on two
// different pooled connections the unlock would be a no-op — a `false` return
// and a WARNING, both of which database/sql discards for an Exec — and the
// lock would survive until that connection closed, which under
// SetMaxIdleConns may be never.
func TestTheLockIsActuallyReleasedWhenTheRunFinishes(t *testing.T) {
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	other := testdb.Second(t, schema)
	var got bool
	if err := other.QueryRow(`SELECT pg_try_advisory_lock($1)`, migrateLockKey).Scan(&got); err != nil {
		t.Fatalf("try lock: %v", err)
	}
	if !got {
		t.Fatal("the migration lock is still held after Migrate returned — " +
			"the unlock landed on a different connection than the lock")
	}
	other.Exec(`SELECT pg_advisory_unlock($1)`, migrateLockKey)
}

// CHARACTERISATION, not a guard on our own code: this is the hazard the pinned
// connection exists to avoid, reproduced here so the design has its measurement
// beside it rather than in a review document. No mutation of this repository
// can redden it.
//
// Two connections are held open so the pool has two distinct sessions; the lock
// is taken on one and released from the other, which is exactly what a pool
// does to `db.ExecContext(lock)` followed by `db.ExecContext(unlock)`.
func TestASessionLockTakenOverThePoolCanBeUnlockedOnTheWrongConnection(t *testing.T) {
	_, schema := testdb.Open(t)
	pool := testdb.Second(t, schema)
	pool.SetMaxOpenConns(2)
	pool.SetMaxIdleConns(2)

	const key = int64(987654321012345)

	a, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn a: %v", err)
	}
	b, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn b: %v", err)
	}
	if _, err := a.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatalf("lock on a: %v", err)
	}

	var released bool
	if err := b.QueryRowContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key).Scan(&released); err != nil {
		t.Fatalf("unlock on b: %v", err)
	}
	if released {
		t.Fatal("unlocking from a session that never held the lock reported success")
	}
	t.Log("pg_advisory_unlock on the wrong session returned false and raised NO error — " +
		"an Exec would have discarded both, which is why the runner pins one *sql.Conn")

	var got bool
	if err := a.QueryRowContext(context.Background(), `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		t.Fatalf("try: %v", err)
	}
	a.ExecContext(context.Background(), `SELECT pg_advisory_unlock_all()`)
	b.Close()
	a.Close()
}

// The runner pins search_path for the run, so where the schema lands is a
// decision rather than an inherited default. Measured: the server default is
// `"$user", public`, and with a schema named after the connecting role present
// every unqualified CREATE TABLE lands there instead.
func TestTheRunnerCreatesInTheSchemaItWasGivenAndNotWhereverTheSessionPoints(t *testing.T) {
	db, poolSchema := testdb.Open(t)
	elsewhere := poolSchema + "_target"
	mustExec(t, db, `CREATE SCHEMA `+elsewhere)
	t.Cleanup(func() { db.Exec(`DROP SCHEMA ` + elsewhere + ` CASCADE`) })

	if _, err := (Migrator{Schema: elsewhere, Logger: quietLogger()}).
		Migrate(context.Background(), db, tiny("CREATE TABLE one (x int);\n")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='one'`, elsewhere); n != 1 {
		t.Errorf("table `one` is not in %s, which is the schema the runner was given", elsewhere)
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='one'`, poolSchema); n != 0 {
		t.Errorf("table `one` landed in %s, which is where the SESSION pointed", poolSchema)
	}
}

func TestARefusedMigrationsDirectoryNeverReachesTheDatabase(t *testing.T) {
	db, schema := testdb.Open(t)
	_, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, fstest.MapFS{
			"1_unpadded.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE one (x int);\n")},
			"1_unpadded.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE one;\n")},
		})
	if err == nil {
		t.Fatal("an unpadded migration name was applied")
	}
	if n := count(t, db, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='schema_migrations'`); n != 0 {
		t.Error("the ledger was created before the directory was validated")
	}
}

// LEG NINE. A migration that cannot take its table lock FAILS INSIDE
// lock_timeout rather than stalling.
//
// MEASURED by the operations lens against a synthetic 0002 of exactly R1's
// shape: with an ALTER queued behind an open reader, one `GET /v1/logbook`
// returned curl http=000 at its own 30s limit — no status, no body, no error —
// and ten concurrent ones all returned 000 after 18.8s, with 9 backends
// active/Lock. /healthz answered 200 in 4.7ms throughout and docker said
// healthy, because /healthz pings and never touches `trips`.
//
// WHAT THIS LEG DOES NOT PROVE, and OE-19 is the correction: lock_timeout
// bounds the MIGRATION'S WAIT and does nothing for the requests queued behind
// the migration's pending ACCESS EXCLUSIVE lock. It is the THIRD OF THREE
// bounds and not the whole; the other two are REQUEST_TIMEOUT and the DSN's
// statement_timeout, and the pair-mutation that keeps them separate is
// recorded in CLAUDE.md.
//
// IT ANSWERS FROM A GOROUTINE BEHIND A `select` WITH A DEADLINE. A leg whose
// only failure mode is a hang is a leg somebody eventually deletes with the
// suite red and no diagnosis, so the deadline is here to turn "it stalled"
// into a sentence.
func TestAMigrationThatCannotTakeItsLockFailsWithinTheTimeout(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	first := fstest.MapFS{
		"0001_one.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE one (x int);\n")},
		"0001_one.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE one;\n")},
	}
	if _, err := m.Migrate(ctx, db, first); err != nil {
		t.Fatalf("applying 0001: %v", err)
	}

	// A REAL LOCK, HELD, ON A SECOND SESSION. testdb.Second is what makes this
	// a different backend rather than the same pooled connection, which would
	// not block at all.
	holder := testdb.Second(t, schema)
	held, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a second connection: %v", err)
	}
	defer held.Close()
	tx, err := held.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("beginning the holding transaction: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE one IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("taking the lock: %v", err)
	}

	second := fstest.MapFS{
		"0001_one.up.sql":   first["0001_one.up.sql"],
		"0001_one.down.sql": first["0001_one.down.sql"],
		"0002_two.up.sql":   &fstest.MapFile{Data: []byte("ALTER TABLE one ADD COLUMN y int;\n")},
		"0002_two.down.sql": &fstest.MapFile{Data: []byte("ALTER TABLE one DROP COLUMN y;\n")},
	}

	errc := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := m.Migrate(ctx, db, second)
		errc <- err
	}()

	// Twice the bound plus a second of slack. The assertion is on the ANSWER,
	// not on the elapsed time — a leg asserting "under 3.2 seconds" is a leg
	// that goes red on a loaded machine and gets deleted for flaking.
	deadline := 2*migrateLockTimeout + time.Second
	select {
	case err := <-errc:
		if err == nil {
			t.Fatalf("the migration succeeded while another session held ACCESS " +
				"EXCLUSIVE on the table it alters — it cannot have")
		}
		if !strings.Contains(err.Error(), "lock timeout") {
			t.Errorf("error = %v, want it to name a lock timeout — a migration that "+
				"failed for some other reason inside the window would satisfy a "+
				"leg that only checked for an error", err)
		}
		t.Logf("answered in %s against a %s bound", time.Since(started).Round(time.Millisecond),
			migrateLockTimeout)
	case <-time.After(deadline):
		t.Fatalf("no answer at all within %s — the migration is stalling on the "+
			"lock, which is the measured shape: nothing in any log, and every "+
			"request behind it silent", deadline)
	}
}

// AND THE CONTROL, WITHOUT WHICH LEG NINE IS SATISFIED BY A RUNNER THAT
// REFUSES EVERY MIGRATION. The same second file, applied with nothing holding
// the lock, must succeed.
func TestTheSameMigrationAppliesWhenNothingHoldsTheLock(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	set := fstest.MapFS{
		"0001_one.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE one (x int);\n")},
		"0001_one.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE one;\n")},
		"0002_two.up.sql":   &fstest.MapFile{Data: []byte("ALTER TABLE one ADD COLUMN y int;\n")},
		"0002_two.down.sql": &fstest.MapFile{Data: []byte("ALTER TABLE one DROP COLUMN y;\n")},
	}
	applied, err := m.Migrate(ctx, db, set)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) != 2 {
		t.Errorf("applied %v, want both", applied)
	}
}
