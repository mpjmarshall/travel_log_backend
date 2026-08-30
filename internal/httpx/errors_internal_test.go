package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The mechanism that keeps's three hand-written bodies honest.
func TestThePrebuiltBodiesEqualWhatTheEncoderProduces(t *testing.T) {
	for _, c := range []Code{CodeTimeout, CodeInternal, CodeUnsupportedRoute} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		WriteJSON(rec, req, StatusFor(c), errorPayload{Code: c})

		if got, want := prebuiltBody(c), rec.Body.String(); got != want {
			t.Errorf("prebuiltBody(%q) = %s, but the encoder writes %s", c, got, want)
		}
		if prebuiltBody(c) == "" {
			t.Errorf("prebuiltBody(%q) is empty; the three bodies are what the "+
				"timeout, the encoder-failure path and the mux's own errors write", c)
		}
	}
}
