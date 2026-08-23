// Command api is the travellog HTTP server.
//
// VS1 SCOPE. This file exists so the stack that VS1 builds can be shown to
// come up: the step's acceptance check is `docker compose up -d && curl -sf
// localhost:8080/healthz`, and there is no way to run that against an empty
// `cmd/api`. VS2 replaces the whole of it with the real one — config.Load,
// slog, the explicit pool, migrations, graceful shutdown, and a /healthz that
// pings the database rather than answering a constant. VS2's own mutation
// proof ("make healthz return a constant and the 503 leg reddens") is written
// against exactly the placeholder below.
//
// Two things here are NOT placeholder and must survive VS2:
//
//   - the blank import of time/tzdata. Stage 2 of the Dockerfile is `scratch`,
//     which carries no zoneinfo, so without this every time.LoadLocation fails
//     in the shipped image and nothing fails on a developer's machine.
//   - the -healthcheck flag. scratch has no shell and no curl, so a Docker
//     HEALTHCHECK has nothing to invoke except this binary. It is the reason
//     the flag exists; it is not a convenience.
//
// It deliberately does NOT read os.Getenv. VS2's acceptance check is that
// internal/config is the only file in the repository that does, and a
// placeholder that has to be un-done to satisfy a later check is worse than
// one that is simply narrow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embeds the IANA timezone database in the binary. Load-bearing: the
	// runtime image is `scratch` and has no /usr/share/zoneinfo.
	_ "time/tzdata"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	healthcheck := flag.Bool(
		"healthcheck",
		false,
		"probe /healthz on the local server and exit 0 or 1; "+
			"this is what the container HEALTHCHECK runs, because scratch has no shell",
	)
	flag.Parse()

	if *healthcheck {
		os.Exit(probe(*addr))
	}

	if err := serve(*addr); err != nil {
		log.Fatalf("api: %v", err)
	}
}

// newMux builds the server's routing table.
//
// EXTRACTED FROM serve AT VS1-BACKFILL, and the extraction is the whole of the
// change: the body is byte-for-byte what serve held, and serve now calls this.
// The reason is that serve blocks until a signal and hands its listener to
// ListenAndServe, so there is no way to reach the handler from a test without
// signalling the test process itself. newMux is what httptest.NewServer takes.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()

	// VS1 PLACEHOLDER. VS2 makes this ping the database so it can answer 503.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})

	return mux
}

func serve(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Printf("api: listening on %s", addr)
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
		log.Printf("api: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
