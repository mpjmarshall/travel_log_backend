package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The mechanism that keeps the two hand-written bodies honest. They cannot come
// from the encoder — http.TimeoutHandler wants a string before any request
// exists, and encoding/json is confined to two functions — so instead of
// trusting them, this asserts each is BYTE-IDENTICAL to what WriteJSON writes
// for the same payload. Change the envelope and this reddens.
func TestThePrebuiltBodiesEqualWhatTheEncoderProduces(t *testing.T) {
	for _, c := range []Code{CodeTimeout, CodeInternal, CodeNotFound} {
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
