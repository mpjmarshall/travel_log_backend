// Signing in to the panel: the compare, the lockout, and the cookie.
package admin_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"travellog/internal/admin"
)

const goodPassword = "a-long-enough-password"

type stubRenderer struct{ last string }

func (s *stubRenderer) Page(w http.ResponseWriter, status int, name string, _ any) {
	s.last = name
	w.WriteHeader(status)
}

func (s *stubRenderer) Fragment(w http.ResponseWriter, status int, name string, _ any) {
	s.last = name
	w.WriteHeader(status)
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func loginDeps(dev bool) (admin.Deps, *clock, *stubRenderer) {
	c := &clock{at: time.Unix(1_700_000_000, 0).UTC()}
	r := &stubRenderer{}
	return admin.Deps{
		Password: goodPassword,
		Sessions: admin.NewSessions(),
		Now:      c.now,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dev:      dev,
		Render:   r,
	}, c, r
}

func post(h http.HandlerFunc, password string) *httptest.ResponseRecorder {
	body := strings.NewReader(url.Values{"password": {password}}.Encode())
	req := httptest.NewRequest(http.MethodPost, "/admin/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == admin.CookieName {
			return c
		}
	}
	return nil
}

func TestTheWrongPasswordSetsNoCookie(t *testing.T) {
	deps, _, _ := loginDeps(false)
	h := admin.Login(deps)

	rec := post(h, "not-the-password")
	if c := cookieOf(t, rec); c != nil {
		t.Fatalf("a refused login set %s=%q", admin.CookieName, c.Value)
	}
	if deps.Sessions.Len() != 0 {
		t.Errorf("Sessions.Len() = %d after a refusal, want 0", deps.Sessions.Len())
	}
}

func TestTheRightPasswordSetsAStrictSecureHttpOnlyCookie(t *testing.T) {
	deps, _, _ := loginDeps(false)

	rec := post(admin.Login(deps), goodPassword)
	c := cookieOf(t, rec)
	if c == nil {
		t.Fatal("a correct password set no session cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie is HttpOnly=%v Secure=%v SameSite=%v, want true true Strict:\n"+
			"    a panel that edits the database must not hand its session to script,\n"+
			"    to a cross-site form, or to anyone reading the wire",
			c.HttpOnly, c.Secure, c.SameSite)
	}
	if _, ok := deps.Sessions.Get(c.Value, deps.Now()); !ok {
		t.Error("the cookie names no live session")
	}
}

func TestSecureIsDroppedOnlyUnderDevelopment(t *testing.T) {
	deps, _, _ := loginDeps(true)

	c := cookieOf(t, post(admin.Login(deps), goodPassword))
	if c == nil {
		t.Fatal("no cookie under DEVELOPMENT")
	}
	if c.Secure {
		t.Error("Secure stayed on under DEVELOPMENT, so the panel cannot be used over plain http")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Error("DEVELOPMENT relaxes Secure alone, never HttpOnly or SameSite")
	}
}

func TestTenFailuresLockTheLoginAndTheRightPasswordIsRefusedToo(t *testing.T) {
	deps, _, _ := loginDeps(false)
	h := admin.Login(deps)

	for i := 0; i < admin.MaxFailures; i++ {
		post(h, "wrong")
	}

	rec := post(h, goodPassword)
	if c := cookieOf(t, rec); c != nil {
		t.Error("the correct password was accepted during a lockout, so guessing " +
			"until the operator signs in defeats the lock")
	}
}

func TestTheLockoutLiftsOnceTheWindowPasses(t *testing.T) {
	deps, c, _ := loginDeps(false)
	h := admin.Login(deps)

	for i := 0; i < admin.MaxFailures; i++ {
		post(h, "wrong")
	}
	c.at = c.at.Add(admin.LockFor + time.Second)

	if cookie := cookieOf(t, post(h, goodPassword)); cookie == nil {
		t.Error("the lockout never lifted, so one attacker locks the operator out for good")
	}
}

func TestNineFailuresDoNotLock(t *testing.T) {
	deps, _, _ := loginDeps(false)
	h := admin.Login(deps)

	for i := 0; i < admin.MaxFailures-1; i++ {
		post(h, "wrong")
	}
	if cookie := cookieOf(t, post(h, goodPassword)); cookie == nil {
		t.Errorf("locked after %d failures, and the floor is %d",
			admin.MaxFailures-1, admin.MaxFailures)
	}
}

func TestASuccessClearsTheCount(t *testing.T) {
	deps, _, _ := loginDeps(false)
	h := admin.Login(deps)

	for i := 0; i < admin.MaxFailures-1; i++ {
		post(h, "wrong")
	}
	post(h, goodPassword)
	for i := 0; i < admin.MaxFailures-1; i++ {
		post(h, "wrong")
	}

	if cookie := cookieOf(t, post(h, goodPassword)); cookie == nil {
		t.Error("failures survived a success, so two separate typos a day apart lock the panel")
	}
}

func TestGetRendersTheLoginPage(t *testing.T) {
	deps, _, r := loginDeps(false)

	rec := httptest.NewRecorder()
	admin.Login(deps)(rec, httptest.NewRequest(http.MethodGet, "/admin/login", nil))

	if r.last != "login" {
		t.Errorf("rendered %q, want %q", r.last, "login")
	}
}
