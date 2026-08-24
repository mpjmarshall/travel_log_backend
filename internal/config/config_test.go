// TEST-FIRST (agent-graph-spec-V4 §6.7). Every leg in this file was written and
// watched to fail before internal/config/config.go existed. The red output is
// recorded in CLAUDE.md under "VS2".
package config_test

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"travellog/internal/config"
)

// allVars is the seven variables VS2's step text names, written out rather than
// read from the package: a test that asks the subject what it reads cannot
// notice the subject forgetting to read one. TestLoadNamesEveryVariableWhenTheEnvironmentIsEmpty
// pins the count from the other side.
//
// EIGHT SINCE THE LIMITER FIX. TRAVELLER_RATE_LIMIT_PER_MIN is the
// authenticated ceiling, and it is a SECOND variable rather than a second use
// of AUTH_RATE_LIMIT_PER_MIN because the two bound different things: the
// credential ceiling bounds unauthenticated Argon2 work and is deliberately
// low, and this one bounds a stolen token and must be high enough that no
// honest client meets it.
var allVars = []string{
	"DATABASE_URL",
	"PORT",
	"LOG_LEVEL",
	"DB_MAX_OPEN_CONNS",
	"DB_MAX_IDLE_CONNS",
	"AUTH_RATE_LIMIT_PER_MIN",
	"TRAVELLER_RATE_LIMIT_PER_MIN",
	"ARGON2_MAX_CONCURRENT",
}

// complete is a whole environment Load must accept. Its values are the ones
// deploy/docker-compose.yml actually sets, so a leg that passes here is a leg
// about the stack rather than about invented input.
func complete() map[string]string {
	return map[string]string{
		"DATABASE_URL":                 "postgres://travellog:travellog@postgres:5432/travellog?sslmode=disable",
		"PORT":                         "8080",
		"LOG_LEVEL":                    "info",
		"DB_MAX_OPEN_CONNS":            "8",
		"DB_MAX_IDLE_CONNS":            "4",
		"AUTH_RATE_LIMIT_PER_MIN":      "10",
		"TRAVELLER_RATE_LIMIT_PER_MIN": "600",
		"ARGON2_MAX_CONCURRENT":        "2",
	}
}

// setEnv makes `vars` the WHOLE of what Load can see: every variable in allVars
// that vars does not name is explicitly unset, so an ambient value on the
// developer's machine cannot turn a missing-variable leg green.
//
// t.Setenv before os.Unsetenv is deliberate — t.Setenv is what registers the
// restore, and it is the only thing in `testing` that does.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for _, k := range allVars {
		t.Setenv(k, "")
		if v, ok := vars[k]; ok {
			t.Setenv(k, v)
			continue
		}
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetting %s: %v", k, err)
		}
	}
}

// without returns a complete environment with the named variables removed.
func without(keys ...string) map[string]string {
	env := complete()
	for _, k := range keys {
		delete(env, k)
	}
	return env
}

// with returns a complete environment with one variable overridden.
func with(key, value string) map[string]string {
	env := complete()
	env[key] = value
	return env
}

// --- one error, naming everything ------------------------------------------

// THE LEG THE STEP NAMES. A Load that fails on the first missing variable
// passes every other test in this file and fails this one: it is the only leg
// that can tell "reports a problem" from "reports the problems".
//
// It must also NOT name the four that ARE set — an error that lists everything
// is as useless as one that lists one thing.
func TestLoadReportsAllThreeMissingVariablesAtOnce(t *testing.T) {
	missing := []string{"DATABASE_URL", "DB_MAX_IDLE_CONNS", "ARGON2_MAX_CONCURRENT"}
	setEnv(t, without(missing...))

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error with three variables unset, want an error")
	}
	for _, name := range missing {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s:\n%s", name, err)
		}
	}
	for _, name := range allVars {
		if slicesContains(missing, name) {
			continue
		}
		if strings.Contains(err.Error(), name) {
			t.Errorf("error names %s, which is set:\n%s", name, err)
		}
	}
}

func TestLoadNamesEveryVariableWhenTheEnvironmentIsEmpty(t *testing.T) {
	setEnv(t, map[string]string{})

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error with an empty environment, want an error")
	}
	for _, name := range allVars {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s:\n%s", name, err)
		}
	}
}

// A variable set to the empty string is a variable that is not set. os.Getenv —
// which spec L30 mandates — cannot tell the two apart, and `FOO=` in a compose
// file or a .env is the ordinary way to produce one.
func TestLoadTreatsAnEmptyValueAsMissing(t *testing.T) {
	setEnv(t, with("DATABASE_URL", ""))

	_, err := config.Load()
	if err == nil {
		t.Fatal(`Load() = nil error with DATABASE_URL="", want an error`)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error does not name DATABASE_URL:\n%s", err)
	}
}

// A caller that ignores the error must not receive a half-filled Config it can
// mistake for a working one.
func TestLoadReturnsTheZeroConfigOnFailure(t *testing.T) {
	setEnv(t, without("DATABASE_URL"))

	cfg, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error, want an error")
	}
	if cfg != (config.Config{}) {
		t.Errorf("Load() returned %+v alongside an error, want the zero Config", cfg)
	}
}

// --- the happy path ---------------------------------------------------------

func TestLoadReadsEveryValueFromACompleteEnvironment(t *testing.T) {
	setEnv(t, complete())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error", err)
	}

	want := config.Config{
		DatabaseURL:              "postgres://travellog:travellog@postgres:5432/travellog?sslmode=disable",
		Port:                     "8080",
		LogLevel:                 slog.LevelInfo,
		DBMaxOpenConns:           8,
		DBMaxIdleConns:           4,
		AuthRateLimitPerMin:      10,
		TravellerRateLimitPerMin: 600,
		Argon2MaxConcurrent:      2,
	}
	if cfg != want {
		t.Errorf("Load() = %+v,\nwant %+v", cfg, want)
	}
}

func TestLoadAcceptsEveryLogLevelInAnyCase(t *testing.T) {
	for raw, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"INFO":  slog.LevelInfo,
		"Warn":  slog.LevelWarn,
	} {
		setEnv(t, with("LOG_LEVEL", raw))
		cfg, err := config.Load()
		if err != nil {
			t.Errorf("LOG_LEVEL=%q: Load() = %v, want no error", raw, err)
			continue
		}
		if cfg.LogLevel != want {
			t.Errorf("LOG_LEVEL=%q: LogLevel = %v, want %v", raw, cfg.LogLevel, want)
		}
	}
}

// --- the invalid values -----------------------------------------------------

// Table-driven over every rejection. Each row is a value somebody could
// plausibly put in a .env, and the assertion is that the error NAMES the
// variable — an error saying only `strconv.Atoi: invalid syntax` is an error
// that does not tell you which of the four integers is wrong.
func TestLoadRejectsInvalidValuesAndNamesTheVariable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
		why   string
	}{
		{"port is not a number", "PORT", "http", "SetAddr would listen on a name the kernel cannot bind"},
		{"port is zero", "PORT", "0", "port 0 asks the kernel for any free port; the container publishes 8080"},
		{"port is above the range", "PORT", "65536", "outside the 16-bit port space"},
		{"port is negative", "PORT", "-1", "outside the 16-bit port space"},
		{"log level is unknown", "LOG_LEVEL", "verbose", "slog has four levels and this is not one"},
		{"max open is not a number", "DB_MAX_OPEN_CONNS", "eight", "strconv"},
		{"max idle is not a number", "DB_MAX_IDLE_CONNS", "four", "strconv"},
		{"rate limit is not a number", "AUTH_RATE_LIMIT_PER_MIN", "ten", "strconv"},
		{"traveller rate limit is not a number", "TRAVELLER_RATE_LIMIT_PER_MIN", "six hundred", "strconv"},
		{"argon2 concurrency is not a number", "ARGON2_MAX_CONCURRENT", "two", "strconv"},
		{"max open is zero", "DB_MAX_OPEN_CONNS", "0", "database/sql reads 0 as UNLIMITED, which removes the ceiling DEC-21 sizes Argon2 against"},
		{"max open is negative", "DB_MAX_OPEN_CONNS", "-1", "same as 0 to database/sql"},
		{"max idle is negative", "DB_MAX_IDLE_CONNS", "-1", "database/sql reads any n<=0 as no idle connections; say 0 and mean it"},
		{"rate limit is zero", "AUTH_RATE_LIMIT_PER_MIN", "0", "a limit of zero refuses every login, which is an outage spelled as a setting"},
		{"traveller rate limit is zero", "TRAVELLER_RATE_LIMIT_PER_MIN", "0", "a limit of zero refuses every authenticated request, which is the app switched off"},
		{"argon2 concurrency is zero", "ARGON2_MAX_CONCURRENT", "0", "a zero-capacity semaphore blocks the first login forever"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, with(tc.key, tc.value))

			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load() = nil error with %s=%q, want an error (%s)", tc.key, tc.value, tc.why)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error does not name %s:\n%s", tc.key, err)
			}
		})
	}
}

// MEASURED, in $(go env GOROOT)/src/database/sql/sql.go, SetMaxIdleConns:
//
//	if db.maxOpen > 0 && db.maxIdleConnsLocked() > db.maxOpen {
//		db.maxIdleCount = db.maxOpen
//	}
//
// The clamp is SILENT and there is no way to observe it afterwards — sql.DBStats
// carries MaxOpenConnections and no idle counterpart. So DB_MAX_IDLE_CONNS=16
// against DB_MAX_OPEN_CONNS=4 is honoured as 4, nothing says so, and spec L21's
// "explicitly configure" becomes a call whose argument the runtime discards.
// Config is the only place that can refuse it.
func TestLoadRejectsMoreIdleConnectionsThanOpenOnes(t *testing.T) {
	env := complete()
	env["DB_MAX_OPEN_CONNS"] = "4"
	env["DB_MAX_IDLE_CONNS"] = "16"
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error with idle 16 > open 4, want an error")
	}
	for _, name := range []string{"DB_MAX_IDLE_CONNS", "DB_MAX_OPEN_CONNS"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s:\n%s", name, err)
		}
	}
}

func TestLoadAcceptsIdleEqualToOpen(t *testing.T) {
	env := complete()
	env["DB_MAX_OPEN_CONNS"] = "4"
	env["DB_MAX_IDLE_CONNS"] = "4"
	setEnv(t, env)

	if _, err := config.Load(); err != nil {
		t.Errorf("Load() = %v with idle 4 == open 4, want no error", err)
	}
}

// Zero idle connections is a real setting — database/sql reads it as "keep
// none" — and must not be swept up by the idle>open rule or by a >0 check.
func TestLoadAcceptsZeroIdleConnections(t *testing.T) {
	setEnv(t, with("DB_MAX_IDLE_CONNS", "0"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() = %v with DB_MAX_IDLE_CONNS=0, want no error", err)
	}
	if cfg.DBMaxIdleConns != 0 {
		t.Errorf("DBMaxIdleConns = %d, want 0", cfg.DBMaxIdleConns)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
