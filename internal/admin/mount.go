package admin

import (
	"crypto/subtle"
	"net/http"
)

// CSRFHeader is what htmx attaches to every request from the panel.
const CSRFHeader = "X-CSRF-Token"

// loginPath and rootPath are the only two literals the middlewares need.
const (
	loginPath = "/admin/login"
	rootPath  = "/admin"
)

// Mount registers the panel, or registers nothing at all when no password is
// configured. An unconfigured panel does not exist rather than standing open.
func Mount(mux *http.ServeMux, deps Deps) {
	if deps.Password == "" {
		return
	}

	login := Login(deps)
	mux.Handle("GET "+loginPath, deps.open(login))
	mux.Handle("POST "+loginPath, deps.open(login))
	mux.Handle("POST /admin/logout", deps.guarded(logout(deps)))
	home := deps.guarded(dashboard(deps))
	mux.Handle("GET "+rootPath, home)
	mux.Handle("GET "+rootPath+"/{$}", home)
}

// open is a page nobody has to be signed in for, which is the login alone.
func (d Deps) open(h http.HandlerFunc) http.Handler {
	return securityHeaders(h)
}

// guarded is every other page: headers, then a session, then the CSRF token
// on anything that writes.
func (d Deps) guarded(h http.HandlerFunc) http.Handler {
	return securityHeaders(d.requireSession(d.requireCSRF(h)))
}

func securityHeaders(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next(w, r)
	})
}

// requireSession sends anyone without a live one to the login page.
func (d Deps) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
			return
		}
		csrf, ok := d.Sessions.Get(c.Value, d.Now())
		if !ok {
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(withCSRF(r.Context(), csrf)))
	}
}

// requireCSRF checks the token on anything that is not a read.
func (d Deps) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		want := csrfFrom(r.Context())
		got := r.Header.Get(CSRFHeader)
		if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "the form was not sent from the panel", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func logout(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil {
			d.Sessions.Drop(c.Value)
		}
		expired := d.cookie("")
		expired.MaxAge = -1
		http.SetCookie(w, expired)
		http.Redirect(w, r, loginPath, http.StatusSeeOther)
	}
}

// dashboard is a placeholder until task 6 gives it its counts.
func dashboard(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.Render.Page(w, http.StatusOK, "dashboard", pageData{CSRF: csrfFrom(r.Context())})
	}
}
