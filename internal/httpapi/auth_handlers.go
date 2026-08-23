// The two auth routes, and the middleware the rest of the API will wear.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). Every rule about
// what a passphrase may be, what an unknown address answers and how long a
// session lives is in internal/auth; what is here is the body shapes, the
// route table, and the ONE function that turns a domain sentinel into a word
// from DEC-12's closed vocabulary.
//
// DEC-74 NAMES THE PACKAGE `httpapi` AND THE LINE AGAINST httpx IS THE POINT:
// httpx is envelope, error vocabulary, middleware and rate limiting and
// imports no domain; httpapi knows what a traveller is. internal/auth
// therefore imports neither — auth/bearer.go reads the credential and carries
// the traveller, and writes no response at all.
//
// DEC-48 IS APPLIED HERE, ON BOTH ROUTES, AND IT IS TWO GUARDS RATHER THAN
// ONE. The limiter counts CALLERS per address; the Argon2 gate inside
// auth.Capped counts CALLS. N addresses buy N times the rate quota, so only
// the second one bounds total Argon2 memory — and neither is load-bearing
// alone. Both answer `rate_limited`, deliberately: to a client they are one
// condition, come back in a moment.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"travellog/internal/auth"
	"travellog/internal/httpx"
)

// Deps is what the routes need. AuthLimit is not optional — see Mount.
type Deps struct {
	Auth      *auth.Service
	Log       *slog.Logger
	AuthLimit *httpx.Limiter
}

// credentials is both request bodies. DEC-61 settles the field names and
// settles that register takes THESE TWO AND NOTHING ELSE: a `name` here would
// be a second writer of a field PATCH /v1/me owns, and two writers of one
// field drift.
//
// Unknown keys are accepted rather than refused (DEC-13, in
// httpx.DecodeJSON): every server addition is additive-and-optional, which is
// a promise about both directions.
type credentials struct {
	Email      string `json:"email"`
	Passphrase string `json:"passphrase"`
}

// travellerBody is register's 201. `name` is a pointer and NOT omitempty: the
// client reads a missing name as "a log nobody has named yet", so `null` is
// the statement and an absent key is a different one.
type travellerBody struct {
	ID    string  `json:"id"`
	Email string  `json:"email"`
	Name  *string `json:"name"`
}

// sessionBody is the ONLY place the plaintext token is ever written. It is not
// stored, not logged, and cannot be recovered from the row.
type sessionBody struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Mount adds the two auth routes, both rate limited.
//
// A NIL LIMITER PANICS RATHER THAN MEANING "NO LIMIT". DEC-48 is a ruling, and
// the way a ruling like this regresses is silently: an optional field left
// unset reads as working software and removes the only bound on unauthenticated
// Argon2 work. Failing at wiring time is loud, immediate, and cannot reach
// production.
func Mount(mux *http.ServeMux, deps Deps) {
	if deps.AuthLimit == nil {
		panic("httpapi: the auth routes need a rate limiter (DEC-48); a nil one is not 'no limit'")
	}
	limited := httpx.RateLimit(deps.AuthLimit, deps.Log)
	mux.Handle("POST /v1/auth/register", limited(http.HandlerFunc(register(deps))))
	mux.Handle("POST /v1/auth/session", limited(http.HandlerFunc(signIn(deps))))
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
		issued, err := deps.Auth.SignIn(r.Context(), body.Email, body.Passphrase)
		if err != nil {
			writeAuthFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusCreated, sessionBody{
			Token: issued.Token, ExpiresAt: issued.ExpiresAt,
		})
	}
}

// RequireTraveller resolves the bearer token and puts the traveller on the
// context. VS7's routes wear it; VS6 builds it and proves it.
//
// THE 401 AND THE 500 ARE DIFFERENT ANSWERS AND THE DIFFERENCE MATTERS TO THE
// CLIENT. A credential that is not live is a 401 and the phone's answer is to
// sign in again. A database that has gone away is a 500 and the phone's answer
// is to wait — reporting it as a 401 would have the client discard a perfectly
// good session it cannot get back.
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
			case err != nil:
				logFailure(r, log, err)
				httpx.WriteError(w, r, httpx.CodeInternal)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithTraveller(r.Context(), tr)))
		})
	}
}

// writeAuthFailure is DEC-62's one mapping: the sentinel is the domain's word
// and the code is the wire's.
//
// EVERY BRANCH PASSES A NAMED CONSTANT rather than computing a Code and
// handing it over. That is what keeps httpx's AST sweep able to see this file:
// its one exemption is WriteErrorFor, and a second site passing a variable
// would have to argue for itself.
//
// DETAIL GOES TO THE LOG AND NEVER TO THE BODY, and only for the branch that
// is a server fault. A wrong passphrase is not an error condition — logging
// one at ERROR level per attempt turns an ordinary typo into an alert, and
// turns a password-spraying run into a log flood that hides the thing worth
// seeing.
func writeAuthFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var invalid auth.InvalidFieldError

	switch {
	case errors.As(err, &invalid):
		httpx.WriteFieldError(w, r, invalid.Field)
	case errors.Is(err, auth.ErrEmailTaken):
		httpx.WriteError(w, r, httpx.CodeConflict)
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrNoSession):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, auth.ErrBusy):
		httpx.WriteError(w, r, httpx.CodeRateLimited)
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
		slog.String("path", r.URL.Path),
		slog.String("requestId", httpx.RequestIDFrom(r.Context())),
		slog.String("err", err.Error()),
	)
}
