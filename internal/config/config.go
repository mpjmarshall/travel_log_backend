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
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
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
	RequestTimeout           time.Duration

	// THE BUCKET GROUP, and it is nine variables rather than four because
	// object storage is the first thing this application talks to that is not
	// the database. What makes MinIO locally and real S3 in production
	// CONFIGURATION rather than code is that every one of these is here.
	//
	// TWO ADDRESSES AND NOT ONE (DEC-42). S3InternalEndpoint is what the API
	// DIALS — inside compose that is http://minio:9000 — and S3PublicBaseURL
	// is what a SIGNATURE COVERS and what a phone connects to. A SigV4
	// signature covers the host, so an address baked into signing code cannot
	// be changed downstream at all: a proxy cannot rewrite it, a Host header
	// cannot correct it, a CNAME cannot rescue it. They are the same value in
	// every deployment except a containerised local stack, and BOTH ARE
	// REQUIRED rather than one defaulting to the other — this file's rule is
	// that nothing has a default, and "the internal endpoint is the public one
	// unless you say otherwise" is exactly the silent value nobody chose that
	// the rule exists to refuse.
	S3InternalEndpoint string
	S3PublicBaseURL    string

	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	// TWO LIFETIMES, TWO AUDIENCES (DEC-47). The private one is the phone's,
	// and it is the revocation knob (DEC-44); the public one is what
	// GET /l/{token} embeds and is fifteen minutes (DEC-84), because the
	// envelope has nothing to re-mint its URLs with. They are configurable
	// because a lifetime that is not closes DEC-21's tuning question by
	// accident, and they are two variables because one number cannot be both.
	S3PresignTTLPrivate time.Duration
	S3PresignTTLPublic  time.Duration

	// MediaMaxBytes is an API-SIDE REFUSAL TO MINT and is not what the bucket
	// enforces. SigV4 signs an exact header value and never a range, so what
	// the bucket enforces is `== the declared byteSize`; this is what stops a
	// capability being minted for an absurd one in the first place. Both
	// sentences are needed and neither is true alone — internal/media's
	// Upload.ByteSize carries the long form.
	MediaMaxBytes int64
}

// MinRequestTimeout and MaxRequestTimeout bound REQUEST_TIMEOUT, and both ends
// are refusals of a value that would silently switch something off.
//
// THE FLOOR IS ARGON2'S. DEC-08's parameters are 64 MiB, t=1, p=4, and one
// hash does not finish in half a second on modest hardware — so a sub-second
// request bound is a build where every sign-in answers 503 and nothing in the
// logs says why. One second is the smallest number that is not that.
//
// THE CEILING IS THE CONNECTION'S. cmd/api's `writeTimeout` is 60s, and a
// handler allowed to run longer than the response may take to be written is a
// handler whose work is discarded by the server underneath it. cmd/api has a
// leg asserting these two agree, because they live in two files and the
// relationship is invisible from either one.
const (
	MinRequestTimeout = time.Second
	MaxRequestTimeout = 60 * time.Second
)

// MinPresignTTL and MaxPresignTTL bound both presign lifetimes, and BOTH ENDS
// ARE THE SIGNER'S OWN rather than a policy invented here.
//
// MEASURED against minio-go: an expiry below one second answers "Expires
// cannot be lesser than 1 second" and one above seven days answers "Expires
// cannot be greater than 7 days" — at PRESIGN time, which means every media
// route 500s at its first request on a value nothing refused at boot. That is
// the same argument REQUEST_TIMEOUT's bounds make: refuse the value that
// silently switches something off, where it can still be reported.
//
// THE CEILING IS NOT A POLICY AND MUST NOT BE READ AS ONE. Seven days is what
// SigV4 permits; it is NOT what "Stop sharing" can honestly promise.
// S3_PRESIGN_TTL_PUBLIC is the revocation window four sentences of client copy
// are written against (DEC-84), so raising it is a copy change and not a knob
// turn. deploy/.env.example says so beside the value.
const (
	MinPresignTTL = time.Second
	MaxPresignTTL = 7 * 24 * time.Hour
)

// MinMediaMaxBytes is a MEASUREMENT and not a round number. The fixture's
// larger object is `internal/logbook/testdata/imagery/hero-mountain.png` at
// 555,376 bytes, so a ceiling below a megabyte is a build that cannot store
// its own seed data — and R4's `make seed` is where that would be discovered,
// as an upload refused by the API that wrote the refusal.
//
// THERE IS NO CEILING ON THE CEILING, deliberately. The API never buffers a
// photograph — DEC-36's whole point is that the bytes go straight to the
// bucket — so a large value costs bucket space rather than memory, and nothing
// in R1-R8 reclaims an object (OE-12). docs/BEFORE-A-PUBLIC-DEPLOY.md carries
// that arithmetic; a number invented here would not be one anybody chose.
const MinMediaMaxBytes = 1 << 20

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

// duration parses a Go duration and enforces both ends.
//
// IT IS A DURATION AND NOT A NUMBER OF SECONDS, and that is the one decision in
// this helper. Every other numeric variable here carries its unit in its name
// — DB_MAX_OPEN_CONNS, AUTH_RATE_LIMIT_PER_MIN — and REQUEST_TIMEOUT does not,
// so a bare `15` would be a value whose unit lives only in a comment. It also
// buys a real refusal: time.ParseDuration rejects `15`, so the ambiguous
// spelling is an error at boot rather than a build that times out after
// fifteen nanoseconds.
//
// A NON-POSITIVE VALUE IS REFUSED RATHER THAN READ AS "no timeout", which is
// the reading somebody expects and is the opposite of what happens:
// http.TimeoutHandler with a duration <= 0 times out IMMEDIATELY, so `0s`
// would be every request answering 503. The floor below catches it, and the
// message says which of the two readings the code takes.
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

// address parses one of DEC-42's two bucket addresses and refuses anything
// minio.New could not act on.
//
// THE SCHEME IS REQUIRED RATHER THAN DEFAULTED, and that is the decision in
// this helper. minio.New takes a HOST and a boolean saying whether to speak
// TLS, so `minio:9000` carries neither: guessing http would be a silent choice
// of transport, and guessing wrong is a signature that fails against a server
// that is right there. A signature also covers the HOST, so an address with no
// host is a URL nothing can be signed for.
//
// The value IS echoed into a problem, unlike DATABASE_URL: these two carry no
// credential — S3_ACCESS_KEY and S3_SECRET_KEY are separate variables — and an
// address you cannot see is an address you cannot fix.
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

// bytes parses a byte count and enforces a floor. It is int64 rather than int
// because it is compared against a file size, and a 32-bit int would make the
// bound wrap on a platform this could plausibly run on.
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
