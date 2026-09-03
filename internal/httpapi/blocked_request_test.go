// The one leg in this package that runs against a real PostgreSQL, and the
// only one that can (leg five).
package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"travellog/internal/geocode"

	"travellog/internal/auth"
	"travellog/internal/config"
	"travellog/internal/httpapi"
	"travellog/internal/httpx"
	"travellog/internal/mail"
	"travellog/internal/media"
	"travellog/internal/postgres"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The measured shape this leg refuses to accept.
func TestARequestBlockedOnALockGetsABoundedAnswerAndNotSilence(t *testing.T) {
	db, schema := testdb.Open(t)
	ctx := context.Background()

	if _, err := (postgres.Migrator{Schema: schema, Logger: quietLog()}).
		Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	appDSN, err := postgres.WithSessionOptions(scopedDSN(t, schema), 700*time.Millisecond)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	app, err := sql.Open("pgx", appDSN)
	if err != nil {
		t.Fatalf("opening the app pool: %v", err)
	}
	t.Cleanup(func() { app.Close() })

	server, token := realServer(t, app, 3*time.Second)

	holder := testdb.Second(t, schema)
	held, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("taking a second connection: %v", err)
	}
	t.Cleanup(func() { held.Close() })
	tx, err := held.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("beginning the holding transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(ctx, `LOCK TABLE trips IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("taking the lock: %v", err)
	}

	type answered struct {
		status int
		retry  string
		body   string
	}
	ch := make(chan answered, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/logbook", nil)
		if err != nil {
			ch <- answered{status: -1, body: err.Error()}
			return
		}
		req.Header.Set("Authorization", token)
		resp, err := server.Client().Do(req)
		if err != nil {
			ch <- answered{status: -1, body: err.Error()}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		ch <- answered{resp.StatusCode, resp.Header.Get("Retry-After"), string(body)}
	}()

	select {
	case got := <-ch:
		if got.status != http.StatusServiceUnavailable {
			t.Errorf("status = %d (%s), want 503 — a request that cannot reach the "+
				"database has not encountered a handler bug, and 500 tells a "+
				"client not to retry", got.status, got.body)
		}
		if got.retry == "" {
			t.Errorf("no Retry-After — 'the dependency is down, try again shortly' " +
				"is the whole difference between this and a poison request")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no answer at all within 10s — this is the measured shape: " +
			"curl http=000, nothing logged, /healthz green")
	}
}

// The control.
func TestTheSameReadAnswers200WhenNothingHoldsTheLock(t *testing.T) {
	db, schema := testdb.Open(t)
	if _, err := (postgres.Migrator{Schema: schema, Logger: quietLog()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	appDSN, err := postgres.WithSessionOptions(scopedDSN(t, schema), 700*time.Millisecond)
	if err != nil {
		t.Fatalf("WithSessionOptions: %v", err)
	}
	app, err := sql.Open("pgx", appDSN)
	if err != nil {
		t.Fatalf("opening the app pool: %v", err)
	}
	t.Cleanup(func() { app.Close() })

	server, token := realServer(t, app, 3*time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/logbook", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", token)
	got, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/logbook: %v", err)
	}
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", got.StatusCode)
	}
}

// realServer is the whole shipped stack over a real pool: the real auth
// store, the real logbook store, Mount, and httpx.Base.
func realServer(t *testing.T, db *sql.DB, requestTimeout time.Duration) (*httptest.Server, string) {
	t.Helper()
	log := quietLog()

	service := &auth.Service{Store: postgres.AuthStore{DB: db}}

	posted := &recordingMail{}

	mux := http.NewServeMux()
	httpapi.Mount(mux, httpapi.Deps{
		Geocode:        noGeocoder{},
		Auth:           service,
		Logbook:        postgres.LogbookStore{DB: db},
		Share:          postgres.ShareStore{DB: db},
		Cities:         postgres.CityStore{DB: db},
		Places:         postgres.PlaceStore{DB: db},
		Photos:         postgres.PhotoStore{DB: db},
		Walks:          postgres.WalkStore{DB: db},
		Public:         postgres.ShareReadStore{DB: db},
		Log:            log,
		Mail:           posted,
		AuthLimit:      httpx.NewLimiter(1000, nil),
		TravellerLimit: httpx.NewLimiter(1000, nil),
		PublicLimit:    httpx.NewLimiter(1000, nil),
		Media:          postgres.MediaStore{DB: db},
		Objects:        media.NewMemory(),
		MediaMaxBytes:  config.MinMediaMaxBytes,
	})
	server := httptest.NewServer(httpx.Chain(mux, httpx.Base(log, requestTimeout)...))
	t.Cleanup(server.Close)

	const invite = "LOCKTESTINVITE"
	if err := (postgres.AuthStore{DB: db}).MintInvite(context.Background(), auth.HashInvite(invite), "the lock leg"); err != nil {
		t.Fatalf("minting an invite: %v", err)
	}

	const credentials = `{"email":"lock@travellog.test","passphrase":"correct-horse-battery-staple","invite":"` + invite + `"}`
	postBody := func(path, body string) map[string]any {
		t.Helper()
		resp, err := server.Client().Post(server.URL+path, "application/json",
			strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s = %d", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		return out
	}
	post := func(path string) map[string]any { return postBody(path, credentials) }
	post("/v1/auth/register")

	if resp, err := server.Client().Post(server.URL+"/v1/auth/code", "application/json",
		strings.NewReader(`{"email":"lock@travellog.test"}`)); err != nil {
		t.Fatalf("asking for a code: %v", err)
	} else {
		resp.Body.Close()
	}
	waitFor(t, func() bool { return posted.count() > 0 })
	code := posted.codeFor(t, "lock@travellog.test")

	issued := postBody("/v1/auth/session",
		`{"email":"lock@travellog.test","code":"`+code+`"}`)
	token, held := issued["token"].(string)
	if !held {
		t.Fatalf("the sign-in answered no token: %v", issued)
	}
	return server, "Bearer " + token
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, nil))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// scopedDSN rebuilds the DSN testdb handed out for a schema, so the
// application's pool lands in the same place the migration did.
func scopedDSN(t *testing.T, schema string) string {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parsing TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// recordingMail is this package's own, because the external test package
// cannot see the one beside the handlers.
type recordingMail struct {
	mu   sync.Mutex
	sent []struct{ To, Text string }
}

func (r *recordingMail) Send(_ context.Context, to string, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, struct{ To, Text string }{to, m.Text})
	return nil
}

func (r *recordingMail) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func (r *recordingMail) codeFor(t *testing.T, to string) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.sent) - 1; i >= 0; i-- {
		if r.sent[i].To != to {
			continue
		}
		for _, f := range strings.Fields(r.sent[i].Text) {
			if len(f) == auth.CodeDigits && strings.Trim(f, "0123456789") == "" {
				return f
			}
		}
	}
	t.Fatalf("no code was mailed to %s", to)
	return ""
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition never became true")
}

// noGeocoder satisfies the port for the legs that never search a city.
type noGeocoder struct{}

func (noGeocoder) Search(context.Context, string, int) ([]geocode.City, error) {
	return nil, nil
}
