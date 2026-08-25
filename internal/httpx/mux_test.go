package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"travellog/internal/httpx"
)

// muxUnderTest is one route, so every other request is either a path the mux
// does not know or a method it does not accept — the two responses net/http
// writes for itself.
func muxUnderTest() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logbook", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"hello": "there"})
	})
	return httpx.MuxErrors()(mux)
}

func TestAnUnknownPathAnswersTheEnvelopeAndNotPlainText(t *testing.T) {
	rec := httptest.NewRecorder()
	muxUnderTest().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no/such/route", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	// The WORD changed at DEC-103 and the envelope did not; what this leg is
	// about is the envelope. See TestARouteThisBuildDoesNotHaveSaysSoRatherThanSayingNotFound
	// for why the word is no longer `not_found`.
	if got, want := rec.Body.String(), `{"code":"unsupported_route"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
}

// The 405 net/http writes for a path it knows under a method it does not.
// THE STATUS IS KEPT AND THE Allow HEADER WITH IT — see MuxErrors for why the
// status is the stdlib's fact and the code is the vocabulary's nearest word.
func TestAKnownPathUnderTheWrongMethodAnswersTheEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	muxUnderTest().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/logbook", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got, want := rec.Body.String(), `{"code":"unsupported_route"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Allow"), "GET, HEAD"; got != want {
		t.Errorf("Allow = %q, want %q — the wrapper must not destroy what the mux knows", got, want)
	}
}

// The measured strings, asserted as absences. Without them the leg above
// passes against a wrapper that APPENDS the envelope to the plain text.
func TestTheStdlibsOwnPlainTextNeverReachesTheClient(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, plain string
	}{
		{"unknown path", http.MethodGet, "/no/such/route", "404 page not found\n"},
		{"wrong method", http.MethodPost, "/v1/logbook", "Method Not Allowed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			muxUnderTest().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if body := rec.Body.String(); body == tc.plain {
				t.Fatalf("body = %q, which is what net/http writes for itself", body)
			}
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("body %q does not decode as JSON: %v", rec.Body.String(), err)
			}
			if payload["code"] != string(httpx.CodeUnsupportedRoute) {
				t.Errorf("code = %q, want %q", payload["code"], httpx.CodeUnsupportedRoute)
			}
		})
	}
}

// A 404 a HANDLER wrote is already the envelope and may carry DEC-12's one
// additive key, so the wrapper must leave it alone. THE `field` IS WHAT MAKES
// THIS LEG ABLE TO FAIL: a wrapper that rewrites every 404 regardless of
// Content-Type produces `{"code":"not_found"}`, which is byte-identical to
// what WriteError writes and invisible to a leg that used one.
//
// AND IT IS THE OTHER HALF OF DEC-103. `bodyUnsupportedRoute` is written only
// when `stdlibWroteIt` is true — 404-or-405 with a NON-JSON Content-Type,
// which is only when net/http answered because no pattern matched — so "the
// mux answered" already means "this build does not have that route". A route
// this build HAS, answering about an id it does not hold, is the one case
// where `not_found` is the right word, and it is the case the client's
// NotFoundScreen exists for.
func TestAHandlersOwn404IsNotRewritten(t *testing.T) {
	const written = `{"code":"not_found","field":"tripId"}`

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(written))
	})

	rec := httptest.NewRecorder()
	httpx.MuxErrors()(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/gone", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Body.String(); got != written {
		t.Errorf("body = %q, want %q — a handler's own JSON 404 keeps its field", got, written)
	}
}

func TestAnOrdinarySuccessIsUntouched(t *testing.T) {
	rec := httptest.NewRecorder()
	muxUnderTest().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/logbook", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Body.String(), `{"hello":"there"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// === DEC-103: the mux answering is not the same thing as the trip being gone ===

// MEASURED against a server at R1 entry state, and reproduced from outside the
// test binary before this changed: `DELETE /v1/trips/{id}` -> `405
// {"code":"not_found"}`, `PATCH /v1/me` -> `404 {"code":"not_found"}`, `PUT
// /v1/places/x` -> `404 {"code":"not_found"}`. Every route this plan has not
// built yet answered `not_found` — THE SAME WORD the vocabulary uses for "that
// trip is not in your log".
//
// TWO CONSEQUENCES AND THE SECOND IS THE BAD ONE. A client build ahead of the
// server tells the user their trip, place, photograph or walk is GONE, on
// eighteen routes, and `NotFoundScreen` says exactly that — verified on
// `wipe/mock-data`, not_found_screen.dart:84 renders "That $what is not in your
// log". And `deletePhoto`, `removePlace` and `deleteTrip` all treat an unknown
// id as SUCCESS by decision (logbook.dart:119, :156, :201 each return
// `Future<bool>.value(true)`), so the obvious network mapping of that rule
// makes a delete against an undeployed route REPORT SUCCESS, DELETE NOTHING,
// and advance the client's cache.
func TestARouteThisBuildDoesNotHaveSaysSoRatherThanSayingNotFound(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		status             int
	}{
		{"a path no version of this API has", http.MethodGet, "/no/such/route", http.StatusNotFound},
		{"a route a later build will have", http.MethodPut, "/v1/places/fushimi", http.StatusNotFound},
		{"a verb this build does not implement", http.MethodDelete, "/v1/logbook", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			muxUnderTest().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			var payload map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("body %q does not decode as JSON: %v", rec.Body.String(), err)
			}
			if payload["code"] != string(httpx.CodeUnsupportedRoute) {
				t.Errorf("code = %q, want %q — `not_found` is what the vocabulary "+
					"says about a TRIP, and a client that reads it as one tells "+
					"the user their trip is gone",
					payload["code"], httpx.CodeUnsupportedRoute)
			}
		})
	}
}

// THE 405 KEEPS ITS STATUS AND ITS Allow HEADER. mux.go's recorded decision
// stands: the status is the stdlib's FACT about the request, and `Allow` is
// information a 404 would throw away. What changed is the word in the body, so
// the status and the code stop contradicting each other.
func TestThe405KeepsItsStatusAndItsAllowHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	muxUnderTest().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/logbook", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if got, want := rec.Header().Get("Allow"), "GET, HEAD"; got != want {
		t.Errorf("Allow = %q, want %q — the wrapper must not destroy what the mux knows",
			got, want)
	}
}
