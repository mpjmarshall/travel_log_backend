// The runner, against a real PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
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

// tiny is a two-file migration set the runner legs can apply quickly.
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

// versionsIn is every.up.sql version in an fs.FS, in the lexical order the
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

// A file edited after it was applied fails loudly: neither silently re-run
// nor silently ignored.
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

// A file that fails half way leaves nothing behind, ledger row included.
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

// The escape hatch, proven from both sides.
func TestTheNoTransactionDirectiveIsWhatMakesConcurrentlyPossible(t *testing.T) {
	const body = "CREATE TABLE IF NOT EXISTS one (x int);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS one_x_idx ON one (x);\n"

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
			Migrate(context.Background(), db, tiny(noTransactionDirective+"\n"+noTransactionReRunnable+"\n"+body))
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
// than racing.
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

// The pinned-connection leg.
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

// characterisation, not a guard on our own code.
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
// decision rather than an inherited default.
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

// leg nine.
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

// The control, without which leg nine is satisfied by A runner that
// refuses every migration.
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

// fixtureFS wraps's two files under internal/postgres/testdata in the
// filenames the runner accepts.
func fixtureFS(t *testing.T, version string) fstest.MapFS {
	t.Helper()
	up, err := os.ReadFile(filepath.Join("testdata", "notx_fixture.up.sql"))
	if err != nil {
		t.Fatalf("reading the no-transaction fixture: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("testdata", "notx_fixture.down.sql"))
	if err != nil {
		t.Fatalf("reading the no-transaction fixture's down file: %v", err)
	}
	return fstest.MapFS{
		version + "_notx_fixture.up.sql":   &fstest.MapFile{Data: up},
		version + "_notx_fixture.down.sql": &fstest.MapFile{Data: down},
	}
}

// subject is one no-transaction migration and the FS it can be loaded from.
type subject struct {
	source string
	name   string
	fsys   fs.FS
}

// noTransactionSubjects is every `-- migrate:no-transaction` file this
// repository holds, derived rather than listed, from both places one can be.
func noTransactionSubjects(t *testing.T) []subject {
	t.Helper()

	var out []subject
	shipped, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("loading the shipped migrations: %v", err)
	}
	for _, m := range shipped {
		if m.NoTransaction {
			out = append(out, subject{source: "migrations/", name: m.Name, fsys: migrations.FS})
		}
	}

	fixture := fixtureFS(t, "0001")
	out = append(out, subject{
		source: "internal/postgres/testdata/",
		name:   "notx_fixture.up.sql",
		fsys:   fixture,
	})
	return out
}

// The guard orders, and its own precondition.
func TestNoTransactionMigrationsAreReRunnable(t *testing.T) {
	subjects := noTransactionSubjects(t)
	if len(subjects) == 0 {
		t.Fatal("no `-- migrate:no-transaction` migration anywhere — this leg " +
			"has nothing to be about, and a subject set of zero is a green that " +
			"means nothing (DEC-99)")
	}

	var fromTestdata int
	for _, s := range subjects {
		if s.source == "internal/postgres/testdata/" {
			fromTestdata++
		}
	}
	if fromTestdata == 0 {
		t.Fatal("the testdata fixture is not in the subject set — migrations/ is " +
			"entirely transactional today, so deleting it leaves this leg with " +
			"nothing to run")
	}

	for _, s := range subjects {
		t.Run(s.source+s.name, func(t *testing.T) {
			db, schema := testdb.Open(t)
			m := Migrator{Schema: schema, Logger: quietLogger()}
			ctx := context.Background()

			applied, err := m.Migrate(ctx, db, s.fsys)
			if err != nil {
				t.Fatalf("first run: %v", err)
			}
			if len(applied) == 0 {
				t.Fatalf("the first run applied nothing")
			}

			if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations`); err != nil {
				t.Fatalf("clearing the ledger to stage the boot loop: %v", err)
			}

			if _, err := m.Migrate(ctx, db, s.fsys); err != nil {
				t.Fatalf("the SECOND run over the same statements failed: %v\n"+
					"    that is the boot loop DEC-99 is about: a half-applied "+
					"no-transaction file re-runs from statement 1 on every boot, "+
					"and this is what it reports instead of the real fault", err)
			}
		})
	}
}

// A no-transaction file that does not declare itself re-runnable is refused
// at load time ((b)).
func TestANoTransactionFileMustDeclareItIsReRunnable(t *testing.T) {
	body := noTransactionDirective + "\nCREATE TABLE IF NOT EXISTS one (x int);\n"
	_, err := loadMigrations(tiny(body))
	if err == nil {
		t.Fatal("a no-transaction migration with no re-runnability declaration was accepted")
	}
	for _, want := range []string{"0001_one.up.sql", noTransactionReRunnable, "IF NOT EXISTS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	declared := noTransactionDirective + "\n" + noTransactionReRunnable +
		"\nCREATE TABLE IF NOT EXISTS one (x int);\n"
	loaded, err := loadMigrations(tiny(declared))
	if err != nil {
		t.Fatalf("a file that DOES declare it was refused: %v", err)
	}
	if len(loaded) != 1 || !loaded[0].NoTransaction {
		t.Fatalf("loaded %d files, NoTransaction=%v; want one no-transaction file",
			len(loaded), len(loaded) == 1 && loaded[0].NoTransaction)
	}

	buried := noTransactionDirective + "\nCREATE TABLE IF NOT EXISTS one (x int);\n" +
		noTransactionReRunnable + "\n"
	if _, err := loadMigrations(tiny(buried)); err == nil {
		t.Error("the declaration was accepted from below the first statement")
	}
}

// The failure message carries the statement text and not only its ordinal
// ((d)), so log line one is enough.
func TestAFailingNoTransactionFileNamesTheStatementTextItDiedOn(t *testing.T) {
	db, schema := testdb.Open(t)
	m := Migrator{Schema: schema, Logger: quietLogger()}
	ctx := context.Background()

	body := noTransactionDirective + "\n" + noTransactionReRunnable + "\n" +
		"CREATE TABLE IF NOT EXISTS probe_half_applied (x int);\n" +
		"SELECT 1/0;\n"

	_, err := m.Migrate(ctx, db, tiny(body))
	if err == nil {
		t.Fatal("a file whose second statement divides by zero was reported as applied")
	}
	for _, want := range []string{"0001_one.up.sql", "statement 2", "SELECT 1/0", "division by zero"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not carry %q, so log line one is not enough: %v", want, err)
		}
	}

	if n := count(t, db, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='probe_half_applied'`); n != 1 {
		t.Fatalf("probe_half_applied exists %d times; the half-applied state this "+
			"leg is about did not happen", n)
	}
	if n := count(t, db, `SELECT count(*) FROM schema_migrations`); n != 0 {
		t.Fatalf("schema_migrations holds %d rows, want 0 — a no-transaction file "+
			"that failed must not be recorded", n)
	}

	again := m
	_, err = again.Migrate(ctx, db, tiny(body))
	if err == nil {
		t.Fatal("the second run succeeded — the fixture is meant to fail every time")
	}
	if !strings.Contains(err.Error(), "SELECT 1/0") {
		t.Errorf("the second run's failure does not carry the statement text either: %v", err)
	}
}
