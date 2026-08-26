// Command api is the travellog HTTP server.
//
// VS2 replaced VS1's placeholder. What VS1 wrote and this file keeps, because
// both are load-bearing for the `scratch` runtime image rather than
// conveniences:
//
//   - the blank import of time/tzdata. Stage 2 of the Dockerfile is `scratch`,
//     which carries no zoneinfo, so without this every time.LoadLocation fails
//     in the shipped image and nothing fails on a developer's machine.
//   - the -healthcheck flag. scratch has no shell and no curl, so a Docker
//     HEALTHCHECK has nothing to invoke except this binary.
//
// WHAT VS2 CHANGED, AND THE TWO CALLS THAT ARE DECISIONS:
//
//   - THE -addr FLAG IS GONE. The listen address is ":"+cfg.Port and PORT comes
//     from the environment, because go_backend.md L30 says configuration —
//     naming ports specifically — is read "strictly via os.Getenv()". A flag
//     setting the port is a second configuration path for the same value, and
//     two paths is how the probe ends up asking a different port than the
//     server answers on. -healthcheck therefore loads the config too, and that
//     is coherent rather than heavy: HEALTHCHECK inherits the container's
//     environment, and a config the server could not load is a server that is
//     not running.
//
//   - /healthz PINGS THE DATABASE. VS1's answered a constant, so a container
//     whose database had gone was reported healthy by Docker and kept taking
//     traffic. Nothing else in the repository can tell those two states apart.
//
// go_backend.md L20 asks for pgx "solely as a blank import driver". This is the
// only import of it in the binary and it is blank; imports_test.go asserts all
// three of those claims by walking the AST, because a grep can see the line but
// not the underscore.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "time/tzdata"

	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/httpapi"
	"travellog/internal/httpx"
	"travellog/internal/logging"
	"travellog/internal/media"
	"travellog/internal/postgres"
	"travellog/migrations"
)

// startupPingTimeout is allowed longer than the per-request one: compose gates
// the api on `postgres: service_healthy`, so a slow answer here is a cold
// database rather than a broken one.
const startupPingTimeout = 10 * time.Second

// healthzPingTimeout is /healthz's own ping. It must come in under the container
// HEALTHCHECK's 3s timeout, or a wedged database turns a probe that should
// report unhealthy into one that never answers at all.
const healthzPingTimeout = 2 * time.Second

const shutdownTimeout = 10 * time.Second

// migrateTimeout bounds the WHOLE migration run, the wait for the advisory
// lock included. Generous because that wait is legitimate — a second replica
// booting behind the first should wait — and bounded because a lock nobody
// releases must not become a container that hangs for ever.
const migrateTimeout = 2 * time.Minute

// bucketTimeout bounds the boot-time bucket check. It is the startup ping's
// budget for the same reason: compose gates the api on `minio: service_healthy`
// (deploy/docker-compose.yml), so a slow answer here is a cold object store
// rather than a broken one — and an unbounded one is a container that hangs
// for ever rather than reporting a misconfiguration.
const bucketTimeout = 10 * time.Second

// main parses the two flags, loads the config and dispatches.
//
// A config that fails to load is written with fmt rather than through the
// logger: the logger's level comes from the config that has just failed, so
// there is nothing to build one from.
func main() {
	healthcheck := flag.Bool(
		"healthcheck",
		false,
		"probe /healthz on the local server and exit 0 or 1; "+
			"this is what the container HEALTHCHECK runs, because scratch has no shell",
	)
	migrateOnly := flag.Bool(
		"migrate-only",
		false,
		"apply migrations and exit; the server does this at boot too, so this "+
			"is for `make migrate` and for a deploy that wants them separated",
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	addr := ":" + cfg.Port

	if *healthcheck {
		os.Exit(probe(addr))
	}

	log := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(log)

	if *migrateOnly {
		if err := migrateOnlyRun(cfg, log); err != nil {
			log.Error("api: migrate-only failed", slog.String("err", err.Error()))
			os.Exit(1)
		}
		return
	}

	if err := run(cfg, addr, log); err != nil {
		log.Error("api: stopping", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

// run owns the database handle for the process's lifetime.
//
// TWO THINGS IT DOES BEFORE IT MIGRATES AND LISTENS, AND BOTH ARE DECISIONS:
//
//   - It configures the pool explicitly, which go_backend.md L21 asks for by
//     name: "Explicitly configure connection pooling in main.go using
//     SetMaxOpenConns() and SetMaxIdleConns()." Both values come from config,
//     which has already refused an idle count above the open one — database/sql
//     clamps that silently and sql.DBStats has no field that would ever report
//     it.
//   - It PINGS, because sql.Open does not connect. Without it the first request
//     is where a wrong DSN is discovered, and it is discovered as a 503 rather
//     than as a startup failure. Compose already gates this container on
//     postgres being healthy, so a failure here is a real misconfiguration and
//     the process should not come up pretending otherwise.
func run(cfg config.Config, addr string, log *slog.Logger) error {
	dsn, err := postgres.WithSessionOptions(cfg.DatabaseURL, cfg.RequestTimeout)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer func() { _ = db.Close() }()

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)

	pingCtx, cancel := context.WithTimeout(context.Background(), startupPingTimeout)
	defer cancel()
	// THE ELAPSED TIME, NOT ONLY THE BUDGET (OPS-MIN-11). This used to answer
	// "the database did not answer within 10s" for a DNS failure that took
	// 250ms, so a fast failure read as a slow one and the first thing an
	// operator does is go looking for a network stall that is not there. The
	// budget stays as a second field, because "247ms" alone does not say
	// whether it gave up or was refused.
	started := time.Now()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("the database did not answer (waited %s of a %s budget): %w",
			time.Since(started).Round(time.Millisecond), startupPingTimeout, err)
	}
	log.Info("database ready",
		slog.Int("maxOpenConns", cfg.DBMaxOpenConns),
		slog.Int("maxIdleConns", cfg.DBMaxIdleConns),
	)

	migCtx, migCancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer migCancel()
	if err := migrateUp(migCtx, db, log); err != nil {
		return err
	}

	// THE BUCKET, ON THE SAME ARGUMENT AS THE PING ABOVE (DEC-98).
	//
	// R2 DISCARDED THE STORE THIS ANSWERS AND R3 KEEPS IT. R2's own comment
	// said the discard was honest rather than sloppy, and gave the reason it
	// cost nothing: the region is pinned, so there is no location cache to
	// warm and a second store would be as cold and as offline as this one.
	// That reasoning still holds — what changed is that there are now three
	// routes that presign, so a second construction would be a second place
	// for the nine S3_* values to be read.
	//
	// What it buys is the two things a healthcheck cannot see. Nothing else
	// creates the bucket: the official image auto-creates nothing and
	// `/minio/health/ready` answers 200 with zero buckets, so `up --wait`
	// reporting three healthy services says nothing about whether a
	// photograph can be stored. And presigning is offline arithmetic, so a
	// wrong S3_SECRET_KEY signs just as happily as a right one — this is the
	// only call in the boot path that a bad credential fails.
	//
	// MEASURED BY DELETING THE THREE LINES BELOW: `up --wait` still reported
	// three healthy services, `mc ls local/` answered ZERO buckets, and the
	// only thing that reddened was the arc's own A2b. `make check` STAYED
	// GREEN — media_test.go guards what mediaStore DOES and cannot see
	// whether run() calls it — so the arc is the only guard on this call
	// site, and that is stated rather than assumed.
	bucketCtx, bucketCancel := context.WithTimeout(context.Background(), bucketTimeout)
	defer bucketCancel()
	objects, err := mediaStore(bucketCtx, cfg, log)
	if err != nil {
		return err
	}

	mount, err := apiRoutes(cfg, db, log, objects)
	if err != nil {
		return err
	}
	return serve(addr, serverChain(newMux(db, log, mount), log, cfg.RequestTimeout), log)
}

// pinger is the half of *sql.DB that /healthz needs.
//
// The narrow interface is what makes the database-down branch reachable from a
// test WITHOUT a database, and that branch is the entire point of VS2's
// /healthz. A handler taking *sql.DB would put its only interesting leg behind
// a TEST_DATABASE_URL skip, which is where legs go to stop being run.
type pinger interface {
	PingContext(ctx context.Context) error
}

// migrateUp applies migrations/*.up.sql, at boot and behind the advisory lock.
//
// It is one line of work and a paragraph of reason. It runs here rather than in
// a separate deploy step so that a container which is running is a container
// whose schema is current — the property `docker compose up -d` has to have for
// the slice arc to mean anything. A second replica starting at the same time
// waits rather than racing, and a checksum mismatch is a process that refuses
// to come up rather than one serving against a schema it does not believe in.
func migrateUp(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	applied, err := postgres.Migrator{Logger: log}.Migrate(ctx, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info("migrations up to date", slog.Int("applied", len(applied)))
	return nil
}

// migrateOnlyRun is `-migrate-only`: open, pool, ping, migrate, stop. It starts
// no listener, so `make migrate` cannot leave a server behind.
func migrateOnlyRun(cfg config.Config, log *slog.Logger) error {
	// THE SAME SESSION SETTINGS AS run(), AND THE statement_timeout IS WHY THIS
	// IS NOT A COPY-PASTE HAZARD: a migration statement that runs longer than a
	// request budget is normal, and it is bounded by migrateTimeout and by
	// migrateLockTimeout instead. The Migrator sets its own lock_timeout on its
	// own pinned connection; what this shares with run() is the search_path
	// pin, which is the setting a migration must not disagree with the
	// application about.
	dsn, err := postgres.WithSessionOptions(cfg.DatabaseURL, migrateTimeout)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("the database did not answer within %s: %w", migrateTimeout, err)
	}
	return migrateUp(ctx, db, log)
}

// newMux builds the server's routing table.
//
// EXTRACTED FROM serve AT VS1-BACKFILL because serve blocks until a signal and
// hands its listener to ListenAndServe, so there is no way to reach the handler
// from a test without signalling the test process itself. newMux is what
// httptest.NewServer takes.
// THE VARIADIC IS WHAT KEPT TWELVE LEGS FROM BEING REWRITTEN. newMux is called
// from a dozen places in this package's tests, every one of them about
// /healthz and none of them about auth; a second required parameter would have
// edited all twelve to pass a nil they do not care about, and an edit that
// large across legs it does not concern is how a leg gets changed by accident.
// healthzPath is written once because two things read it: the route below and
// the access log's quiet list. Two spellings would mean a probe that is
// mounted and never demoted.
const healthzPath = "/healthz"

func newMux(db pinger, log *slog.Logger, mounts ...func(*http.ServeMux)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, healthzHandler(db, log))
	for _, mount := range mounts {
		mount(mux)
	}
	return mux
}

// apiRoutes builds the auth service from the config and answers something that
// mounts its routes.
//
// IT REFUSES A CEILING BELOW ONE RATHER THAN CARRYING ON. config.Load already
// floors ARGON2_MAX_CONCURRENT at 1, so this is unreachable through main —
// which is exactly why it is here: the guard that only exists in the caller is
// the guard the second caller does not get.
//
// THE ARGON2 PARAMETERS ARE DEC-08's UNTUNED 64 MiB / t=1 / p=4 (DEC-21). What
// bounds the memory is not the parameters, it is the two guards around them:
// AUTH_RATE_LIMIT_PER_MIN counts callers per address and the gate counts calls.
func apiRoutes(cfg config.Config, db *sql.DB, log *slog.Logger, objects media.Store) (func(*http.ServeMux), error) {
	gate, err := auth.NewGate(cfg.Argon2MaxConcurrent)
	if err != nil {
		return nil, fmt.Errorf("ARGON2_MAX_CONCURRENT: %w", err)
	}
	service := &auth.Service{
		Store:  postgres.AuthStore{DB: db},
		Hasher: auth.Capped{Hasher: auth.Argon2id{Params: auth.DefaultParams}, Gate: gate},
	}
	credential, traveller, public := limiters(cfg)

	return func(mux *http.ServeMux) {
		httpapi.Mount(mux, httpapi.Deps{
			Auth:    service,
			Logbook: postgres.LogbookStore{DB: db},
			// THE SHARE PORT IS A SECOND TYPE OVER THE SAME POOL (R5). Both
			// are `struct{ DB *sql.DB }`, so this line costs nothing at boot;
			// what it buys is that the interface each handler is handed says
			// what that handler can reach — the share handlers cannot read the
			// whole log, and the logbook handlers cannot mint a capability.
			Share: postgres.ShareStore{DB: db},
			// THE GEOGRAPHY GROUP (R6), on the same argument and for the same
			// price: two more `struct{ DB *sql.DB }` over the same pool, so
			// the city handler cannot remove a place and the place handlers
			// cannot attach anything to a trip.
			Cities: postgres.CityStore{DB: db},
			Places: postgres.PlaceStore{DB: db},
			// THE PHOTOGRAPH AND WALK PORTS (R7), same argument, same price.
			// The walk route cannot delete a photograph and the photo routes
			// cannot empty a track — and there is no route anywhere that can
			// delete a walk, because nothing in this app authorises destroying
			// a recording of a day.
			Photos: postgres.PhotoStore{DB: db},
			Walks:  postgres.WalkStore{DB: db},
			// THE PUBLIC READ'S PORT (R8), same argument, same price — and it
			// is the one whose interface matters most, because it is the only
			// handler in the API that no bearer token stands in front of. It
			// can resolve a token digest and read one trip's published rows,
			// and it can do nothing else at all.
			Public:         postgres.ShareReadStore{DB: db},
			Log:            log,
			AuthLimit:      credential,
			TravellerLimit: traveller,
			// THE THIRD CEILING, ITS OWN INSTANCE (PD-09). Sharing the
			// credential bucket would mean one person reading a shared trip
			// locks everybody out of signing in.
			PublicLimit: public,
			// THE MEDIA GROUP (R3). Two ports and one number, and Mount panics
			// on any of the three being absent — for the reason it panics on a
			// nil limiter: an optional field left unset reads as working
			// software right up until somebody uploads a photograph.
			//
			// `objects` IS THE STORE `run` ALREADY BUILT AND ALREADY PROVED.
			// It is the only thing in the boot path that a wrong
			// S3_SECRET_KEY fails, because presigning is offline arithmetic
			// and a wrong secret signs just as happily as a right one.
			Media:         postgres.MediaStore{DB: db},
			Objects:       objects,
			MediaMaxBytes: cfg.MediaMaxBytes,
		})
	}, nil
}

// limiters builds the API's THREE ceilings from the three variables that carry
// them.
//
// IT IS A FUNCTION OF ITS OWN SO THAT A TEST CAN SPEND FROM THEM. Which
// variable feeds which limiter is invisible from outside the process — a
// swapped pair gives the credential routes a ceiling of 600 and the
// authenticated ones a ceiling of 10, and every leg about status codes passes.
// Mount's nil panic is what guards the wiring; this is what guards the
// arithmetic.
//
// THREE AND NOT TWO SINCE R8, AND THEY ARE THREE INSTANCES AND NOT ONLY THREE
// NUMBERS (PD-09). `GET /l/{token}` is unauthenticated and is not a credential
// attempt, so it needs its own budget AND its own bucket: sharing the
// credential instance would put one person browsing a shared trip in the same
// 10/min allowance as everybody's sign-in.
func limiters(cfg config.Config) (credential, traveller, public *httpx.Limiter) {
	return httpx.NewLimiter(cfg.AuthRateLimitPerMin, nil),
		httpx.NewLimiter(cfg.TravellerRateLimitPerMin, nil),
		httpx.NewLimiter(cfg.PublicRateLimitPerMin, nil)
}

// mediaConfig is the nine S3_* variables becoming the store's own Config, and
// it is a function of its own for the reason limiters() is one.
//
// WHICH ADDRESS FEEDS WHICH HALF IS INVISIBLE FROM OUTSIDE THE PROCESS
// (DEC-42). Swap the two and the server is perfectly healthy, every request
// succeeds, and every presigned URL is minted for `minio:9000` — a host only
// something inside the compose network can resolve, and a SigV4 signature
// covers the host, so nothing downstream can correct it. The same holds for
// the two lifetimes: reaching for the private one where the public belongs is
// a share page that dies mid-scroll, and no status code reports it. This is
// what media_test.go asserts on.
func mediaConfig(cfg config.Config) media.Config {
	return media.Config{
		InternalEndpoint: cfg.S3InternalEndpoint,
		PublicBaseURL:    cfg.S3PublicBaseURL,
		Region:           cfg.S3Region,
		Bucket:           cfg.S3Bucket,
		AccessKey:        cfg.S3AccessKey,
		SecretKey:        cfg.S3SecretKey,
		TTLPrivate:       cfg.S3PresignTTLPrivate,
		TTLPublic:        cfg.S3PresignTTLPublic,
	}
}

// mediaStore builds the store AND creates its bucket, and the second half is
// the point: building a client is offline and so is presigning, so a boot that
// stopped at the constructor would come up healthy against a bucket that does
// not exist and credentials that do not work.
func mediaStore(ctx context.Context, cfg config.Config, log *slog.Logger) (media.Store, error) {
	store, err := media.New(mediaConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("the media store: %w", err)
	}
	started := time.Now()
	if err := store.EnsureBucket(ctx); err != nil {
		// The elapsed time as well as the budget, for the reason the startup
		// ping carries both (OPS-MIN-11): a DNS failure that took 250ms and a
		// server that never answered are different problems, and only the
		// budget was ever printed.
		return nil, fmt.Errorf("the bucket %q at %s did not answer (waited %s of a %s budget): %w",
			cfg.S3Bucket, cfg.S3InternalEndpoint,
			time.Since(started).Round(time.Millisecond), bucketTimeout, err)
	}
	log.Info("bucket ready",
		slog.String("bucket", cfg.S3Bucket),
		slog.String("endpoint", cfg.S3InternalEndpoint),
	)
	return store, nil
}

// serverChain is httpx.Base plus two, and every position in it is a decision.
//
// TIMEOUT IS FINALLY IN IT. This comment used to explain the SUBTRACTION —
// "Timeout takes a duration this build cannot read: there is no REQUEST_TIMEOUT
// in internal/config, and inventing one here would be a configuration value
// nobody chose" — and that was correct and stayed true for four steps, during
// which `httpx.Timeout` had ZERO production call sites. DEC-96 adds the
// variable, so the reason is discharged rather than restated: the duration
// comes from config and the middleware is wired.
//
// Recover, RequestID and AccessLog were already here because the auth routes
// are the first thing a client can reach with a body: without Recover a
// panicking handler closes the connection with no response at all, and without
// RequestID the detail httpx sends to the log has nothing to tie it to.
//
// RETRY-AFTER GOES ABOVE TIMEOUT, AND THAT IS THE ONLY PLACE IT WORKS.
// `http.TimeoutHandler` writes its own 503 from inside net/http, so a
// Retry-After set anywhere BELOW it never touches the 503 a client is most
// likely to meet. Above it, one wrapper covers both that 503 and every
// handler-written one.
//
// MuxErrors IS INNERMOST AND IS NOT PART OF Base. It has to sit directly
// around the mux, because the 404 and the 405 it exists to rewrite are written
// by the mux itself; and it belongs inside the other three because an unknown
// path is a request that happened and should be recovered, identified and
// logged like any other.
//
// COMPRESS SITS BELOW THE ACCESS LOG AND ABOVE MuxErrors, and both boundaries
// are decisions (DEC-94). Below the access log, so `bytes` counts what
// actually went on the wire — the number an operator wants, and the one the
// gzip measurement exists to support; outermost it would report the
// uncompressed size and quietly undo it. Above MuxErrors, so the envelope
// bodies MuxErrors writes go through the compressor's own small-body floor
// rather than around it.
func serverChain(mux *http.ServeMux, log *slog.Logger, requestTimeout time.Duration) http.Handler {
	return httpx.Chain(mux,
		httpx.Recover(log),
		httpx.RequestID(),
		// `/healthz` IS DEMOTED TO Debug WHILE IT IS HEALTHY (DEC-101). The
		// container probes it every five seconds for ever; the disk cost is
		// survivable anywhere and the SIGNAL cost is a 20:1 dilution of the
		// one file you read at 3am. A probe that FAILS is still an ERROR line,
		// because that is the most interesting line in the file — see
		// accessLevel, where the failure branch is checked first.
		httpx.AccessLog(log, healthzPath),
		httpx.RetryAfter(),
		httpx.Timeout(requestTimeout),
		httpx.Compress(),
		httpx.MuxErrors(),
	)
}

// healthzHandler answers ok, or unavailable on a ping that does not come back.
//
// THE PING IS BOUNDED (spec L22). An unbounded one against a wedged server
// holds the handler past the HEALTHCHECK's own timeout, which turns "unhealthy"
// into "no answer".
//
// THE DETAIL GOES TO THE LOG AND NEVER TO THE BODY. /healthz is the one route
// reachable unauthenticated, and a driver error names hosts, ports and database
// names to anyone who asks.
func healthzHandler(db pinger, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthzPingTimeout)
		defer cancel()

		status, code := "ok", http.StatusOK
		if err := db.PingContext(ctx); err != nil {
			status, code = "unavailable", http.StatusServiceUnavailable
			log.Error("healthz: the database did not answer",
				slog.String("err", err.Error()))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, "{%q:%q}\n", "status", status)
	}
}

// writeTimeout IS A PROMISE ABOUT THE CLIENT'S DOWNLINK, AND THAT IS WHY IT IS
// NAMED AND ARGUED RATHER THAN INLINE (DEC-94).
//
// It was 15s. At 15s the server was promising that no log will ever exceed
// fifteen seconds of the client's downlink, and NOTHING ENFORCED THAT. The
// performance lens measured what the broken promise looks like: against an
// 11,102,597-byte log, `GET /v1/logbook` answers HTTP 200 with a VALID ETag and
// a body cut mid-token — at 400 kB/s, 8,371,312 bytes received, curl exit 18,
// `json.load` -> "Unterminated string starting at char 8371258"; at 500 kB/s,
// 9,631,256 bytes, code 200; at 200 kB/s, 5,622,648 bytes, code 200.
//
// THREE THINGS ADDRESS THAT CLASS AND THIS IS ONE OF THEM. httpx.Compress
// moves the threshold 5-15x (that same log gzips to 2,084,727 bytes at level
// 1); DEC-93's 500-point cap on `Walk.points` removes the largest growth term;
// and this raises the ceiling. None of the three alone removes it.
//
// SIXTY SECONDS, AND HERE IS THE PROMISE IT MAKES. Through the compressor, the
// 11.1 MB worst case is 2.08 MB on the wire, so 60s covers a client sustaining
// 35 kB/s — below any usable mobile link, and roughly 17x more headroom than
// the 15s the same body had at 139 kB/s. It is deliberately NOT unbounded: a
// WriteTimeout of 0 means a slow-read client can hold a connection and a
// database-free goroutine indefinitely, which is the other failure this knob
// exists for.
//
// IT IS NOT THE PER-REQUEST BOUND. REQUEST_TIMEOUT bounds how long the HANDLER
// may take (httpx.Timeout, wired in run) and is 15s; this bounds how long the
// RESPONSE may take to reach a client that may be on a train. Making them one
// number would mean either a handler allowed a minute of database time or a
// log that cannot be delivered over a slow link.
const writeTimeout = 60 * time.Second

func serve(addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// probe is the container's health check. It talks to the loopback address on
// the server's own port, because inside the container there is nothing else.
func probe(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: bad addr %q: %v\n", addr, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	url := "http://127.0.0.1:" + port + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s\n", resp.Status)
		return 1
	}
	return 0
}
