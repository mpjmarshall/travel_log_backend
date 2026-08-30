// Package config is the only place in this repository that reads the process
// environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the whole of what the binary reads.
type Config struct {
	DatabaseURL              string
	Port                     string
	LogLevel                 slog.Level
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	AuthRateLimitPerMin      int
	TravellerRateLimitPerMin int

	PublicRateLimitPerMin int
	Argon2MaxConcurrent   int
	RequestTimeout        time.Duration

	S3InternalEndpoint string
	S3PublicBaseURL    string

	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	S3PresignTTLPrivate time.Duration
	S3PresignTTLPublic  time.Duration

	MediaMaxBytes int64
}

// MinRequestTimeout and MaxRequestTimeout bound REQUEST_TIMEOUT, and both
// ends are refusals of a value that would silently switch something off.
const (
	MinRequestTimeout = time.Second
	MaxRequestTimeout = 60 * time.Second
)

// MinPresignTTL and MaxPresignTTL bound both presign lifetimes, and both ends
// are the signer's own rather than a policy invented here.
const (
	MinPresignTTL = time.Second
	MaxPresignTTL = 7 * 24 * time.Hour
)

// MinMediaMaxBytes is a measurement and not a round number.
const MinMediaMaxBytes = 1 << 20

// Load reads the environment and returns either a whole Config or a single
// error naming every variable that is missing or invalid.
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
		PublicRateLimitPerMin:    l.atLeast("PUBLIC_RATE_LIMIT_PER_MIN", 1),
		Argon2MaxConcurrent:      l.atLeast("ARGON2_MAX_CONCURRENT", 1),
		RequestTimeout:           l.duration("REQUEST_TIMEOUT", MinRequestTimeout, MaxRequestTimeout),
		S3InternalEndpoint:       l.address("S3_INTERNAL_ENDPOINT"),
		S3PublicBaseURL:          l.address("S3_PUBLIC_BASE_URL"),
		S3Region:                 l.required("S3_REGION"),
		S3Bucket:                 l.required("S3_BUCKET"),
		S3AccessKey:              l.required("S3_ACCESS_KEY"),
		S3SecretKey:              l.required("S3_SECRET_KEY"),
		S3PresignTTLPrivate:      l.duration("S3_PRESIGN_TTL_PRIVATE", MinPresignTTL, MaxPresignTTL),
		S3PresignTTLPublic:       l.duration("S3_PRESIGN_TTL_PUBLIC", MinPresignTTL, MaxPresignTTL),
		MediaMaxBytes:            l.bytes("MEDIA_MAX_BYTES", MinMediaMaxBytes),
	}

	l.refuseSilentIdleClamp(cfg)

	if err := l.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// refuseSilentIdleClamp rejects an idle count above the open count.
func (l *loader) refuseSilentIdleClamp(cfg Config) {
	if !l.broke("DB_MAX_OPEN_CONNS") && !l.broke("DB_MAX_IDLE_CONNS") &&
		cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		l.add("DB_MAX_IDLE_CONNS", fmt.Sprintf(
			"%d exceeds DB_MAX_OPEN_CONNS=%d; database/sql would clamp it to %d in silence",
			cfg.DBMaxIdleConns, cfg.DBMaxOpenConns, cfg.DBMaxOpenConns))
	}
}

// loader accumulates problems instead of returning on's first one.
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
// helper goes through it.
func (l *loader) required(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		l.add(name, "not set")
	}
	return v
}

// port parses a listen port and refuses anything outside 1-65535.
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

// atLeast parses an integer and enforces a floor.
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

// duration parses a Go duration and enforces both ends.
func (l *loader) duration(name string, floor, ceiling time.Duration) time.Duration {
	v := l.required(name)
	if l.broke(name) {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.add(name, fmt.Sprintf("%q is not a duration — write it with a unit, like 15s", v))
		return 0
	}
	if d < floor {
		l.add(name, fmt.Sprintf(
			"%s is below the minimum of %s; http.TimeoutHandler reads a non-positive "+
				"duration as 'time out immediately', and one Argon2id hash at 64 MiB "+
				"does not finish inside a fraction of a second either", d, floor))
		return 0
	}
	if d > ceiling {
		l.add(name, fmt.Sprintf(
			"%s is above the maximum of %s, which is the server's own write deadline "+
				"— a handler allowed to outlive it is a handler whose work is "+
				"discarded underneath it", d, ceiling))
		return 0
	}
	return d
}

// address parses one of's two bucket addresses and refuses anything
// minio.New could not act on.
func (l *loader) address(name string) string {
	v := l.required(name)
	if l.broke(name) {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil {
		l.add(name, fmt.Sprintf("%q is not a URL: %v", v, err))
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		l.add(name, fmt.Sprintf(
			"%q has no http:// or https:// scheme; minio.New takes a host AND a "+
				"transport, and a bare host:port gives it neither", v))
		return ""
	}
	if u.Host == "" {
		l.add(name, fmt.Sprintf(
			"%q names no host, and a SigV4 signature covers the host", v))
		return ""
	}
	return v
}

// bytes parses a byte count and enforces a floor.
func (l *loader) bytes(name string, floor int64) int64 {
	v := l.required(name)
	if l.broke(name) {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		l.add(name, fmt.Sprintf("%q is not a number of bytes", v))
		return 0
	}
	if n < floor {
		l.add(name, fmt.Sprintf(
			"%d is below the minimum of %d; the fixture's own larger photograph is "+
				"555,376 bytes, so a ceiling under a megabyte is a build that cannot "+
				"store its own seed data", n, floor))
		return 0
	}
	return n
}
