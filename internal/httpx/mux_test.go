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
	if got, want := rec.Body.String(), `{"code":"not_found"}`; got != want {
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
	if got, want := rec.Body.String(), `{"code":"not_found"}`; got != want {
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
			if payload["code"] != string(httpx.CodeNotFound) {
				t.Errorf("code = %q, want %q", payload["code"], httpx.CodeNotFound)
			}
		})
	}
}

// A 404 a HANDLER wrote is already the envelope and may carry DEC-12's one
// additive key, so the wrapper must leave it alone. THE `field` IS WHAT MAKES
// THIS LEG ABLE TO FAIL: a wrapper that rewrites every 404 regardless of
// Content-Type produces `{"code":"not_found"}`, which is byte-identical to
// what WriteError writes and invisible to a leg that used one.
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
