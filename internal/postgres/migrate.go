// Package postgres applies the schema.
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

// migrateLockKey is arbitrary and must never change.
const migrateLockKey int64 = 5602251094132771329

// migrateLockTimeout bounds how long one statement in a migration waits for a
// table lock.
const migrateLockTimeout = 3 * time.Second

// migrationsTable has three columns, not two.
const migrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text        PRIMARY KEY,
	checksum   text        NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// ErrChecksumMismatch reports a migration whose bytes changed after it was
// applied.
var ErrChecksumMismatch = errors.New("postgres: a migration was edited after it was applied")

// noTransactionDirective opts one file out of its transaction, and must be
// its first line.
const noTransactionDirective = "-- migrate:no-transaction"

// noTransactionReRunnable is the second header line such a file must carry,
// The runner refuses one that does not ((b)).
const noTransactionReRunnable = "-- migrate:re-runnable"

// upName and downName are the only two filenames the runner accepts.
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

// Migrator applies the.up.sql files in an fs.FS, in lexical order, each in
// its own transaction, with the whole run behind one advisory lock.
type Migrator struct {
	Schema string
	Logger *slog.Logger
}

// Migrate applies every unapplied file and answers the versions it applied,
// in the order it applied them.
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

// releaseLock is belt and braces over the pinned connection.
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

// apply runs one file's statements and records it, in a transaction unless
// the file opted out.
func (m Migrator) apply(ctx context.Context, conn *sql.Conn, f Migration) error {
	const record = `INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`

	stmts := splitStatements(f.SQL)
	if len(stmts) == 0 {
		return fmt.Errorf("postgres: %s holds no statements", f.Name)
	}

	if f.NoTransaction {
		for i, s := range stmts {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("postgres: %s statement %d, %q (no-transaction, so "+
					"statements 1..%d are already applied and NO schema_migrations row "+
					"was written, which means the next boot re-runs from statement 1): %w",
					f.Name, i+1, statementSummary(s), i, err)
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
			return fmt.Errorf("postgres: %s statement %d, %q: %w",
				f.Name, i+1, statementSummary(s), err)
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

// appliedChecksums answers version -> checksum for everything already
// applied.
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

// loadMigrations reads, validates and orders the.up.sql files at the root of
// fsys.
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
			noTx := strings.HasPrefix(src, noTransactionDirective)
			if noTx && !declaresReRunnable(src) {
				return nil, fmt.Errorf("postgres: %s carries %s and does not declare %s "+
					"in its header — a no-transaction file is NOT atomic, so a failure "+
					"part way leaves statements 1..i applied and no schema_migrations "+
					"row, and every later boot re-runs from statement 1 and fails on an "+
					"already-applied statement with a DIFFERENT error that hides the "+
					"original fault (DEC-99). Write every statement as IF NOT EXISTS or "+
					"DROP ... IF EXISTS and say so with that line",
					name, noTransactionDirective, noTransactionReRunnable)
			}
			out = append(out, Migration{
				Version:       v,
				Name:          name,
				SQL:           src,
				Checksum:      hex.EncodeToString(sum[:]),
				NoTransaction: noTx,
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

// declaresReRunnable reports whether a file's header carries the
// re-runnability declaration.
func declaresReRunnable(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, noTransactionReRunnable):
			return true
		case strings.HasPrefix(trimmed, "--"):
			continue
		default:
			return false
		}
	}
	return false
}

// statementSummary is the statement as one readable line.
func statementSummary(stmt string) string {
	var kept []string
	body := false
	for _, line := range strings.Split(stmt, "\n") {
		trimmed := strings.TrimSpace(line)
		if !body {
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			body = true
		}
		if trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	one := strings.Join(kept, " ")
	const cap = 160
	if len(one) > cap {
		return one[:cap] + "…"
	}
	return one
}
