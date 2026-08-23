// The two responses `http.ServeMux` writes for itself, brought inside DEC-12's
// envelope.
//
// THE GAP IS STRUCTURAL RATHER THAN AN OVERSIGHT. An unknown path and a known
// path under an unregistered method are both answered by net/http BEFORE any
// handler runs — `http.Error(w, "404 page not found", 404)` and
// `http.Error(w, "Method Not Allowed", 405)`, each with
// `Content-Type: text/plain; charset=utf-8` — so no call to WriteError is
// involved and DEC-12's AST sweep cannot see them. Measured on Go 1.26.5:
//
//	GET  /nope        -> 404 text/plain "404 page not found\n"
//	POST /v1/logbook  -> 405 text/plain "Method Not Allowed\n"  Allow: GET, HEAD
//
// The client decodes every body as JSON and switches on the code, so a
// text/plain body is not a WRONG answer to it — it is an UNPARSEABLE one,
// which is a worse failure than any word in the vocabulary. This is the same
// class as http.TimeoutHandler's body, and it takes the same shape of fix: a
// ResponseWriter wrapper deciding at WriteHeader time, exactly as jsonByDefault
// does in middleware.go.
//
// TWO DECISIONS IN IT, AND BOTH HAD A REAL ALTERNATIVE:
//
//   - THE 405 KEEPS ITS STATUS AND ITS `Allow` HEADER. Rewriting it to a 404
//     would make the vocabulary and the status agree — StatusFor(not_found) is
//     404 — at the cost of telling a client the path does not exist when the
//     mux has just said which methods it takes. The status is the stdlib's
//     fact about the request; the body is the vocabulary's nearest word. A
//     THIRTEENTH word (`method_not_allowed`) was the other alternative and is
//     refused by DEC-12: the block is closed, and a 405 is a client that
//     disagrees with the API rather than a condition a user can be told about.
//
//   - IT DECIDES ON THE Content-Type, NOT ON THE STATUS ALONE. A handler's own
//     404 is already the envelope and may carry `field`; rewriting every 404
//     would throw that away. http.Error sets text/plain before WriteHeader and
//     WriteError sets application/json before WriteHeader, so by the time this
//     wrapper is asked, the two are already distinguishable.
package httpx

import (
	"io"
	"net/http"
	"strconv"
	"strings"
)

// MuxErrors wraps the mux so the 404 and 405 net/http writes itself carry the
// envelope. It goes INNERMOST in the chain, directly around the mux: a 404 is a
// request that happened and should be recovered, identified, logged and timed
// like any other.
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
	w.Header().Set("Content-Length", strconv.Itoa(len(bodyNotFound)))
	w.ResponseWriter.WriteHeader(status)
	_, _ = io.WriteString(w.ResponseWriter, bodyNotFound)
}

// stdlibWroteIt is the whole of the discrimination: one of the two statuses
// net/http answers on its own, with the Content-Type net/http sets rather than
// the one WriteJSON sets.
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
