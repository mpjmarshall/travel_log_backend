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
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer func() { _ = db.Close() }()

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)

	pingCtx, cancel := context.WithTimeout(context.Background(), startupPingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("the database did not answer within %s: %w", startupPingTimeout, err)
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

	mount, err := apiRoutes(cfg, db, log)
	if err != nil {
		return err
	}
	return serve(addr, serverChain(newMux(db, log, mount), log), log)
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
	db, err := sql.Open("pgx", cfg.DatabaseURL)
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
func newMux(db pinger, log *slog.Logger, mounts ...func(*http.ServeMux)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(db, log))
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
func apiRoutes(cfg config.Config, db *sql.DB, log *slog.Logger) (func(*http.ServeMux), error) {
	gate, err := auth.NewGate(cfg.Argon2MaxConcurrent)
	if err != nil {
		return nil, fmt.Errorf("ARGON2_MAX_CONCURRENT: %w", err)
	}
	service := &auth.Service{
		Store:  postgres.AuthStore{DB: db},
		Hasher: auth.Capped{Hasher: auth.Argon2id{Params: auth.DefaultParams}, Gate: gate},
	}
	limiter := httpx.NewLimiter(cfg.AuthRateLimitPerMin, nil)

	return func(mux *http.ServeMux) {
		httpapi.Mount(mux, httpapi.Deps{Auth: service, Log: log, AuthLimit: limiter})
	}, nil
}

// serverChain is httpx.Base MINUS Timeout, and the subtraction is stated
// rather than silent.
//
// httpx.Base is Recover, RequestID, AccessLog, Timeout — and Timeout takes a
// duration this build cannot read: there is no REQUEST_TIMEOUT in
// internal/config, and inventing one here would be a configuration value
// nobody chose, silently in force, which is the thing config.Load exists to
// refuse. It belongs to the step that adds the variable.
//
// The other three are wired now because the auth routes are the first thing
// here a client can reach with a body: without Recover a panicking handler
// closes the connection with no response at all, and without RequestID the
// detail httpx sends to the log has nothing to tie it to.
//
// MuxErrors IS INNERMOST AND IS NOT PART OF Base. It has to sit directly
// around the mux, because the 404 and the 405 it exists to rewrite are written
// by the mux itself; and it belongs inside the other three because an unknown
// path is a request that happened and should be recovered, identified and
// logged like any other.
func serverChain(mux *http.ServeMux, log *slog.Logger) http.Handler {
	return httpx.Chain(mux,
		httpx.Recover(log),
		httpx.RequestID(),
		httpx.AccessLog(log),
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

func serve(addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
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
