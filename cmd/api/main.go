// Command api is the travellog HTTP server.
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

	"travellog/internal/admin"
	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/geocode"
	"travellog/internal/httpapi"
	"travellog/internal/httpx"
	"travellog/internal/logging"
	"travellog/internal/mail"
	"travellog/internal/media"
	"travellog/internal/postgres"
	"travellog/migrations"
)

// startupPingTimeout is allowed longer than the per-request one.
const startupPingTimeout = 10 * time.Second

// healthzPingTimeout is /healthz's own ping.
const healthzPingTimeout = 2 * time.Second

const shutdownTimeout = 10 * time.Second

// migrateTimeout bounds the whole migration run, the wait for the advisory
// lock included.
const migrateTimeout = 2 * time.Minute

// Komoot's public Photon needs no key. The User-Agent is what OSM's policy
// asks for, and the timeout stops a slow third party holding a request open.
const (
	geocodeBase    = "https://photon.komoot.io"
	geocodeAgent   = "travel-log/1.0 (+https://github.com/mpjmarshall/travellog)"
	geocodeTimeout = 8 * time.Second
)

// bucketTimeout bounds the boot-time bucket check.
const bucketTimeout = 10 * time.Second

// main parses's two flags, loads the config and dispatches.
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

// run owns the database handle for the lifetime of the process.
func run(cfg config.Config, addr string, log *slog.Logger) error {
	sender, err := mailer(cfg, log)
	if err != nil {
		return err
	}

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

	bucketCtx, bucketCancel := context.WithTimeout(context.Background(), bucketTimeout)
	defer bucketCancel()
	objects, err := mediaStore(bucketCtx, cfg, log)
	if err != nil {
		return err
	}

	mount, err := apiRoutes(cfg, db, log, objects, sender)
	if err != nil {
		return err
	}
	panel, err := adminPanel(cfg, db, objects, log)
	if err != nil {
		return err
	}

	return serve(addr, serverChain(newMux(db, log, mount, panel), log, cfg.RequestTimeout), log)
}

// pinger is the half of *sql.DB that /healthz needs.
type pinger interface {
	PingContext(ctx context.Context) error
}

// migrateUp applies migrations/*.up.sql, at boot and behind the advisory
// lock.
func migrateUp(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	applied, err := postgres.Migrator{Logger: log}.Migrate(ctx, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info("migrations up to date", slog.Int("applied", len(applied)))
	return nil
}

// migrateOnlyRun is `-migrate-only`: open, pool, ping, migrate, stop.
func migrateOnlyRun(cfg config.Config, log *slog.Logger) error {
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

// adminPanel is the /admin mount, or a mount that registers nothing when no
// password is configured.
func adminPanel(cfg config.Config, db *sql.DB, objects media.Store, log *slog.Logger) (func(*http.ServeMux), error) {
	if cfg.AdminPassword == "" {
		return func(*http.ServeMux) {}, nil
	}
	pages, err := admin.NewTemplates(log)
	if err != nil {
		return nil, fmt.Errorf("the admin panel's templates: %w", err)
	}
	return func(mux *http.ServeMux) {
		admin.Mount(mux, admin.Deps{
			Password: cfg.AdminPassword,
			Sessions: admin.NewSessions(),
			Now:      time.Now,
			Log:      log,
			Dev:      cfg.Development,
			Render:   pages,
			Store:    postgres.AdminStore{DB: db},
			Writer:   postgres.AdminStore{DB: db},
			Objects:  objects,
		})
	}, nil
}

// newMux builds the server's routing table.
const healthzPath = "/healthz"

func newMux(db pinger, log *slog.Logger, mounts ...func(*http.ServeMux)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, healthzHandler(db, log))
	for _, mount := range mounts {
		mount(mux)
	}
	return mux
}

// apiRoutes builds the auth service from the config and answers something
// that mounts its routes.
func apiRoutes(cfg config.Config, db *sql.DB, log *slog.Logger, objects media.Store, sender mail.Sender) (func(*http.ServeMux), error) {
	if db == nil {
		return nil, errors.New("apiRoutes: the database handle is nil, and every store built " +
			"on it is a non-nil interface value — so the routes would mount and answer a " +
			"panic on their first query")
	}
	service := &auth.Service{Store: postgres.AuthStore{DB: db}}
	places := geocode.NewPhoton(geocodeBase, geocodeAgent, &http.Client{
		Timeout: geocodeTimeout,
	})
	credential, traveller, public := limiters(cfg)

	return func(mux *http.ServeMux) {
		httpapi.Mount(mux, httpapi.Deps{
			Mail:           sender,
			Auth:           service,
			Logbook:        postgres.LogbookStore{DB: db},
			Share:          postgres.ShareStore{DB: db},
			Cities:         postgres.CityStore{DB: db},
			Places:         postgres.PlaceStore{DB: db},
			Photos:         postgres.PhotoStore{DB: db},
			Walks:          postgres.WalkStore{DB: db},
			Public:         postgres.ShareReadStore{DB: db},
			Geocode:        places,
			Log:            log,
			AuthLimit:      credential,
			TravellerLimit: traveller,
			PublicLimit:    public,
			Media:          postgres.MediaStore{DB: db},
			Objects:        objects,
			MediaMaxBytes:  cfg.MediaMaxBytes,
		})
	}, nil
}

// limiters builds the API's three ceilings from their three variables, which
// carry them.
func limiters(cfg config.Config) (credential, traveller, public *httpx.Limiter) {
	return httpx.NewLimiter(cfg.AuthRateLimitPerMin, nil),
		httpx.NewLimiter(cfg.TravellerRateLimitPerMin, nil),
		httpx.NewLimiter(cfg.PublicRateLimitPerMin, nil)
}

// mediaConfig turns the nine S3_* variables into the store's own Config.
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

// mediaStore builds the store and creates its bucket, and's second half is
// the point.
func mediaStore(ctx context.Context, cfg config.Config, log *slog.Logger) (media.Store, error) {
	store, err := media.New(mediaConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("the media store: %w", err)
	}
	started := time.Now()
	if err := store.EnsureBucket(ctx); err != nil {
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
func serverChain(mux *http.ServeMux, log *slog.Logger, requestTimeout time.Duration) http.Handler {
	return httpx.Chain(mux,
		httpx.Recover(log),
		httpx.RequestID(),
		httpx.AccessLog(log, healthzPath),
		httpx.RetryAfter(),
		httpx.Timeout(requestTimeout),
		httpx.Compress(),
		httpx.MuxErrors(),
	)
}

// healthzHandler answers ok, or unavailable on a ping that does not come
// back.
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

// writeTimeout is a promise about the client's downlink, and that is why it
// is named and argued rather than inline.
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

// probe is the container's health check.
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

// mailSendTimeout bounds one delivery attempt.
const mailSendTimeout = 15 * time.Second

// mailer builds the sign-in code sender, detached so delivery time cannot say
// whether an address has a log here.
func mailer(cfg config.Config, log *slog.Logger) (mail.Sender, error) {
	sender, err := mail.NewLogSender(log, cfg.Development)
	if err != nil {
		return nil, fmt.Errorf("no mail provider is configured and DEVELOPMENT is not set: %w", err)
	}
	return mail.Detached(mail.WithTimeout(sender, mailSendTimeout), log), nil
}
