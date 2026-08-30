// The two auth routes, and the middleware the rest of the API will wear.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
	"travellog/internal/mail"
	"travellog/internal/media"
)

// Deps is what the routes need.
type Deps struct {
	Auth    *auth.Service
	Logbook logbook.Store

	Mail mail.Sender
	Log  *slog.Logger

	Share logbook.ShareStore

	Cities logbook.CityStore
	Places logbook.PlaceStore

	Photos logbook.PhotoStore
	Walks  logbook.WalkStore

	Public logbook.PublicStore

	AuthLimit      *httpx.Limiter
	TravellerLimit *httpx.Limiter

	PublicLimit *httpx.Limiter

	Media   logbook.MediaStore
	Objects media.Store

	MediaMaxBytes int64

	Now func() time.Time
}

// Clock is Now, or time.Now.
func (d Deps) Clock() func() time.Time {
	if d.Now == nil {
		return time.Now
	}
	return d.Now
}

// mediaService is the Service, built from its two ports rather than stored as
// a third field.
func (d Deps) mediaService() logbook.Service {
	return logbook.Service{Media: d.Media, Objects: d.Objects}
}

// places is the same Service built from the port D2's removal needs, and it
// is a second accessor rather than one that fills every field.
func (d Deps) places() logbook.Service {
	return logbook.Service{Places: d.Places}
}

// photos builds the Service from the port M2.2's re-file needs.
func (d Deps) photos() logbook.Service {
	return logbook.Service{Photos: d.Photos}
}

// credentials is both request bodies.
type credentials struct {
	Email      string `json:"email"`
	Passphrase string `json:"passphrase"`
	Code       string `json:"code"`
}

// travellerBody is register's 201.
type travellerBody struct {
	ID    string  `json:"id"`
	Email string  `json:"email"`
	Name  *string `json:"name"`
}

// sessionBody is the only place the plaintext token is ever written.
type sessionBody struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Mount adds every route in the table, each behind a ceiling and the
// authenticated ones behind the credential check as well.
func Mount(mux *http.ServeMux, deps Deps) {
	if deps.AuthLimit == nil {
		panic("httpapi: the auth routes need a rate limiter (DEC-48); a nil one is not 'no limit'")
	}
	if deps.TravellerLimit == nil {
		panic("httpapi: the authenticated routes need a rate limiter of their own; " +
			"a nil one is not 'no limit', it is an unlimited log read for a stolen token")
	}
	if deps.PublicLimit == nil {
		panic("httpapi: the public read needs a rate limiter of its own; a nil one " +
			"is not 'no limit', it is unmetered token enumeration on the only " +
			"route with no credential in front of it")
	}
	if deps.Logbook == nil {
		panic("httpapi: the logbook routes need a store; a nil one is not 'no logbook', " +
			"it is a 500 the first time somebody asks for their log")
	}
	if deps.Mail == nil {
		panic("httpapi: the code route needs a mailer; a nil one is not 'no mail', " +
			"it is a sign-in nobody can complete and a 202 that says it worked")
	}
	if deps.Log == nil {
		panic("httpapi: the routes need a logger; a nil one is not 'no logging', " +
			"it is a 500 in place of every 429 the rate limiter means to send")
	}
	if deps.Share == nil {
		panic("httpapi: the share routes need a store; a nil one is not 'no sharing', " +
			"it is a panic the first time somebody presses Stop sharing")
	}
	if deps.Public == nil {
		panic("httpapi: the public read needs a store; a nil one is not 'no public " +
			"sharing', it is a panic on every link anybody has ever handed out")
	}
	if deps.Cities == nil {
		panic("httpapi: the city route needs a store; a nil one is not 'no cities', " +
			"it is a panic the first time T5 adds one")
	}
	if deps.Places == nil {
		panic("httpapi: the place routes need a store; a nil one is not 'no places', " +
			"it is a panic inside D2's removal")
	}
	if deps.Photos == nil {
		panic("httpapi: the photo routes need a store; a nil one is not 'no " +
			"photographs', it is a panic the first time M2 writes a note")
	}
	if deps.Walks == nil {
		panic("httpapi: the walk route needs a store; a nil one is not 'no walks', " +
			"it is a panic on the route that keeps a day's recording alive")
	}
	if deps.Media == nil {
		panic("httpapi: the media routes need a store; a nil one is not 'no media'")
	}
	if deps.Objects == nil {
		panic("httpapi: the media routes need an object store — nothing else can mint " +
			"an upload capability, and a nil one is a panic on the first photograph")
	}
	if deps.MediaMaxBytes <= 0 {
		panic("httpapi: MEDIA_MAX_BYTES is not set on Deps, and a bound of zero " +
			"refuses every upload — which is a feature switched off by a setting " +
			"that reads like a safety measure")
	}
	perAddress := httpx.RateLimit(deps.AuthLimit, deps.Log)
	perTraveller := limitByTraveller(deps.TravellerLimit, deps.Log)
	perPublicAddress := httpx.RateLimit(deps.PublicLimit, deps.Log)
	authed := RequireTraveller(deps.Auth, deps.Log)
	capability := httpx.CapabilityHeaders()
	for _, route := range Routes(deps) {
		handler := http.Handler(route.Handler)

		switch route.Limit {
		case LimitTraveller:
			handler = perTraveller(handler)
		case LimitPublic:
			handler = perPublicAddress(handler)
		default:
			handler = perAddress(handler)
		}
		if route.Auth {
			handler = authed(handler)
		}

		if route.NoStore {
			handler = capability(handler)
		}

		mux.Handle(route.Method+" "+route.Pattern, recordRoute(route, handler))
	}
}

// recordRoute puts the route's pattern on the access line beside the raw
// path.
func recordRoute(route Route, next http.Handler) http.Handler {
	pattern := route.Method + " " + route.Pattern
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.RecordRoute(r.Context(), pattern)
		next.ServeHTTP(w, r)
	})
}

// limitByTraveller counts against the traveller the credential named.
func limitByTraveller(l *httpx.Limiter, log *slog.Logger) httpx.Middleware {
	return httpx.RateLimitBy(l, log, "traveller", func(r *http.Request) (string, bool) {
		tr, held := auth.TravellerFrom(r.Context())
		return tr.ID, held
	})
}

func register(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body credentials
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		tr, err := deps.Auth.Register(r.Context(), body.Email, body.Passphrase)
		if err != nil {
			writeAuthFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusCreated, travellerBody{
			ID: tr.ID, Email: tr.Email, Name: tr.Name,
		})
	}
}

func signIn(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body credentials
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}

		var issued auth.Issued
		var err error
		if body.Code != "" {
			issued, err = deps.Auth.SignInWithCode(r.Context(), body.Email, body.Code)
		} else {
			issued, err = deps.Auth.SignIn(r.Context(), body.Email, body.Passphrase)
		}
		if err != nil {
			writeAuthFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusCreated, sessionBody{
			Token: issued.Token, ExpiresAt: issued.ExpiresAt,
		})
	}
}

// revokeSession is `DELETE /v1/auth/session`, and its sibling rides on A
// query parameter rather than on A second path.
func revokeSession(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		everywhere, ok := revokeScope(r)
		if !ok {
			httpx.WriteFieldError(w, r, "scope")
			return
		}

		if everywhere {
			if _, err := deps.Auth.RevokeEverySession(r.Context(), traveller.ID); err != nil {
				writeAuthFailure(w, r, deps.Log, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		token, presented := auth.Bearer(r)
		if !presented {
			httpx.WriteError(w, r, httpx.CodeUnauthenticated)
			return
		}
		if _, err := deps.Auth.RevokeSession(r.Context(), traveller.ID, token); err != nil {
			writeAuthFailure(w, r, deps.Log, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// revokeScope reads `?scope=`, and refuses A value it does not know rather
// than falling back to the smaller act.
func revokeScope(r *http.Request) (everywhere, ok bool) {
	switch r.URL.Query().Get("scope") {
	case "", "this":
		return false, true
	case "all":
		return true, true
	default:
		return false, false
	}
}

// RequireTraveller resolves the bearer token and puts the traveller on the
// context.
func RequireTraveller(service *auth.Service, log *slog.Logger) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, held := auth.Bearer(r)
			if !held {
				httpx.WriteError(w, r, httpx.CodeUnauthenticated)
				return
			}

			tr, err := service.Authenticate(r.Context(), token)
			switch {
			case errors.Is(err, auth.ErrNoSession):
				httpx.WriteError(w, r, httpx.CodeUnauthenticated)
				return
			case httpx.DependencyIsDown(err):
				logFailure(r, log, err)
				httpx.WriteError(w, r, httpx.CodeTimeout)
				return
			case err != nil:
				logFailure(r, log, err)
				httpx.WriteError(w, r, httpx.CodeInternal)
				return
			}
			ctx := auth.WithTraveller(r.Context(), tr)
			httpx.RecordTraveller(ctx, tr.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeAuthFailure is the one mapping: the sentinel is the domain's word and
// the code is the wire's.
func writeAuthFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var invalid auth.InvalidFieldError

	switch {
	case errors.As(err, &invalid):
		httpx.WriteFieldError(w, r, invalid.Field)
	case errors.Is(err, auth.ErrEmailTaken), errors.Is(err, auth.ErrRegistrationClosed):
		httpx.WriteError(w, r, httpx.CodeConflict)
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrNoSession):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, auth.ErrBusy):
		httpx.WriteError(w, r, httpx.CodeRateLimited)
	case httpx.DependencyIsDown(err):
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}

func logFailure(r *http.Request, log *slog.Logger, err error) {
	if log == nil {
		log = slog.Default()
	}
	log.LogAttrs(r.Context(), slog.LevelError, "auth: the request failed",
		slog.String("path", httpx.LoggedPath(r)),
		slog.String("requestId", httpx.RequestIDFrom(r.Context())),
		slog.String("err", err.Error()),
	)
}

// requestCode is `POST /v1/auth/code`. It answers 202 whatever it did, or the
// status would be the account oracle the service is shaped to avoid.
func requestCode(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email string `json:"email"`
		}
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}

		code, tr, send, err := deps.Auth.RequestCode(r.Context(), body.Email)
		if err != nil {
			writeAuthFailure(w, r, deps.Log, err)
			return
		}
		if send {
			_ = deps.Mail.Send(r.Context(), tr.Email, mail.SignInCode(code, auth.CodeTTL))
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
