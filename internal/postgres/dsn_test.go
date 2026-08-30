// The DSN rewrite.
package postgres_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"travellog/internal/postgres"
	"travellog/internal/postgres/testdb"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const bareDSN = "postgres://travellog:travellog@postgres:5432/travellog?sslmode=disable"

func optionsOf(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parsing %s: %v", dsn, err)
	}
	return u.Query().Get("options")
}

// All three, on one line.
func TestTheDSNCarriesTheThreeSessionSettings(t *testing.T) {
	got, err := postgres.WithSessionOptions(bareDSN, 15*time.Second)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	options := optionsOf(t, got)

	for _, want := range []string{
		"-c search_path=public",
		"-c statement_timeout=15000ms",
		"-c idle_in_transaction_session_timeout=60000ms",
	} {
		if !strings.Contains(options, want) {
			t.Errorf("options = %q, want it to carry %q", options, want)
		}
	}
}

// The unit is explicit and that is not decoration.
func TestTheTimeoutsCarryTheirUnit(t *testing.T) {
	got, err := postgres.WithSessionOptions(bareDSN, 15*time.Second)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	options := optionsOf(t, got)
	if strings.Contains(options, "statement_timeout=15 ") || strings.HasSuffix(options, "statement_timeout=15") {
		t.Errorf("options = %q — a bare number here is milliseconds", options)
	}
}

// preserving an existing `options=` is load-bearing, not polite.
func TestAnExistingOptionsParameterSurvivesAndWins(t *testing.T) {
	scoped := bareDSN + "&options=" + url.QueryEscape("-c search_path=t_123")

	got, err := postgres.WithSessionOptions(scoped, 15*time.Second)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	options := optionsOf(t, got)

	if !strings.Contains(options, "search_path=t_123") {
		t.Errorf("options = %q — the scoped search_path was lost, which points every "+
			"store test at a schema with no tables in it while every leg still runs",
			options)
	}
	if strings.Contains(options, "search_path=public") {
		t.Errorf("options = %q carries BOTH search_paths — the last one wins in "+
			"libpq, so this is a coin toss rather than a setting", options)
	}
	if !strings.Contains(options, "statement_timeout=15000ms") {
		t.Errorf("options = %q — the settings that were NOT already there must "+
			"still be added", options)
	}
}

// The other half of the same rule: a deployment that already answered the
// question is the more specific speaker.
func TestASettingAlreadyInTheDSNIsNotOverridden(t *testing.T) {
	theirs := bareDSN + "&options=" + url.QueryEscape("-c statement_timeout=90s")

	got, err := postgres.WithSessionOptions(theirs, 15*time.Second)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	options := optionsOf(t, got)
	if strings.Contains(options, "statement_timeout=15000ms") {
		t.Errorf("options = %q — a default that cannot be turned off is not a "+
			"default", options)
	}
	if !strings.Contains(options, "statement_timeout=90s") {
		t.Errorf("options = %q lost the operator's own setting", options)
	}
}

func TestARubbishDSNIsRefusedRatherThanRewritten(t *testing.T) {
	if _, err := postgres.WithSessionOptions("://not a url", time.Second); err == nil {
		t.Error("WithSessionOptions accepted a DSN that is not a URL")
	}
}

// The one that asks postgres what it actually received.
func TestPostgresAppliesTheSettingsThisDSNAsksFor(t *testing.T) {
	db, schema := testdb.Open(t)
	ctx := context.Background()

	dsn, err := postgres.WithSessionOptions(scopedURL(t, schema), 7*time.Second)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	pool := openPool(t, dsn)

	for _, want := range []struct{ setting, value string }{
		{"statement_timeout", "7s"},
		{"idle_in_transaction_session_timeout", "1min"},
		{"search_path", schema},
	} {
		var got string
		if err := pool.QueryRowContext(ctx,
			`SELECT current_setting($1)`, want.setting).Scan(&got); err != nil {
			t.Fatalf("reading %s: %v", want.setting, err)
		}
		if got != want.value {
			t.Errorf("the server reports %s = %q, want %q — libpq parsed the "+
				"`options=` line differently from the way this file builds it",
				want.setting, got, want.value)
		}
	}
	_ = db
}

// scopedURL rebuilds the DSN testdb would have handed out for a schema.
func scopedURL(t *testing.T, schema string) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func openPool(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the pool: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
