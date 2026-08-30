// The two responses `http.ServeMux` writes for itself, brought inside's
// envelope.
package httpx

import (
	"io"
	"net/http"
	"strconv"
	"strings"
)

// MuxErrors wraps the mux so the 404 and 405 net/http writes itself carry the
// envelope.
func MuxErrors() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(&envelopeWriter{ResponseWriter: w}, r)
		})
	}
}

// envelopeWriter replaces a plain-text 404 or 405 with the envelope and
// swallows the body that was about to follow it.
type envelopeWriter struct {
	http.ResponseWriter
	wroteHeader bool
	swallowing  bool
}

func (w *envelopeWriter) WriteHeader(status int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true

	if !w.stdlibWroteIt(status) {
		w.ResponseWriter.WriteHeader(status)
		return
	}

	w.swallowing = true
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Content-Length", strconv.Itoa(len(bodyUnsupportedRoute)))
	w.ResponseWriter.WriteHeader(status)
	_, _ = io.WriteString(w.ResponseWriter, bodyUnsupportedRoute)
}

// stdlibWroteIt is the whole of the discrimination.
func (w *envelopeWriter) stdlibWroteIt(status int) bool {
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return false
	}
	return !strings.HasPrefix(w.Header().Get("Content-Type"), contentTypeJSON)
}

func (w *envelopeWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.swallowing {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *envelopeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
