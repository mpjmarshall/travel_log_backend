package admin

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"travellog/internal/httpx"
)

// CookieName carries the session id and nothing else.
const CookieName = "admin_session"

// MaxFailures is how many refusals one client address may collect before the
// login stops answering it, and LockFor is how long that lasts.
const (
	MaxFailures = 10
	LockFor     = 15 * time.Minute
)

// Renderer is what draws a page. Task 5 supplies the real one.
type Renderer interface {
	Page(w http.ResponseWriter, status int, name string, data any)
}

// Deps is everything the panel is given. Password empty means no panel.
type Deps struct {
	Password string
	Sessions *Sessions
	Now      func() time.Time
	Log      *slog.Logger
	Dev      bool
	Render   Renderer
}

type attempts struct {
	mu    sync.Mutex
	n     map[string]int
	until map[string]time.Time
}

func newAttempts() *attempts {
	return &attempts{n: map[string]int{}, until: map[string]time.Time{}}
}

// locked reports whether this key is inside its cooling period.
func (a *attempts) locked(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	at, ok := a.until[key]
	if !ok {
		return false
	}
	if now.Before(at) {
		return true
	}
	delete(a.until, key)
	delete(a.n, key)
	return false
}

// fail records a refusal and starts the lock on reaching the ceiling.
func (a *attempts) fail(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.n[key]++
	if a.n[key] < MaxFailures {
		return false
	}
	a.until[key] = now.Add(LockFor)
	return true
}

func (a *attempts) clear(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.n, key)
	delete(a.until, key)
}

// Login answers the form on GET and judges it on POST. A locked key is refused
// whatever it sends, so guessing until the operator signs in cannot beat it.
func Login(deps Deps) http.HandlerFunc {
	tries := newAttempts()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			deps.Render.Page(w, http.StatusOK, "login", nil)
			return
		}

		key := httpx.ClientKey(r)
		now := deps.Now()
		if tries.locked(key, now) {
			deps.Log.Warn("admin: login refused, locked",
				slog.String("client", key))
			deps.Render.Page(w, http.StatusTooManyRequests, "login", pageData{Locked: true})
			return
		}

		if err := r.ParseForm(); err != nil {
			deps.Render.Page(w, http.StatusBadRequest, "login", pageData{Failed: true})
			return
		}

		given := r.PostFormValue("password")
		if subtle.ConstantTimeCompare([]byte(given), []byte(deps.Password)) != 1 {
			locked := tries.fail(key, now)
			deps.Log.Warn("admin: login refused",
				slog.String("client", key), slog.Bool("locked", locked))
			deps.Render.Page(w, http.StatusUnauthorized, "login", pageData{Failed: true})
			return
		}

		id, _, err := deps.Sessions.New(now)
		if err != nil {
			deps.Log.Error("admin: minting a session", slog.String("err", err.Error()))
			deps.Render.Page(w, http.StatusInternalServerError, "login", pageData{Failed: true})
			return
		}

		tries.clear(key)
		http.SetCookie(w, deps.cookie(id))
		deps.Log.Info("admin: signed in", slog.String("client", key))
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	}
}

// cookie is Secure everywhere but a development stack, which is the one place
// the panel is reached over plain http.
func (d Deps) cookie(id string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   !d.Dev,
		SameSite: http.SameSiteStrictMode,
	}
}

// pageData is what a template is given. It grows as the pages do.
type pageData struct {
	Failed bool
	Locked bool
	CSRF   string
}
