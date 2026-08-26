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
	"travellog/internal/logbook"
	"travellog/internal/media"
)

// Deps is what the routes need. Neither limiter is optional — see Mount.
type Deps struct {
	Auth    *auth.Service
	Logbook logbook.Store
	Log     *slog.Logger

	// Share is H1's three writes, and it is a SEPARATE PORT from Logbook
	// rather than three more methods on it. Both are satisfied by a struct
	// over the same pool, so this costs nothing at wiring time; what it buys
	// is that the interface a handler is handed says what that handler can
	// reach — the share handlers cannot read the whole log, and the logbook
	// handlers cannot mint a capability. See logbook.ShareStore.
	Share logbook.ShareStore

	// THE GEOGRAPHY GROUP (R6): T5's city and C1's pin, and they are two ports
	// rather than one for the reason Share is separate from Logbook. The city
	// handler cannot remove a place — which is the only D2-scale destruction
	// in this step — and the place handlers cannot attach anything to a trip.
	Cities logbook.CityStore
	Places logbook.PlaceStore

	// THE PHOTOGRAPH AND WALK PORTS (R7), and they are two rather than one
	// for the reason Cities and Places are two. The walk handler cannot delete
	// a photograph and the photo handlers cannot empty a track — and `walks`
	// has no `place_id` at all, so the two entities do not touch anywhere in
	// the schema either.
	Photos logbook.PhotoStore
	Walks  logbook.WalkStore

	AuthLimit      *httpx.Limiter
	TravellerLimit *httpx.Limiter

	// THE MEDIA GROUP (R3). Two ports and one number, and none of the three
	// is optional on a build that mounts the media routes — Mount panics on a
	// nil, for the reason it panics on a nil limiter: an optional field left
	// unset reads as working software.
	//
	// TWO PORTS AND NOT ONE, because they are two systems with two failure
	// modes. `Media` is the row — what was declared, and whether it has been
	// committed. `Objects` is the bucket — what is actually there, and the
	// only thing that can mint a capability. `logbook.Service` is what spans
	// them, and it is the ONLY thing in R1-R8 that does (PD-05).
	Media   logbook.MediaStore
	Objects media.Store

	// MediaMaxBytes is MEDIA_MAX_BYTES: an API-side refusal to MINT, taken
	// before the capability exists. It is a number on Deps rather than a
	// package constant because it is configuration (spec L30) and because
	// internal/logbook reads no environment.
	MediaMaxBytes int64

	// Now is the clock the begin response's `expiresAt` is measured from. It
	// is a field so a leg can pin it; nil means time.Now.
	Now func() time.Time
}

// Clock is Now, or time.Now.
//
// A METHOD RATHER THAN A DEFAULT SET AT WIRING TIME, because Deps is a struct
// literal at four call sites and a zero value that panics on use would be a
// nil-pointer dereference inside a handler rather than a working default.
func (d Deps) Clock() func() time.Time {
	if d.Now == nil {
		return time.Now
	}
	return d.Now
}

// mediaService is PD-05's Service, built from the two ports rather than stored
// as a third field.
//
// A THIRD FIELD WOULD BE A THIRD THING TO WIRE AND A FOURTH WAY TO GET IT
// WRONG: a Deps carrying Media, Objects AND a Service built from some other
// pair is a state the type would allow and nothing would catch. It is a value
// struct of two interfaces, so building it per request costs nothing.
func (d Deps) mediaService() logbook.Service {
	return logbook.Service{Media: d.Media, Objects: d.Objects}
}

// places is the same Service built from the port D2's removal needs, and it is
// a SECOND accessor rather than one that fills every field.
//
// A `Service{Media, Objects, Places}` handed to both routes would let the
// media commit reach a place store and D2's removal reach the bucket, which is
// the exact thing the two-port split on Deps exists to prevent one layer up.
// Each accessor gives the operation the ports that operation names, and a
// Service is a value struct of interfaces, so building one per request costs
// nothing.
func (d Deps) places() logbook.Service {
	return logbook.Service{Places: d.Places}
}

// photos is the same Service built from the port M2.2's re-file needs, and it
// is the THIRD such accessor for the reason the second exists: a
// `Service{Media, Objects, Places, Photos}` handed to every route would let
// the media commit reach a photograph and the re-file reach the bucket, which
// is the exact thing the port split on Deps exists to prevent one layer up.
func (d Deps) photos() logbook.Service {
	return logbook.Service{Photos: d.Photos}
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

// Mount adds every route in the table, each behind a ceiling and the
// authenticated ones behind the credential check as well.
//
// A NIL LIMITER PANICS RATHER THAN MEANING "NO LIMIT". DEC-48 is a ruling, and
// the way a ruling like this regresses is silently: an optional field left
// unset reads as working software and removes the only bound on unauthenticated
// Argon2 work. Failing at wiring time is loud, immediate, and cannot reach
// production. That argument covers both limiters, and the second one is the
// wiring leg for cmd/api: apiRoutes forgetting to build it is a panic at boot
// rather than a route served with no ceiling.
//
// RATE LIMITING AND AUTHENTICATION ARE COMPOSED, NOT ALTERNATED, AND THAT IS
// THE FIX FOR A DEFECT THIS FUNCTION SHIPPED. It read:
//
//	if route.Auth { handler = authed(handler) } else { handler = limited(handler) }
//
// so every authenticated route had no ceiling whatever — measured against the
// running stack at 7b47bee: the credential routes 429 after their burst, and
// 60 concurrent GET /v1/logbook drew 60 200s. Against a thirty-day untuned
// session TTL with no revocation surface, that is unlimited whole-log reads
// for a stolen token, and unlimited cascading deletes once the write routes
// land.
//
// THE TWO CEILINGS ARE TWO LIMITERS BECAUSE THEY BOUND TWO THINGS. The
// credential limiter bounds an unauthenticated 64 MiB-per-attempt Argon2
// surface and is deliberately low (AUTH_RATE_LIMIT_PER_MIN, 10). The
// authenticated one bounds a stolen token, so it has to be high enough that no
// honest client ever meets it (TRAVELLER_RATE_LIMIT_PER_MIN, 600) — reusing the
// credential ceiling would be a phone that stops syncing.
//
// AND THE AUTHENTICATED LIMITER SITS INSIDE THE AUTHENTICATION RATHER THAN
// OUTSIDE IT. It keys on the traveller, which is on the context only after
// RequireTraveller has resolved the credential, so the composition is
// authed(limited(handler)) and not the other way round. Two consequences, both
// wanted: a stolen token used from a thousand addresses is one bucket, and a
// flood from somebody holding no credential cannot spend a traveller's
// allowance. One consequence that is not: the session lookup happens before the
// ceiling, so what this bounds is the log read and the cascade rather than the
// token lookup. Bounding THAT is a general per-address ceiling on the whole
// API, which is a third budget and does not exist yet — see CLAUDE.md.
func Mount(mux *http.ServeMux, deps Deps) {
	if deps.AuthLimit == nil {
		panic("httpapi: the auth routes need a rate limiter (DEC-48); a nil one is not 'no limit'")
	}
	if deps.TravellerLimit == nil {
		panic("httpapi: the authenticated routes need a rate limiter of their own; " +
			"a nil one is not 'no limit', it is an unlimited log read for a stolen token")
	}
	if deps.Logbook == nil {
		panic("httpapi: the logbook routes need a store; a nil one is not 'no logbook', " +
			"it is a 500 the first time somebody asks for their log")
	}
	// THE SHARE PORT PANICS FOR THE REASON EVERY OTHER NIL HERE DOES: an
	// optional field left unset reads as working software, and the first
	// symptom would be a nil-pointer dereference inside H1's 'Stop sharing' —
	// on the one control in the app whose job is to revoke a live capability.
	if deps.Share == nil {
		panic("httpapi: the share routes need a store; a nil one is not 'no sharing', " +
			"it is a panic the first time somebody presses Stop sharing")
	}
	// THE GEOGRAPHY PORTS PANIC FOR THE SAME REASON, and the place one has the
	// sharper consequence: `DELETE /v1/places/{id}` is D2, and a nil store
	// there is a panic on the one control in the app that asks the user
	// whether thirty photographs live or die.
	if deps.Cities == nil {
		panic("httpapi: the city route needs a store; a nil one is not 'no cities', " +
			"it is a panic the first time T5 adds one")
	}
	if deps.Places == nil {
		panic("httpapi: the place routes need a store; a nil one is not 'no places', " +
			"it is a panic inside D2's removal")
	}
	// THE PHOTOGRAPH AND WALK PORTS PANIC FOR THE SAME REASON, and the walk
	// one has the sharper consequence: a track is a recording of a day that
	// has passed, so a nil store there is a panic on the one route standing
	// between N1's two controls and the only copy of it.
	if deps.Photos == nil {
		panic("httpapi: the photo routes need a store; a nil one is not 'no " +
			"photographs', it is a panic the first time M2 writes a note")
	}
	if deps.Walks == nil {
		panic("httpapi: the walk route needs a store; a nil one is not 'no walks', " +
			"it is a panic on the route that keeps a day's recording alive")
	}
	// THE MEDIA PORTS PANIC FOR THE REASON THE LIMITERS DO. A nil object store
	// is not "no media": it is a nil-pointer dereference inside a handler on
	// the first upload, wearing a 500 that says nothing, on a route whose
	// whole job is to hand out a capability. Failing at wiring time is loud,
	// immediate, and cannot reach production.
	if deps.Media == nil {
		panic("httpapi: the media routes need a store; a nil one is not 'no media'")
	}
	if deps.Objects == nil {
		panic("httpapi: the media routes need an object store — nothing else can mint " +
			"an upload capability, and a nil one is a panic on the first photograph")
	}
	// A CEILING OF ZERO IS NOT "NO CEILING", IT IS A FEATURE SWITCHED OFF BY A
	// SETTING THAT READS LIKE A SAFETY MEASURE. internal/config already floors
	// MEDIA_MAX_BYTES at the fixture's own largest object and says exactly
	// that; this is the guard the second caller gets, because the guard that
	// only exists in the caller is the guard the second caller does not get.
	if deps.MediaMaxBytes <= 0 {
		panic("httpapi: MEDIA_MAX_BYTES is not set on Deps, and a bound of zero " +
			"refuses every upload — which is a feature switched off by a setting " +
			"that reads like a safety measure")
	}
	perAddress := httpx.RateLimit(deps.AuthLimit, deps.Log)
	perTraveller := limitByTraveller(deps.TravellerLimit, deps.Log)
	authed := RequireTraveller(deps.Auth, deps.Log)
	capability := httpx.CapabilityHeaders()
	for _, route := range Routes(deps) {
		handler := http.Handler(route.Handler)

		// THE CEILING COMES FROM THE ROW AND NOT FROM `Auth` (PD-09). On every
		// row today the two agree, and this line is what makes the field real
		// rather than decoration: change a row's Limit and the route wears a
		// different ceiling. It also decides the COMPOSITION, and that is not
		// cosmetic — the traveller limiter keys on the traveller, which is on
		// the context only after RequireTraveller has resolved the credential,
		// so it can only go INSIDE the authentication.
		switch route.Limit {
		case LimitTraveller:
			handler = perTraveller(handler)
		default:
			handler = perAddress(handler)
		}
		if route.Auth {
			handler = authed(handler)
		}

		// DEC-51's POLICY, FROM THE TABLE, AND OUTSIDE THE CEILING AND THE
		// CREDENTIAL CHECK. A 401 and a 429 from a capability route are still
		// responses about a capability route, and a policy that only applies
		// once a handler runs is a policy whose absence nobody can see.
		if route.NoStore {
			handler = capability(handler)
		}

		mux.Handle(route.Method+" "+route.Pattern, recordRoute(route, handler))
	}
}

// recordRoute puts the route's PATTERN on the access line beside the raw path
// (DEC-101). Without it `path` is the raw URL, so once `/v1/trips/{id}` exists
// nothing aggregates and "how slow is the trip write" has no query that
// answers it.
//
// IT COMES FROM THE TABLE AND NOT FROM `r.Pattern`. http.ServeMux fills that
// field on a request it CLONES on the way down, so the outer request the
// access log holds never carries it — and the table is already the authority
// for the string.
//
// IT IS OUTSIDE THE LIMITERS AND THE AUTH CHECK, so a 429 and a 401 are
// attributed to the route they were aimed at. A rate limit nobody can
// attribute is a rate limit nobody can tune.
func recordRoute(route Route, next http.Handler) http.Handler {
	pattern := route.Method + " " + route.Pattern
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.RecordRoute(r.Context(), pattern)
		next.ServeHTTP(w, r)
	})
}

// limitByTraveller counts against the traveller the credential named.
//
// This is the whole of what httpx cannot do for itself: it imports no domain,
// so it takes the key as a function and this package supplies the one that
// knows what a traveller is. A request with no traveller on the context is a
// middleware mounted outside RequireTraveller, and httpx.RateLimitBy answers
// that with a 500 rather than a shared bucket.
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

// revokeSession is `DELETE /v1/auth/session`, AND ITS SIBLING RIDES ON A QUERY
// PARAMETER RATHER THAN ON A SECOND PATH.
//
// The security lens's argument for "revoke them all" is short and right: A
// STOLEN TOKEN IS PRECISELY THE CASE WHERE YOU DO NOT KNOW WHICH ROW TO
// DELETE, and 'this token' is the one thing the thief will not use. Against a
// thirty-day untuned TTL it is the only recovery a user has.
//
// WHY `?scope=all` AND NOT `DELETE /v1/auth/sessions`. A plural path would be
// a route the plan's own table does not hold — the surface is counted at 23 at
// the end of R8 and this step is allotted six rows — and, more usefully, the
// two are one decision with two answers rather than two resources. The
// precedent is R6's `?photos=keep|delete` on D2's delete, which is the same
// shape: one destructive act, a parameter choosing how far it reaches.
//
// WHERE IT DIFFERS FROM THAT PRECEDENT IS THAT THE PARAMETER IS OPTIONAL, and
// the reason is which way the default is safe. R6's is REQUIRED because "a
// default is a silent answer to the question D2 makes the user answer on
// screen" — there the two branches destroy different amounts and neither is
// the obvious one. Here the route's name is singular, the default is the
// SMALLER act, and a caller who omits the parameter gets exactly what the path
// says. A required parameter would also break the plainest possible sign-out.
//
// IT ANSWERS 204 AND NO BODY, EVEN FOR `all`. There is nothing to splice: a
// session is not in the emitted log, so no version moves and no ETag changes.
// A count of revoked sessions was put and declined — it is a number with no
// reader, and this project's own standard is that a field is real when
// something READS it. The count is still returned by the STORE, where a leg
// reads it.
//
// A SECOND REVOKE OF THE SAME TOKEN CANNOT REACH THIS HANDLER, because
// RequireTraveller resolves the credential first and a revoked token is not a
// live session — so the honest answer to "is this idempotent?" is that the
// second attempt is a 401, which is the same thing the caller wanted.
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
			// IT REVOKES THE CALLER'S OWN TOKEN TOO, and that is the point
			// rather than a side effect: "sign out everywhere" that leaves the
			// device you pressed it on signed in is a control that has not
			// done what it says.
			if _, err := deps.Auth.RevokeEverySession(r.Context(), traveller.ID); err != nil {
				writeAuthFailure(w, r, deps.Log, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// The token is read from the header again rather than carried on the
		// context: `auth.Bearer` is the one parser, and a second copy of the
		// credential in a context value is a second place for it to leak from.
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

// revokeScope reads `?scope=`, and REFUSES A VALUE IT DOES NOT KNOW rather
// than falling back to the smaller act.
//
// `?scope=al` — a typo — must not quietly sign one device out while the user
// believes every device is out. That is the one failure mode this parameter
// has, and the whole of why an unknown value is a 422 naming the field instead
// of a default.
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
			case httpx.DependencyIsDown(err):
				// DEC-96, and this is the site that matters most: EVERY
				// authenticated request passes through here, so an outage
				// that answered 500 here answered 500 for the whole API.
				logFailure(r, log, err)
				httpx.WriteError(w, r, httpx.CodeTimeout)
				return
			case err != nil:
				logFailure(r, log, err)
				httpx.WriteError(w, r, httpx.CodeInternal)
				return
			}
			// DEC-101. The access log sits ABOVE this and its deferred line
			// runs against the request it was handed, not the one made below
			// — so the id is written into a slot rather than into a new
			// context value, which would never travel back up.
			ctx := auth.WithTraveller(r.Context(), tr)
			httpx.RecordTraveller(ctx, tr.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
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
	case errors.Is(err, auth.ErrEmailTaken), errors.Is(err, auth.ErrRegistrationClosed):
		// TWO SENTINELS, ONE WORD, AND THAT IS DEC-86's ORACLE SHRINKING
		// RATHER THAN A COLLAPSE. `ErrEmailTaken` told a caller that THAT
		// ADDRESS is registered here, which the security lens flagged as an
		// enumeration surface; `ErrRegistrationClosed` tells them only that
		// the instance is in use, which the sign-in page already tells them.
		// They share a branch so that the two are indistinguishable on the
		// wire — the same status, the same body, the same bytes.
		httpx.WriteError(w, r, httpx.CodeConflict)
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrNoSession):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, auth.ErrBusy):
		httpx.WriteError(w, r, httpx.CodeRateLimited)
	case httpx.DependencyIsDown(err):
		// DEC-96. Sign-in against a database that is down is not a bad
		// passphrase and is not a server bug.
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
		slog.String("path", r.URL.Path),
		slog.String("requestId", httpx.RequestIDFrom(r.Context())),
		slog.String("err", err.Error()),
	)
}
