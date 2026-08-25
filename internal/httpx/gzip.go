// gzip, and the two headers that make it safe to put a cache in front of this
// server (DEC-94).
//
// WHY IT IS HERE AT ALL, AND IT IS NOT A PERFORMANCE TUNE. `GET /v1/logbook`
// answered HTTP 200 with a VALID ETag and a body cut mid-token when
// body/link-speed exceeded WriteTimeout. Measured by the performance lens
// against a real server holding a plausible ten-year log — 350 trips, 14,200
// photographs, emitted body 11,102,597 bytes — and reproduced three times: at
// 400 kB/s, 8,371,312 bytes received, curl exit 18, `json.load` ->
// "Unterminated string starting at char 8371258", last bytes on disk
// `{"lat":35.0409862745072,"lng`. At 500 kB/s: 9,631,256 bytes, code 200. At
// 200 kB/s: 5,622,648 bytes, code 200. Unthrottled the same request is 151.9ms
// and complete. A 200 with a truncated body is the worst answer in the set,
// because the ETag that comes with it is valid and the phone caches both.
//
// COMPRESSION ALONE DOES NOT REMOVE THE CLASS — it moves the threshold 5-15x.
// Level 1 takes the fixture's 99,271 bytes to 6,806 in 0.3ms and the 11.1 MB
// log to 2,084,727 in 47.2ms. The other half of the fix is the WriteTimeout
// ceiling, said out loud in cmd/api, and DEC-93's 500-point cap on
// `Walk.points` removes the largest growth term. Three things, one class.
//
// LEVEL 1, NOT THE DEFAULT 6. The measurement above is what chooses it: at
// level 1 the whole-log compress is 47.2ms of CPU against a body that saves
// 9 MB of wire, and the marginal ratio from raising the level is small against
// a document that is mostly repeated keys. gzip.BestSpeed is also what keeps
// this off the critical path of a small answer.
//
// IT SITS OUTSIDE THE HANDLER AND INSIDE THE ACCESS LOG. The access log's
// `bytes` then counts what actually went on the wire, which is the number an
// operator wants; putting it outermost would make every access line report the
// uncompressed size and quietly undo the one measurement this file exists to
// support.
package httpx

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressFloor is the smallest body worth compressing, in bytes.
//
// IT IS ARITHMETIC RATHER THAN TASTE. A gzip stream costs 10 bytes of header
// and 8 of trailer before any deflate framing, so a short body comes out
// LARGER — and every error in this API is a body of about twenty bytes
// (`{"code":"not_found"}` is exactly 20), so without a floor the twelve-word
// vocabulary would be sent as ~40 bytes of framing wrapped around nothing.
//
// 1400 is one Ethernet MTU's worth of payload: below it the answer fits in a
// single packet either way, so compressing buys no round trip and costs CPU on
// both ends. Above it, saving bytes starts saving time.
const compressFloor = 1400

// gzipWriters is a pool because a gzip.Writer holds a 32 KiB window and this
// middleware is on the read path of every request. Reset is what makes reuse
// safe; the pool is per-process and never grows past concurrency.
var gzipWriters = sync.Pool{
	New: func() any {
		w, err := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		if err != nil {
			// gzip.NewWriterLevel errors only on an invalid level, and
			// BestSpeed is a constant, so this is unreachable rather than
			// unhandled — and a nil in the pool would be a nil dereference on
			// a request rather than at start-up.
			panic("httpx: gzip.BestSpeed was rejected: " + err.Error())
		}
		return w
	},
}

// Compress answers gzip to a client that asked for it, and sets Vary either
// way.
func Compress() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// VARY GOES ON EVERY ANSWER, INCLUDING THE UNCOMPRESSED ONE, and
			// that is the half a "compress if asked" middleware forgets. A
			// cache that stores the PLAIN answer without being told the
			// representation varies will hand it to the next client
			// identically — harmless — but a cache that stores the COMPRESSED
			// one under the same key hands gzip to a client that cannot read
			// it. Setting it unconditionally means no path can miss it.
			w.Header().Add("Vary", "Accept-Encoding")

			if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
				next.ServeHTTP(w, r)
				return
			}

			cw := &compressWriter{ResponseWriter: w}
			defer cw.close()
			next.ServeHTTP(cw, r)
		})
	}
}

// acceptsGzip reads the header as the comma-separated list with q-values that
// it is, rather than as a string to search.
//
// `strings.Contains(h, "gzip")` is the obvious spelling and it is wrong in
// both directions: it accepts `gzip;q=0`, which is a client saying explicitly
// that it does NOT want gzip, and it would accept a future `x-gzip-notreally`.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		for _, param := range fields[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(param), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			if q, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// compressWriter buffers until it knows whether the body is worth compressing.
//
// THE DECISION CANNOT BE MADE AT WriteHeader TIME, which is where every other
// wrapper in this package decides things. Content-Length is set by only some
// handlers — mux.go's envelope writer sets it, WriteJSON does not — so the only
// reliable measure of "is this body big enough" is the body. So the first
// writes are held in `head` until either the floor is crossed (switch to gzip,
// flush what is held through it) or the handler finishes below it (write the
// held bytes out plain).
type compressWriter struct {
	http.ResponseWriter

	status      int
	wroteHeader bool

	head     []byte
	gz       *gzip.Writer
	passing  bool // decided: send plain
	deciding bool // still buffering
}

func (w *compressWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status

	// A STATUS WITH NO BODY IS NOT COMPRESSED, and 304 is the one that matters:
	// it is the answer DEC-31 exists to produce and the most common one the API
	// gives. Writing a gzip header into it would put a body on a response
	// defined not to have one.
	if status == http.StatusNotModified || status == http.StatusNoContent || status < 200 {
		w.passing = true
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.deciding = true
}

func (w *compressWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.passing {
		return w.ResponseWriter.Write(b)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}

	w.head = append(w.head, b...)
	if len(w.head) < compressFloor {
		return len(b), nil
	}
	if err := w.startCompressing(); err != nil {
		return 0, err
	}
	return len(b), nil
}

// startCompressing commits to gzip and flushes everything held so far through
// it. The two headers are set BEFORE WriteHeader, which is the only moment
// they can be.
func (w *compressWriter) startCompressing() error {
	w.Header().Set("Content-Encoding", "gzip")
	// CONTENT-LENGTH DESCRIBES THE UNCOMPRESSED BODY AND MUST NOT SURVIVE.
	// mux.go's envelope writer sets one on every stdlib 404, and a wrong
	// Content-Length is a client that hangs waiting for bytes that never come.
	w.Header().Del("Content-Length")

	w.deciding = false
	w.ResponseWriter.WriteHeader(w.status)

	w.gz = gzipWriters.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)

	held := w.head
	w.head = nil
	_, err := w.gz.Write(held)
	return err
}

// close finishes whichever branch the response took. It runs from a defer, so
// it also runs when the handler panicked — in which case Recover writes the
// envelope through this same writer and the small-body path sends it plain.
func (w *compressWriter) close() {
	switch {
	case w.gz != nil:
		_ = w.gz.Close()
		gzipWriters.Put(w.gz)
		w.gz = nil
	case w.deciding:
		w.deciding = false
		w.ResponseWriter.WriteHeader(w.status)
		if len(w.head) > 0 {
			_, _ = w.ResponseWriter.Write(w.head)
			w.head = nil
		}
	case !w.wroteHeader:
		// A handler that wrote nothing at all. net/http sends 200 with an
		// empty body, which is what it would have done without this wrapper.
	}
}

func (w *compressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
