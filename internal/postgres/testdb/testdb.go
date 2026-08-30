// Package testdb is the test seam onto a real PostgreSQL.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const urlVar = "TEST_DATABASE_URL"

// skipVar is the opt-out, and it is written down rather than inferred.
const skipVar = "TRAVELLOG_SKIP_DB"

// optedOut reads the opt-out generously, because the alternative is a guard
// that appears broken.
func optedOut(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// minServerVersionNum is PostgreSQL 15, and it is a hard floor.
const minServerVersionNum = 150000

// TB is the slice of *testing.T that testdb uses.
type TB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// Open answers a pool scoped to a fresh empty schema, and that schema's name
// — which is what a Migrator's Schema field wants.
func Open(t TB) (*sql.DB, string) {
	t.Helper()

	dsn := os.Getenv(urlVar)
	if strings.TrimSpace(dsn) == "" {
		if optedOut(os.Getenv(skipVar)) {
			t.Skipf("%s is unset and %s=1, so this tier is skipped ON PURPOSE.\n"+
				"    Nothing below this line has been checked against a database.",
				urlVar, skipVar)
			return nil, ""
		}
		t.Fatalf("%s is not set, so the database tier cannot run — and it is the "+
			"tier that holds the cascades, the snapshots and the locks.\n"+
			"    Bring one up:            make test-db\n"+
			"    Or say you mean it:      %s=1 make check", urlVar, skipVar)
		return nil, ""
	}

	ctx := context.Background()

	boot, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", urlVar, err)
		return nil, ""
	}
	closeBoot := true
	defer func() {
		if closeBoot {
			boot.Close()
		}
	}()

	if err := boot.PingContext(ctx); err != nil {
		t.Fatalf("connecting to the database named by %s: %v", urlVar, err)
		return nil, ""
	}

	var num int
	if err := boot.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&num); err != nil {
		t.Fatalf("reading server_version_num: %v", err)
		return nil, ""
	}
	if err := checkServerVersion(num); err != nil {
		t.Fatalf("%v", err)
		return nil, ""
	}

	schema := fmt.Sprintf("t_%d_%d", time.Now().UnixNano()%1_000_000_000, rand.N(1_000_000))
	if _, err := boot.ExecContext(ctx, `CREATE SCHEMA `+quoteIdent(schema)); err != nil {
		t.Fatalf("creating the test schema %s: %v", schema, err)
		return nil, ""
	}

	scoped, err := scopedDSN(dsn, schema)
	if err != nil {
		t.Fatalf("scoping %s to schema %s: %v", urlVar, schema, err)
		return nil, ""
	}
	db, err := sql.Open("pgx", scoped)
	if err != nil {
		t.Fatalf("opening the scoped pool: %v", err)
		return nil, ""
	}

	closeBoot = false
	t.Cleanup(func() {
		db.Close()
		_, _ = boot.ExecContext(context.Background(), `DROP SCHEMA `+quoteIdent(schema)+` CASCADE`)
		boot.Close()
	})
	return db, schema
}

func checkServerVersion(num int) error {
	if num >= minServerVersionNum {
		return nil
	}
	return fmt.Errorf("this schema needs PostgreSQL 15 or later and %s points at %s.\n"+
		"    migrations/0001_init.up.sql uses the column-list form of ON DELETE SET NULL,\n"+
		"    added in 15 (DEC-66). On an older server it does not parse, and the plain\n"+
		"    composite form it would be 'fixed' to nulls traveller_id, which is NOT NULL.\n"+
		"    deploy/docker-compose.yml pins postgres:17 — use `make test-db`",
		urlVar, versionText(num))
}

// versionText turns 140012 into 14.12 and 90624 into 9.6.24.
func versionText(num int) string {
	if num >= 100000 {
		return fmt.Sprintf("%d.%d", num/10000, num%10000)
	}
	return fmt.Sprintf("%d.%d.%d", num/10000, (num/100)%100, num%100)
}

// scopedDSN adds `options=-c search_path=<schema>` to a URL-form DSN, so
// every connection the pool opens lands in the test's own schema.
func scopedDSN(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	q := u.Query()
	opts := strings.TrimSpace(q.Get("options"))
	scoped := "-c search_path=" + schema
	if opts != "" {
		scoped = opts + " " + scoped
	}
	q.Set("options", scoped)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// quoteIdent is enough for the generated schema names above, which are
// [a-z0-9_] by construction.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// Second answers a second, independent pool on the same schema.
func Second(t TB, schema string) *sql.DB {
	t.Helper()
	dsn := os.Getenv(urlVar)
	if strings.TrimSpace(dsn) == "" {
		t.Skipf("%s is not set; run: make test-db", urlVar)
		return nil
	}
	scoped, err := scopedDSN(dsn, schema)
	if err != nil {
		t.Fatalf("scoping to %s: %v", schema, err)
		return nil
	}
	db, err := sql.Open("pgx", scoped)
	if err != nil {
		t.Fatalf("opening a second pool: %v", err)
		return nil
	}
	t.Cleanup(func() { db.Close() })
	return db
}
