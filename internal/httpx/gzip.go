// gzip, and's two headers that make it safe to put a cache in front of this
// server.
package httpx

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressFloor is the smallest body worth compressing, in bytes.
const compressFloor = 1400

// gzipWriters is a pool because a gzip.Writer holds a 32 KiB window and this
// middleware is on the read path of every request.
var gzipWriters = sync.Pool{
	New: func() any {
		w, err := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		if err != nil {
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

// compressWriter buffers until it knows whether the body is worth
// compressing.
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
// it.
func (w *compressWriter) startCompressing() error {
	w.Header().Set("Content-Encoding", "gzip")
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

// close finishes whichever branch the response took.
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
	}
}

func (w *compressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
