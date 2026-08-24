// Package config is the only place in this repository that reads the process
// environment.
//
// go_backend.md L30: "Read all configuration (DB DSN, ports) strictly via
// os.Getenv(). No third-party config managers in the Go code." The monopoly is
// enforced by a mechanism rather than a convention — sweep_test.go walks every
// non-test file's AST and asserts the set of callers is exactly this file.
//
// TWO THINGS ABOUT Load() THAT ARE DECISIONS RATHER THAN STYLE:
//
//   - It reports EVERY problem, not the first. A first-failure Load makes a
//     fresh checkout a sequence of restarts, each revealing one more missing
//     variable; eight variables is eight runs. One error is one run.
//
//   - Nothing has a default. The client project's own house style is the
//     precedent — apiBaseUrlProvider throws until it is overridden rather than
//     carrying a placeholder, because "a placeholder host would fail later as
//     a connection error". A defaulted DB_MAX_OPEN_CONNS is a pool size nobody
//     chose, silently in force on a VPS. deploy/docker-compose.yml sets all
//     eight with defaults of its own, which is where a default belongs: in the
//     deployment, visible, beside the thing it configures.
//
// A consequence worth stating: `make run` on a bare host now fails, naming all
// eight. That is the design working. `make up` is the supported path.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config is the whole of what the binary reads. The parent plan's S03 lists
// fifteen variables; VS2 named seven, the limiter fix added an eighth, and the
// rest arrive with the steps that read them.
//
// Port is kept as a string because that is what it is used as — ":"+Port. It is
// nonetheless parsed and range-checked by loader.port, so "http" and "65536"
// are refused here rather than by the kernel at Listen time.
//
// THE TWO RATE LIMITS ARE TWO CEILINGS ON TWO DIFFERENT THINGS, and
// TravellerRateLimitPerMin is a second variable rather than a second use of
// AuthRateLimitPerMin. The credential ceiling bounds an unauthenticated
// 64 MiB-per-attempt Argon2 surface and is deliberately low; the authenticated
// one bounds a stolen token against a thirty-day session TTL with no revocation
// surface, so it has to be high enough that no honest client ever meets it. One
// number cannot be both, and reusing the low one is a phone that stops syncing.
type Config struct {
	DatabaseURL              string
	Port                     string
	LogLevel                 slog.Level
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	AuthRateLimitPerMin      int
	TravellerRateLimitPerMin int
	Argon2MaxConcurrent      int
}

// Load reads the environment and returns either a whole Config or a single
// error naming every variable that is missing or invalid. It never returns a
// partly-filled Config beside an error.
//
// Field order in the composite literal below is evaluation order in Go, so
// problems come out in the order deploy/.env.example lists the variables.
func Load() (Config, error) {
	var l loader

	cfg := Config{
		DatabaseURL:              l.required("DATABASE_URL"),
		Port:                     l.port("PORT"),
		LogLevel:                 l.level("LOG_LEVEL"),
		DBMaxOpenConns:           l.atLeast("DB_MAX_OPEN_CONNS", 1),
		DBMaxIdleConns:           l.atLeast("DB_MAX_IDLE_CONNS", 0),
		AuthRateLimitPerMin:      l.atLeast("AUTH_RATE_LIMIT_PER_MIN", 1),
		TravellerRateLimitPerMin: l.atLeast("TRAVELLER_RATE_LIMIT_PER_MIN", 1),
		Argon2MaxConcurrent:      l.atLeast("ARGON2_MAX_CONCURRENT", 1),
	}

	l.refuseSilentIdleClamp(cfg)

	if err := l.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// refuseSilentIdleClamp rejects an idle count above the open count.
//
// MEASURED, in $(go env GOROOT)/src/database/sql/sql.go, SetMaxIdleConns:
//
//	if db.maxOpen > 0 && db.maxIdleConnsLocked() > db.maxOpen {
//		db.maxIdleCount = db.maxOpen
//	}
//
// The clamp is silent and UNOBSERVABLE afterwards — sql.DBStats carries
// MaxOpenConnections and has no idle counterpart, so nothing in the running
// process can report that the number it was handed was discarded. spec L21 asks
// for the pool to be configured EXPLICITLY, and a call whose argument the
// runtime quietly overrides is the opposite of that. This is the only place the
// disagreement can be seen, so it is refused here.
func (l *loader) refuseSilentIdleClamp(cfg Config) {
	if !l.broke("DB_MAX_OPEN_CONNS") && !l.broke("DB_MAX_IDLE_CONNS") &&
		cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		l.add("DB_MAX_IDLE_CONNS", fmt.Sprintf(
			"%d exceeds DB_MAX_OPEN_CONNS=%d; database/sql would clamp it to %d in silence",
			cfg.DBMaxIdleConns, cfg.DBMaxOpenConns, cfg.DBMaxOpenConns))
	}
}

// loader accumulates problems instead of returning on the first one.
type loader struct {
	problems []string
	failed   map[string]bool
}

func (l *loader) add(name, why string) {
	if l.failed == nil {
		l.failed = map[string]bool{}
	}
	l.failed[name] = true
	l.problems = append(l.problems, fmt.Sprintf("  %s: %s", name, why))
}

func (l *loader) broke(name string) bool { return l.failed[name] }

func (l *loader) err() error {
	if len(l.problems) == 0 {
		return nil
	}
	noun := "problems"
	if len(l.problems) == 1 {
		noun = "problem"
	}
	head := fmt.Sprintf("config: %d %s with the environment:\n", len(l.problems), noun)
	return errors.New(head + strings.Join(l.problems, "\n"))
}

// required is the only reader of the environment in this package; every other
// helper goes through it. os.Getenv — which spec L30 mandates — cannot
// distinguish unset from set-to-empty, and it does not need to: `FOO=` in a
// compose file or a .env is a variable nobody has filled in, which is the same
// thing as one nobody has written.
//
// The value is never echoed into a problem. DATABASE_URL carries a password.
func (l *loader) required(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		l.add(name, "not set")
	}
	return v
}

// port parses a listen port and refuses anything outside 1-65535.
//
// Port 0 is legal to the kernel and wrong here: it asks for any free port, and
// the container publishes a fixed one, so the API would come up listening
// somewhere Docker is not forwarding to.
func (l *loader) port(name string) string {
	v := l.required(name)
	if l.broke(name) {
		return ""
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.add(name, fmt.Sprintf("%q is not a number", v))
		return ""
	}
	if n < 1 || n > 65535 {
		l.add(name, fmt.Sprintf("%d is outside 1-65535", n))
		return ""
	}
	return v
}

func (l *loader) level(name string) slog.Level {
	v := l.required(name)
	if l.broke(name) {
		return 0
	}
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	l.add(name, fmt.Sprintf("%q is not one of debug, info, warn, error", v))
	return 0
}

// atLeast parses an integer and enforces a floor. The floors are not tidiness.
// Each rejects a value that database/sql or this application reads as
// something other than what it says:
//
//   - DB_MAX_OPEN_CONNS at 0 means UNLIMITED to database/sql, which removes the
//     ceiling DEC-21 sizes Argon2's 64 MiB-per-login against. Floor 1.
//   - DB_MAX_IDLE_CONNS at 0 genuinely means "keep none" and is allowed;
//     anything below 0 is read as 0 as well, so it is refused rather than
//     silently reinterpreted. Floor 0.
//   - ARGON2_MAX_CONCURRENT at 0 is a zero-capacity semaphore, which blocks the
//     first login forever instead of refusing it. DEC-48 rejects queueing
//     precisely because it turns memory exhaustion into timeout exhaustion,
//     and 0 is the worst case of that. Floor 1.
//   - AUTH_RATE_LIMIT_PER_MIN at 0 refuses every login: an outage spelled as a
//     setting. Floor 1.
//   - TRAVELLER_RATE_LIMIT_PER_MIN at 0 refuses every authenticated request,
//     which is the whole application switched off by a setting that reads like
//     a safety measure. Floor 1.
func (l *loader) atLeast(name string, floor int) int {
	v := l.required(name)
	if l.broke(name) {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.add(name, fmt.Sprintf("%q is not a number", v))
		return 0
	}
	if n < floor {
		l.add(name, fmt.Sprintf("%d is below the minimum of %d", n, floor))
		return 0
	}
	return n
}
