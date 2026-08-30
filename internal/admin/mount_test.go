// Mounting the panel, and the three middlewares every page wears.
package admin_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"travellog/internal/admin"
)

// mountedPaths are registered whenever the panel mounts at all, so the same
// list proves both directions: 404 without a password, not 404 with one.
var mountedPaths = []string{"/admin", "/admin/login"}

func mounted(t *testing.T, password string) (*http.ServeMux, admin.Deps) {
	t.Helper()
	deps := admin.Deps{
		Password: password,
		Sessions: admin.NewSessions(),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Render:   &stubRenderer{},
	}
	mux := http.NewServeMux()
	admin.Mount(mux, deps)
	return mux, deps
}

func get(mux *http.ServeMux, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestNoPasswordMountsNothing(t *testing.T) {
	mux, _ := mounted(t, "")

	for _, path := range mountedPaths {
		if code := get(mux, path).Code; code != http.StatusNotFound {
			t.Errorf("%s answered %d with no password set, want 404: a panel nobody\n"+
				"    configured must not exist, so every stack that has not been told\n"+
				"    a password boots exactly as it does today", path, code)
		}
	}
}

func TestAPasswordMountsThoseSamePaths(t *testing.T) {
	mux, _ := mounted(t, goodPassword)

	for _, path := range mountedPaths {
		if code := get(mux, path).Code; code == http.StatusNotFound {
			t.Errorf("%s answered 404 with a password set, so the previous test\n"+
				"    proves nothing about mounting", path)
		}
	}
}

func TestEveryPageRefusesWithoutASession(t *testing.T) {
	mux, _ := mounted(t, goodPassword)

	rec := get(mux, "/admin")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("/admin without a session = %d, want 303 to the login", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", got)
	}
}

func TestAnUnknownCookieIsNotASession(t *testing.T) {
	mux, _ := mounted(t, goodPassword)

	rec := get(mux, "/admin", &http.Cookie{Name: admin.CookieName, Value: "invented"})
	if rec.Code != http.StatusSeeOther {
		t.Errorf("an invented cookie answered %d, want a redirect to the login", rec.Code)
	}
}

func TestASessionReachesThePage(t *testing.T) {
	mux, deps := mounted(t, goodPassword)
	id, _, err := deps.Sessions.New(deps.Now())
	if err != nil {
		t.Fatal(err)
	}

	rec := get(mux, "/admin", &http.Cookie{Name: admin.CookieName, Value: id})
	if rec.Code != http.StatusOK {
		t.Errorf("/admin with a live session = %d, want 200", rec.Code)
	}
}

func TestAMutatingRouteRefusesWithoutTheCSRFToken(t *testing.T) {
	mux, deps := mounted(t, goodPassword)
	id, csrf, err := deps.Sessions.New(deps.Now())
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: admin.CookieName, Value: id}

	post := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
		req.AddCookie(cookie)
		if token != "" {
			req.Header.Set(admin.CSRFHeader, token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(""); code != http.StatusForbidden {
		t.Errorf("a POST with no CSRF token = %d, want 403", code)
	}
	if code := post("not-the-token"); code != http.StatusForbidden {
		t.Errorf("a POST with the wrong CSRF token = %d, want 403", code)
	}
	if code := post(csrf); code == http.StatusForbidden {
		t.Error("a POST carrying the session's own CSRF token was refused")
	}
}

func TestEveryAdminResponseCarriesItsSecurityHeaders(t *testing.T) {
	mux, _ := mounted(t, goodPassword)

	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	}
	h := get(mux, "/admin/login").Header()
	for name, value := range want {
		if got := h.Get(name); !strings.Contains(got, value) {
			t.Errorf("%s = %q, want it to contain %q", name, got, value)
		}
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	mux, deps := mounted(t, goodPassword)
	id, csrf, err := deps.Sessions.New(deps.Now())
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: admin.CookieName, Value: id})
	req.Header.Set(admin.CSRFHeader, csrf)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if _, ok := deps.Sessions.Get(id, deps.Now()); ok {
		t.Error("the session survived a logout")
	}
}
