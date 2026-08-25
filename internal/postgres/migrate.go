// Package postgres applies the schema. VS4 is the runner and nothing else: the
// two transaction helpers (DEC-06) land at VS5 and are deliberately absent.
//
// PostgreSQL 15 is a hard floor (DEC-66) because migrations/0001_init.up.sql
// uses the column-list form of ON DELETE SET NULL, which 14 cannot parse.
// internal/postgres/testdb refuses an older server rather than letting that
// arrive as a syntax error in a file nobody is reading.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// migrateLockKey is arbitrary and must never change: two builds using different
// keys do not exclude each other, which is the one failure this lock prevents.
const migrateLockKey int64 = 5602251094132771329

// migrateLockTimeout bounds how long ONE statement in a migration waits for a
// table lock, and it is the THIRD OF THREE bounds rather than the whole thing
// (DEC-96, correcting OE-19).
//
// WHAT IT DOES NOT DO IS THE PART WORTH WRITING DOWN. It bounds the
// MIGRATION'S WAIT. It does nothing at all for the requests queued behind the
// migration's PENDING ACCESS EXCLUSIVE lock — a pending exclusive request
// blocks every later lock request on that table, so the queue forms whether or
// not the migration is patient. Measured by the operations lens against a
// synthetic ALTER of exactly R1's shape: one `GET /v1/logbook` returned curl
// http=000 at its own 30s limit, ten concurrent ones all returned 000 after
// 18.8s with 9 backends active/Lock, and /healthz answered 200 in 4.7ms
// throughout because it pings and never touches `trips`. Docker reported
// healthy through all of it. The other two bounds — REQUEST_TIMEOUT and the
// DSN's statement_timeout — are what answer those requests.
//
// THREE SECONDS, DERIVED RATHER THAN CHOSEN. The wait is not the cost; the
// QUEUE BEHIND IT is, so the number bounds an outage rather than a migration.
// It sits well under the per-request bound (REQUEST_TIMEOUT, 15s) so that a
// migration's lock wait can never be the thing that times a request out, and
// well under migrateTimeout (120s), which bounds the whole run including the
// advisory lock — that wait IS legitimate, because a second replica booting
// behind the first should queue.
//
// A MIGRATION THAT LOSES THE RACE FAILS AND THE CONTAINER RETRIES. That is
// deliberate: `restart: unless-stopped` brings it back, the health start
// period is 150s, and three seconds of noise in a log beats an unbounded stall
// with nothing in any log at all.
const migrateLockTimeout = 3 * time.Second

// migrationsTable has THREE columns, not two. DEC-17's own text describes
// `schema_migrations(version, applied_at)` while S05's work field describes
// `(version, checksum, applied_at)`; the checksum column is what the loud
// checksum failure is made of, so DEC-17's text is the one that is wrong.
const migrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text        PRIMARY KEY,
	checksum   text        NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// ErrChecksumMismatch reports a migration whose bytes changed after it was
// applied. Forward-only means the applied version cannot be re-run and the new
// bytes cannot be applied, so the only honest answer is to refuse to boot.
var ErrChecksumMismatch = errors.New("postgres: a migration was edited after it was applied")

// noTransactionDirective opts one file out of its transaction, and must be its
// first line.
//
// It exists now rather than later because the statements PostgreSQL refuses
// inside a transaction block are otherwise unreachable. Measured:
// `BEGIN; CREATE INDEX CONCURRENTLY foo_idx ON photos(caption);` answers
// `ERROR: CREATE INDEX CONCURRENTLY cannot run inside a transaction block`, and
// VACUUM, ALTER SYSTEM, CREATE DATABASE and REINDEX CONCURRENTLY are the same
// class. A file carrying this directive is NOT atomic — a failure half way
// leaves the earlier statements applied and no row in schema_migrations — so
// such a file must be written to be re-runnable.
const noTransactionDirective = "-- migrate:no-transaction"

// upName and downName are the only two filenames the runner accepts. The
// four-digit pad is not decoration: S05 says lexical order, and lexical order
// over an embed.FS is correct only at a constant width, since `10_x.up.sql`
// sorts before `2_x.up.sql` and reorders the schema silently.
var (
	upName   = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.up\.sql$`)
	downName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.down\.sql$`)
	schemaOK = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// Migration is one .up.sql file with the digest the runner records.
type Migration struct {
	Version       string
	Name          string
	SQL           string
	Checksum      string
	NoTransaction bool
}

// Migrator applies the .up.sql files in an fs.FS, in lexical order, each in its
// own transaction, with the whole run behind one advisory lock.
//
// Schema is pinned as the session search_path for the run, and empty means
// "public". That pin is load-bearing rather than tidy: the server default is
// `"$user", public` and an unqualified CREATE TABLE lands in the FIRST entry,
// so with a schema named after the connecting role present, every table in 0001
// lands there instead and the stack comes up looking correct. Measured on 17.11
// as the travellog role — `CREATE SCHEMA travellog; CREATE TABLE lands_where (x
// int);` puts lands_where in schema `travellog`. It also decides where DEC-65's
// functional index resolves `lower()`, since pg_catalog is implicitly first
// only while nothing names it explicitly later.
//
// Logger is slog.Default() when nil.
type Migrator struct {
	Schema string
	Logger *slog.Logger
}

// Migrate applies every unapplied file and answers the versions it applied, in
// the order it applied them. A second run over an unchanged directory answers
// an empty slice.
//
// THE WHOLE RUN IS ON ONE PINNED *sql.Conn, and that is the point.
// pg_advisory_lock is SESSION-scoped while database/sql is a POOL, so
// `db.ExecContext(lock)` and `db.ExecContext(unlock)` can land on two different
// connections. The unlock then does nothing, and PostgreSQL reports that as a
// WARNING and a `false` return value — both of which database/sql discards for
// an Exec. Measured: `SELECT pg_advisory_unlock(99)` on a session not holding
// lock 99 returns `f` and emits `WARNING: you don't own a lock of type
// ExclusiveLock`, raising no error. The lock then survives until that specific
// pooled connection closes, which under SetMaxIdleConns may be never, so a
// second replica booting later blocks for ever with nothing in any log.
func (m Migrator) Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) ([]string, error) {
	log := m.Logger
	if log == nil {
		log = slog.Default()
	}
	schema := m.Schema
	if schema == "" {
		schema = "public"
	}
	if !schemaOK.MatchString(schema) {
		return nil, fmt.Errorf("postgres: refusing schema name %q", schema)
	}

	files, err := loadMigrations(fsys)
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: taking a connection for the migration: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_catalog.set_config('search_path', $1, false)`, schema); err != nil {
		return nil, fmt.Errorf("postgres: pinning search_path to %q: %w", schema, err)
	}
	// BESIDE THE search_path PIN AND FOR THE SAME REASON: the whole run is on
	// this one pinned connection, so a session setting made here holds for
	// every statement in it — which `db.ExecContext` could not promise,
	// because database/sql is a pool and the setting would land on whichever
	// connection answered.
	if _, err := conn.ExecContext(ctx, `SELECT pg_catalog.set_config('lock_timeout', $1, false)`,
		strconv.FormatInt(migrateLockTimeout.Milliseconds(), 10)); err != nil {
		return nil, fmt.Errorf("postgres: setting lock_timeout for the migration: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return nil, fmt.Errorf("postgres: taking the migration lock: %w", err)
	}
	defer releaseLock(ctx, conn, log)

	if _, err := conn.ExecContext(ctx, migrationsTable); err != nil {
		return nil, fmt.Errorf("postgres: creating schema_migrations: %w", err)
	}

	applied, err := appliedChecksums(ctx, conn)
	if err != nil {
		return nil, err
	}

	var ran []string
	for _, f := range files {
		if sum, ok := applied[f.Version]; ok {
			if sum != f.Checksum {
				return ran, fmt.Errorf("%w: %s was applied with checksum %s and now hashes to %s — "+
					"migrations are forward-only, so neither re-running it nor ignoring it is safe; "+
					"revert the edit, or add a new migration",
					ErrChecksumMismatch, f.Name, sum, f.Checksum)
			}
			continue
		}
		if err := m.apply(ctx, conn, f); err != nil {
			return ran, err
		}
		log.Info("migrate: applied", "version", f.Version, "file", f.Name, "no_transaction", f.NoTransaction)
		ran = append(ran, f.Version)
	}
	return ran, nil
}

// releaseLock is belt and braces over the pinned connection: if it ever logs,
// the lock was taken on a different session and the next boot will block with
// no other symptom.
func releaseLock(ctx context.Context, conn *sql.Conn, log *slog.Logger) {
	var released bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, migrateLockKey).Scan(&released); err != nil {
		log.Error("migrate: the advisory unlock failed", "err", err, "key", migrateLockKey)
		return
	}
	if !released {
		log.Error("migrate: the advisory unlock answered false — the lock was not held on this connection",
			"key", migrateLockKey)
	}
}

// apply runs one file's statements and records it, in a transaction unless the
// file opted out. Statements go one at a time so that a failure names which,
// and because the simple query protocol wraps several sent together in an
// implicit transaction block — the very thing the opt-out exists to escape.
func (m Migrator) apply(ctx context.Context, conn *sql.Conn, f Migration) error {
	const record = `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`

	stmts := splitStatements(f.SQL)
	if len(stmts) == 0 {
		return fmt.Errorf("postgres: %s holds no statements", f.Name)
	}

	if f.NoTransaction {
		for i, s := range stmts {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("postgres: %s statement %d (no-transaction, so statements 1..%d are already applied): %w",
					f.Name, i+1, i, err)
			}
		}
		if _, err := conn.ExecContext(ctx, record, f.Version, f.Checksum); err != nil {
			return fmt.Errorf("postgres: recording %s: %w", f.Name, err)
		}
		return nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: beginning %s: %w", f.Name, err)
	}
	defer tx.Rollback()

	for i, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("postgres: %s statement %d: %w", f.Name, i+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, record, f.Version, f.Checksum); err != nil {
		return fmt.Errorf("postgres: recording %s: %w", f.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing %s: %w", f.Name, err)
	}
	return nil
}

// appliedChecksums answers version -> checksum for everything already applied.
func appliedChecksums(ctx context.Context, conn *sql.Conn) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading schema_migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var v, c string
		if err := rows.Scan(&v, &c); err != nil {
			return nil, fmt.Errorf("postgres: reading schema_migrations: %w", err)
		}
		out[v] = c
	}
	return out, rows.Err()
}

// loadMigrations reads, validates and orders the .up.sql files at the root of
// fsys. Every refusal here is a refusal to boot, which is the point: a
// migrations directory the runner half-understands is worse than one it
// rejects. A .up.sql with no .down.sql beside it is refused too — down files
// are never run automatically, but a migration nobody can reverse by hand is
// one that has to be reversed by restore.
func loadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the migrations directory: %w", err)
	}

	upsByVersion := map[string]string{}
	downsByVersion := map[string]bool{}
	var out []Migration

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		switch {
		case upName.MatchString(name):
			v := upName.FindStringSubmatch(name)[1]
			if first, dup := upsByVersion[v]; dup {
				return nil, fmt.Errorf("postgres: two migrations at version %s: %s and %s", v, first, name)
			}
			upsByVersion[v] = name

			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return nil, fmt.Errorf("postgres: reading %s: %w", name, err)
			}
			sum := sha256.Sum256(body)
			src := string(body)
			out = append(out, Migration{
				Version:       v,
				Name:          name,
				SQL:           src,
				Checksum:      hex.EncodeToString(sum[:]),
				NoTransaction: strings.HasPrefix(src, noTransactionDirective),
			})
		case downName.MatchString(name):
			downsByVersion[downName.FindStringSubmatch(name)[1]] = true
		default:
			return nil, fmt.Errorf("postgres: %s is not a migration name — "+
				"expected NNNN_name.up.sql or NNNN_name.down.sql, four digits and [a-z0-9_]", name)
		}
	}

	if len(out) == 0 {
		return nil, errors.New("postgres: no .up.sql migrations found")
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	for _, m := range out {
		if !downsByVersion[m.Version] {
			return nil, fmt.Errorf("postgres: %s has no down file — "+
				"down files are checked in and never run automatically, but %s must exist",
				m.Name, strings.TrimSuffix(m.Name, ".up.sql")+".down.sql")
		}
	}
	return out, nil
}
