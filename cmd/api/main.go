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

	// Embeds the IANA timezone database in the binary. Load-bearing: the
	// runtime image is `scratch` and has no /usr/share/zoneinfo.
	_ "time/tzdata"

	// go_backend.md L20: use this "solely as a blank import driver". This is
	// the ONLY import of it in the repository and it is blank; imports_test.go
	// asserts all three of those claims by walking the AST, because a grep can
	// see the line but not the underscore.
	_ "github.com/jackc/pgx/v5/stdlib"

	"travellog/internal/config"
	"travellog/internal/logging"
)

const (
	// The startup ping is allowed longer than the per-request one: compose
	// gates the api on `postgres: service_healthy`, so a slow answer here is a
	// cold database rather than a broken one.
	startupPingTimeout = 10 * time.Second

	// /healthz's own ping. It must come in under the container HEALTHCHECK's
	// 3s timeout, or a wedged database turns a probe that should report
	// unhealthy into one that never answers at all.
	healthzPingTimeout = 2 * time.Second

	shutdownTimeout = 10 * time.Second
)

func main() {
	healthcheck := flag.Bool(
		"healthcheck",
		false,
		"probe /healthz on the local server and exit 0 or 1; "+
			"this is what the container HEALTHCHECK runs, because scratch has no shell",
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		// Written with fmt rather than through the logger: the logger's level
		// comes from the config that has just failed to load, so there is
		// nothing to build one from.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	addr := ":" + cfg.Port

	if *healthcheck {
		os.Exit(probe(addr))
	}

	log := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(log)

	if err := run(cfg, addr, log); err != nil {
		log.Error("api: stopping", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

// run owns the database handle for the process's lifetime.
func run(cfg config.Config, addr string, log *slog.Logger) error {
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// go_backend.md L21: "Explicitly configure connection pooling in main.go
	// using SetMaxOpenConns() and SetMaxIdleConns()." Both values come from
	// config, which has already refused an idle count above the open one —
	// database/sql clamps that silently and sql.DBStats has no field that
	// would ever report it.
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)

	// sql.Open does not connect. Without this the first request is where a
	// wrong DSN is discovered, and it is discovered as a 503 rather than as a
	// startup failure. Compose already gates this container on postgres being
	// healthy, so a failure here is a real misconfiguration and the process
	// should not come up pretending otherwise.
	pingCtx, cancel := context.WithTimeout(context.Background(), startupPingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("the database did not answer within %s: %w", startupPingTimeout, err)
	}
	log.Info("database ready",
		slog.Int("maxOpenConns", cfg.DBMaxOpenConns),
		slog.Int("maxIdleConns", cfg.DBMaxIdleConns),
	)

	// Migrations run here — VS4, internal/store/migrate.go. `make migrate`
	// exits 1 today rather than exiting 0 on nothing.

	return serve(addr, newMux(db, log), log)
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

// newMux builds the server's routing table.
//
// EXTRACTED FROM serve AT VS1-BACKFILL because serve blocks until a signal and
// hands its listener to ListenAndServe, so there is no way to reach the handler
// from a test without signalling the test process itself. newMux is what
// httptest.NewServer takes.
func newMux(db pinger, log *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// spec L22 — the ping is bounded. An unbounded one against a wedged
		// server holds the handler past the HEALTHCHECK's own timeout, which
		// turns "unhealthy" into "no answer".
		ctx, cancel := context.WithTimeout(r.Context(), healthzPingTimeout)
		defer cancel()

		status, code := "ok", http.StatusOK
		if err := db.PingContext(ctx); err != nil {
			status, code = "unavailable", http.StatusServiceUnavailable
			// The detail goes to the log and never to the body. /healthz is
			// the one route reachable unauthenticated, and a driver error
			// names hosts, ports and database names to anyone who asks.
			log.Error("healthz: the database did not answer",
				slog.String("err", err.Error()))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, "{%q:%q}\n", "status", status)
	})

	return mux
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
