// TEST-FIRST (agent-graph-spec-V4 §6.7).
package config_test

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"travellog/internal/config"
)

// allVars is the seven variables the step text names, written out rather than
// read from the package.
var allVars = []string{
	"DATABASE_URL",
	"PORT",
	"LOG_LEVEL",
	"DB_MAX_OPEN_CONNS",
	"DB_MAX_IDLE_CONNS",
	"AUTH_RATE_LIMIT_PER_MIN",
	"TRAVELLER_RATE_LIMIT_PER_MIN",
	"PUBLIC_RATE_LIMIT_PER_MIN",
	"ARGON2_MAX_CONCURRENT",
	"REQUEST_TIMEOUT",
	"S3_INTERNAL_ENDPOINT",
	"S3_PUBLIC_BASE_URL",
	"S3_REGION",
	"S3_BUCKET",
	"S3_ACCESS_KEY",
	"S3_SECRET_KEY",
	"S3_PRESIGN_TTL_PRIVATE",
	"S3_PRESIGN_TTL_PUBLIC",
	"MEDIA_MAX_BYTES",
}

// complete is a whole environment Load must accept.
func complete() map[string]string {
	return map[string]string{
		"DATABASE_URL":                 "postgres://travellog:travellog@postgres:5432/travellog?sslmode=disable",
		"PORT":                         "8080",
		"LOG_LEVEL":                    "info",
		"DB_MAX_OPEN_CONNS":            "8",
		"DB_MAX_IDLE_CONNS":            "4",
		"AUTH_RATE_LIMIT_PER_MIN":      "10",
		"TRAVELLER_RATE_LIMIT_PER_MIN": "600",
		"PUBLIC_RATE_LIMIT_PER_MIN":    "120",
		"ARGON2_MAX_CONCURRENT":        "2",
		"REQUEST_TIMEOUT":              "15s",
		"S3_INTERNAL_ENDPOINT":         "http://minio:9000",
		"S3_PUBLIC_BASE_URL":           "http://127.0.0.1:9000",
		"S3_REGION":                    "us-east-1",
		"S3_BUCKET":                    "travellog-media",
		"S3_ACCESS_KEY":                "travellog",
		"S3_SECRET_KEY":                "travellogsecret",
		"S3_PRESIGN_TTL_PRIVATE":       "2m",
		"S3_PRESIGN_TTL_PUBLIC":        "15m",
		"MEDIA_MAX_BYTES":              "26214400",
	}
}

// setEnv makes `vars` the whole of what Load can see.
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

// the leg the step names.
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

// A variable set to the empty string is a variable that is not set.
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

// A caller that ignores the error must not receive a half-filled Config it
// can mistake for a working one.
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
		PublicRateLimitPerMin:    120,
		Argon2MaxConcurrent:      2,
		RequestTimeout:           15 * time.Second,
		S3InternalEndpoint:       "http://minio:9000",
		S3PublicBaseURL:          "http://127.0.0.1:9000",
		S3Region:                 "us-east-1",
		S3Bucket:                 "travellog-media",
		S3AccessKey:              "travellog",
		S3SecretKey:              "travellogsecret",
		S3PresignTTLPrivate:      2 * time.Minute,
		S3PresignTTLPublic:       15 * time.Minute,
		MediaMaxBytes:            26214400,
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

// Table-driven over every rejection.
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
		{"request timeout has no unit", "REQUEST_TIMEOUT", "15", "a bare number is ambiguous and ParseDuration refuses it, which is the point of a duration"},
		{"request timeout is not a duration", "REQUEST_TIMEOUT", "fifteen", "ParseDuration"},
		{"request timeout is zero", "REQUEST_TIMEOUT", "0s", "http.TimeoutHandler reads a non-positive duration as 'time out immediately', so every request answers 503"},
		{"request timeout is negative", "REQUEST_TIMEOUT", "-1s", "same as zero to http.TimeoutHandler"},
		{"request timeout is below the floor", "REQUEST_TIMEOUT", "500ms", "one Argon2id hash at 64 MiB does not finish inside it, so every sign-in answers 503"},
		{"request timeout is above the ceiling", "REQUEST_TIMEOUT", "120s", "a handler allowed to outlive the connection's own write deadline"},

		{"the internal endpoint has no scheme", "S3_INTERNAL_ENDPOINT", "minio:9000", "minio.New needs a host and a transport, and a bare host:port gives neither"},
		{"the internal endpoint is a scheme we do not speak", "S3_INTERNAL_ENDPOINT", "s3://minio:9000", "only http and https"},
		{"the internal endpoint has no host", "S3_INTERNAL_ENDPOINT", "http://", "a signature covers the host"},
		{"the public base has no scheme", "S3_PUBLIC_BASE_URL", "127.0.0.1:9000", "the phone connects to this one, and it is what the signature covers"},
		{"the public base is not a URL at all", "S3_PUBLIC_BASE_URL", "http://a b c", "url.Parse"},

		{"the private lifetime has no unit", "S3_PRESIGN_TTL_PRIVATE", "120", "a bare number is ambiguous and ParseDuration refuses it"},
		{"the private lifetime is below the signer's floor", "S3_PRESIGN_TTL_PRIVATE", "500ms", "minio-go refuses an expiry under a second"},
		{"the private lifetime is zero", "S3_PRESIGN_TTL_PRIVATE", "0s", "every minted URL would be expired on arrival"},
		{"the public lifetime is above the signer's ceiling", "S3_PRESIGN_TTL_PUBLIC", "169h", "SigV4 presigned URLs cap at seven days"},
		{"the public lifetime is not a duration", "S3_PRESIGN_TTL_PUBLIC", "fifteen", "ParseDuration"},

		{"the media bound is not a number", "MEDIA_MAX_BYTES", "twenty-five megabytes", "strconv"},
		{"the media bound is zero", "MEDIA_MAX_BYTES", "0", "a ceiling of zero refuses every upload, which is a feature switched off by a setting that reads like a safety measure"},
		{"the media bound is negative", "MEDIA_MAX_BYTES", "-1", "same as zero"},
		{"the media bound is below the fixture's own largest object", "MEDIA_MAX_BYTES", "555375", "one byte under hero-mountain.png, and `make seed` is where it would be found"},
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

// MEASURED, in $(go env GOROOT)/src/database/sql/sql.go, SetMaxIdleConns.
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
