// Package testdb is the test seam onto a real PostgreSQL: it hands a test a
// pool scoped to a FRESH EMPTY SCHEMA, and skips — loudly enough to read —
// when there is no database to reach.
//
// WHY A SCHEMA RATHER THAN A DATABASE. Every leg in internal/postgres creates
// tables and then destroys them, so each needs its own namespace or two tests
// cannot run beside each other. A schema is one statement to make and one to
// drop; a database is a connection to another database to create it from and a
// template lock to fight over. The scoping is done in the DSN
// (`options=-c search_path=<schema>`) rather than with `SET search_path`,
// because database/sql is a POOL: a SET applies to the one connection it
// landed on and every other connection in the pool still points at public.
// That is the same class of defect as the migration lock's, and it is worth
// stating twice because it is invisible until the second connection is used.
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

// minServerVersionNum is PostgreSQL 15, and it is a HARD FLOOR (DEC-66).
//
// migrations/0001_init.up.sql uses the COLUMN-LIST form of ON DELETE SET NULL
// — `ON DELETE SET NULL (place_id)` — which 15 introduced. On 14 and earlier
// the file does not parse, and without this check the failure arrives as
// `syntax error at or near "("` pointing at a line that looks fine.
//
// Worse than the syntax error is what happens if somebody "fixes" it by
// dropping the column list: a composite FK's plain ON DELETE SET NULL nulls
// EVERY column of the referencing key, traveller_id included, and traveller_id
// is NOT NULL — so D2's keep branch and _repointed abort instead of clearing a
// pin. Measured on 17.11, with PostgreSQL echoing its own generated statement:
// `UPDATE ONLY "public"."photos" SET "traveller_id" = NULL, "place_id" = NULL`.
const minServerVersionNum = 150000

// TB is the slice of *testing.T that testdb uses. It is an interface rather
// than *testing.T so the skip path itself can be exercised: a test that only
// asserted the skip STRING would leave "does it actually skip?" proven by
// nothing.
type TB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// Open answers a pool scoped to a fresh empty schema, and that schema's name —
// which is what a Migrator's Schema field wants. The schema is dropped when
// the test finishes.
//
// WITHOUT A DATABASE IT FAILS, UNLESS SOMEBODY HAS SAID IN WRITING THAT THEY
// MEAN IT. It used to skip unconditionally so `make check` stayed green on a
// machine with no Docker, and the cost of that was measured rather than
// argued: the whole of internal/postgres skips — every cascade, snapshot,
// advisory-lock and schema leg, which is where the hardest reasoning in this
// repository lives — and the gate goes green anyway. A default green that says
// nothing about the layer most likely to break is worse than a red one,
// because it is believed.
//
// TRAVELLOG_SKIP_DB=1 is the opt-out, and it is deliberately a second variable
// rather than a looser reading of the first. Unset TEST_DATABASE_URL is
// ambiguous — it is equally "I have no Docker" and "I forgot" — and only one
// of those should pass. This is the same ruling DEC-48 already made about a
// nil limiter: a missing dependency must not read as a decision.
//
// It FAILS on a server older than 15, because that is a database that is
// present and wrong rather than absent.
func Open(t TB) (*sql.DB, string) {
	t.Helper()

	dsn := os.Getenv(urlVar)
	if strings.TrimSpace(dsn) == "" {
		if os.Getenv(skipVar) == "1" {
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

// versionText turns 140012 into 14.12 and 90624 into 9.6.24. Postgres numbered
// releases differently before 10, and the older form is the one somebody with
// a stale local install is most likely to be running.
func versionText(num int) string {
	if num >= 100000 {
		return fmt.Sprintf("%d.%d", num/10000, num%10000)
	}
	return fmt.Sprintf("%d.%d.%d", num/10000, (num/100)%100, num%100)
}

// scopedDSN adds `options=-c search_path=<schema>` to a URL-form DSN, so every
// connection the pool opens lands in the test's own schema.
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
// [a-z0-9_] by construction. It is not a general-purpose quoter and there is
// no user input anywhere near it.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// Second answers a SECOND, independent pool on the same schema. Two legs need
// a genuinely separate session — one holds an advisory lock while the runner
// tries to take it, and one demonstrates what a session-scoped lock does when
// the pool hands the unlock to a different connection.
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
