// The session settings the application's own connections carry, and the one
// place a DSN is rewritten (DEC-96, DB-MIN-14).
//
// THREE SETTINGS, ON ONE `options=` LINE, AND EACH CLOSES A DIFFERENT HOLE.
//
//   - `search_path=public` closes a divergence the database lens found: the
//     MIGRATOR pins its schema (migrate.go, `set_config('search_path', …)` on
//     the pinned connection) and the APPLICATION did not, so migrations create
//     in a named schema and every read resolves through the server default,
//     `"$user", public`. With a schema named after the connecting role present
//     — which is one `CREATE SCHEMA travellog` away — the two disagree and the
//     application reads an empty database that looks correctly migrated.
//
//   - `statement_timeout` is DEC-96's second bound. httpx.Timeout stops the
//     HANDLER waiting; this stops the SERVER working. Without it a query
//     abandoned by a cancelled request keeps running to completion, holding a
//     connection out of a pool of eight and whatever locks it took.
//
//   - `idle_in_transaction_session_timeout` is the one that is not about
//     latency at all. A transaction left open holds its locks and pins the
//     oldest snapshot the whole cluster must keep, so autovacuum stops
//     reclaiming and the table bloats — and this project has already shipped
//     one transaction with no `defer tx.Rollback()`. The bound is the backstop
//     for the next one.
//
// WHY `options=` AND NOT `SET`. database/sql is a POOL: a `SET` runs on
// whichever connection answered and every other connection in the pool keeps
// the server default. That is the same defect testdb's own comment records
// about schema scoping, and it is invisible until the second connection is
// used. `options=` is part of the DSN, so pgx applies it to every connection
// it opens, including ones opened hours later to replace an idle one.
package postgres

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// IdleInTransactionTimeout bounds a transaction nobody is advancing.
//
// SIXTY SECONDS, AND IT IS FOUR TIMES THE REQUEST BOUND ON PURPOSE. Every
// legitimate transaction here is inside one request, so anything still open a
// minute after the request that started it could have finished is abandoned by
// definition. The margin exists so that a slow migration or a long
// `WithTravellerTx` under load is never killed by the backstop for a bug.
const IdleInTransactionTimeout = 60 * time.Second

// WithSessionOptions returns dsn with the application's session settings on
// its `options=` parameter, preserving anything already there.
//
// PRESERVING IS NOT POLITENESS: internal/postgres/testdb scopes its pool by
// putting `-c search_path=<schema>` on this very parameter, so a rewrite that
// replaced it would silently point every store test at `public` — where the
// tables are not — while every leg still ran.
//
// A SETTING ALREADY NAMED IN THE DSN WINS. The deployment is the more specific
// speaker: if somebody has put a statement_timeout on DATABASE_URL they are
// answering a question about their own database, and a default overriding it
// is a default that cannot be turned off.
func WithSessionOptions(dsn string, statementTimeout time.Duration) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("postgres: DATABASE_URL is not a URL: %w", err)
	}

	q := u.Query()
	existing := strings.TrimSpace(q.Get("options"))

	wanted := [][2]string{
		{"search_path", "public"},
		{"statement_timeout", durationOption(statementTimeout)},
		{"idle_in_transaction_session_timeout", durationOption(IdleInTransactionTimeout)},
	}

	parts := []string{}
	if existing != "" {
		parts = append(parts, existing)
	}
	for _, setting := range wanted {
		if strings.Contains(existing, setting[0]+"=") {
			continue
		}
		parts = append(parts, "-c "+setting[0]+"="+setting[1])
	}

	q.Set("options", strings.Join(parts, " "))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// durationOption renders a duration the way libpq reads one.
//
// MILLISECONDS WITH AN EXPLICIT UNIT. A bare integer in these two settings is
// milliseconds by default, so `15` would be fifteen thousandths of a second —
// a database that refuses every query — and the failure would read as "the
// database is broken" rather than "the unit was wrong".
func durationOption(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}
