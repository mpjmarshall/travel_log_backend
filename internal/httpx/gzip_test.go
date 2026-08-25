// The compressor, and the header that stops the next cache in front of this
// from serving compressed bytes to a client that did not ask (DEC-94).
package httpx_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/httpx"
)

// jsonBody is a handler writing a body big enough to be worth compressing and
// compressible enough to prove it happened. The repetition is the point: a
// 40-byte body would be LARGER gzipped, which is a real branch and has its own
// leg below.
func jsonBody(n int) http.Handler {
	body := `{"trips":[` + strings.Repeat(`{"id":"kyoto","name":"Kyoto in May"},`, n) + `{}]}`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `W/"2-7"`)
		_, _ = io.WriteString(w, body)
	})
}

func getThrough(t *testing.T, h http.Handler, accept string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func gunzip(t *testing.T, r io.Reader) []byte {
	t.Helper()
	zr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("opening the gzip stream: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading the gzip stream: %v", err)
	}
	if err := zr.Close(); err != nil {
		t.Fatalf("closing the gzip stream: %v — a truncated stream is exactly the "+
			"failure this middleware must not introduce", err)
	}
	return out
}

// THE ONE THE PERFORMANCE LENS MEASURED. `GET /v1/logbook` answers HTTP 200
// with a VALID ETag and a body cut mid-token when body/link-speed exceeds
// WriteTimeout. Reproduced three times against an 11,102,597-byte log: at
// 400 kB/s, 8,371,312 bytes received, curl exit 18, `json.load` ->
// "Unterminated string starting at char 8371258", last bytes on disk
// `{"lat":35.0409862745072,"lng`. At 500 kB/s: 9,631,256 bytes, code 200. At
// 200 kB/s: 5,622,648 bytes, code 200. Unthrottled the same request is 151.9ms
// and complete.
//
// COMPRESSION DOES NOT REMOVE THE CLASS, IT MOVES THE THRESHOLD 5-15x — level
// 1 takes the fixture's 99,271 bytes to 6,806 in 0.3ms and the 11.1 MB log to
// 2,084,727 in 47.2ms. Both halves of the fix are needed and this is the first;
// the WriteTimeout ceiling is the second and is said out loud in cmd/api.
func TestTheBodyIsCompressedWhenAskedAndSaysSo(t *testing.T) {
	h := httpx.Compress()(jsonBody(400))

	plain := getThrough(t, h, "")
	gz := getThrough(t, h, "gzip")

	if got := gz.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := plain.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("a client that did not ask got %q", got)
	}

	plainBody, err := io.ReadAll(plain.Body)
	if err != nil {
		t.Fatalf("reading the plain body: %v", err)
	}
	if !bytes.Equal(gunzip(t, gz.Body), plainBody) {
		t.Error("the compressed body does not decompress to the plain one")
	}
}

// VARY IS NOT HYGIENE. Without it the next cache in front of this stores one
// representation under one key and serves compressed bytes to a client that
// did not ask for them — which is not a slow client, it is a client that
// cannot read the answer at all.
func TestVaryNamesAcceptEncodingOnBothAnswers(t *testing.T) {
	h := httpx.Compress()(jsonBody(400))

	for _, tc := range []struct{ name, accept string }{
		{"the client that asked", "gzip"},
		{"the client that did not", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := getThrough(t, h, tc.accept).Header.Get("Vary")
			if !strings.Contains(got, "Accept-Encoding") {
				t.Errorf("Vary = %q, want it to name Accept-Encoding — an intermediary "+
					"that does not see this serves gzip to a client that did not ask "+
					"for it, and it is the UNCOMPRESSED answer that teaches the cache "+
					"the wrong lesson", got)
			}
		})
	}
}

// THE ETAG IS A FACT ABOUT THE LOG AND NOT ABOUT THE ENCODING. A middleware
// that appends `-gzip` to the tag — which is what several off-the-shelf ones
// do, and what a hand-rolled one reaches for to keep caches honest — makes a
// client that switches Accept-Encoding re-download an unchanged log. Vary is
// what keeps the caches honest here; the tag stays put.
func TestTheETagIsUnchangedByTheEncoding(t *testing.T) {
	h := httpx.Compress()(jsonBody(400))

	plain := getThrough(t, h, "").Header.Get("ETag")
	gz := getThrough(t, h, "gzip").Header.Get("ETag")

	if plain != gz {
		t.Errorf("etags differ by encoding: %q vs %q — a client that switches "+
			"Accept-Encoding would then re-download an unchanged log", plain, gz)
	}
	if plain != `W/"2-7"` {
		t.Errorf("the tag the handler set came out as %q", plain)
	}
}

// A SMALL BODY IS NOT COMPRESSED, and the reason is arithmetic rather than
// taste: gzip's header, trailer and deflate block overhead make a short body
// LARGER, and every error in this API is a body of about twenty bytes. So the
// vocabulary's twelve words would each be sent as ~40 bytes of gzip framing
// around nothing.
func TestAShortBodyIsSentUncompressed(t *testing.T) {
	h := httpx.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":"not_found"}`)
	}))

	got := getThrough(t, h, "gzip")
	if enc := got.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q on a 20-byte body — gzip framing is larger "+
			"than the body it would wrap", enc)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != `{"code":"not_found"}` {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(got.Header.Get("Vary"), "Accept-Encoding") {
		t.Error("Vary is missing on the small-body path, which is the path most " +
			"responses take")
	}
}

// A 304 HAS NO BODY AND MUST NOT GROW ONE. This is the response DEC-31 exists
// to produce, so a compressor that wrote a gzip header into it would break the
// cheapest and most common answer the API gives.
func TestA304IsNotGivenABody(t *testing.T) {
	h := httpx.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `W/"2-7"`)
		w.WriteHeader(http.StatusNotModified)
	}))

	got := getThrough(t, h, "gzip")
	if got.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d", got.StatusCode)
	}
	if enc := got.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q on a 304", enc)
	}
	body, _ := io.ReadAll(got.Body)
	if len(body) != 0 {
		t.Errorf("a 304 carries %d bytes", len(body))
	}
}

// CONTENT-LENGTH MUST NOT SURVIVE THE COMPRESSION. A handler that sets it —
// mux.go's envelope writer does, on every stdlib 404 — describes the
// UNCOMPRESSED body, and a wrong Content-Length is a connection the client
// hangs on or truncates.
func TestAHandlerSetContentLengthIsDroppedWhenCompressing(t *testing.T) {
	body := strings.Repeat(`{"id":"kyoto"},`, 400)
	h := httpx.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconvItoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))

	got := getThrough(t, h, "gzip")
	if got.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("the body was not compressed, so this leg proves nothing")
	}
	if cl := got.Header.Get("Content-Length"); cl != "" && cl == strconvItoa(len(body)) {
		t.Errorf("Content-Length = %s, which describes the UNCOMPRESSED body — the "+
			"client either hangs waiting for bytes that never come or truncates", cl)
	}
}

// A CLIENT THAT SPELLS IT DIFFERENTLY IS STILL A CLIENT THAT ASKED. Accept-
// Encoding is a comma-separated list with optional q-values, and `deflate,
// gzip;q=0.8` names gzip.
func TestGzipIsRecognisedInsideAList(t *testing.T) {
	h := httpx.Compress()(jsonBody(400))

	for _, accept := range []string{"gzip", "gzip, deflate, br", "deflate, gzip;q=0.8", "GZIP"} {
		t.Run(accept, func(t *testing.T) {
			if got := getThrough(t, h, accept).Header.Get("Content-Encoding"); got != "gzip" {
				t.Errorf("Accept-Encoding: %s -> Content-Encoding %q, want gzip", accept, got)
			}
		})
	}
	for _, accept := range []string{"", "identity", "br", "gzip;q=0"} {
		t.Run("not "+accept, func(t *testing.T) {
			if got := getThrough(t, h, accept).Header.Get("Content-Encoding"); got != "" {
				t.Errorf("Accept-Encoding: %q -> Content-Encoding %q, want none", accept, got)
			}
		})
	}
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
