package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/httpx"
)

type trip struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func TestWriteJSONWritesTheValueTheStatusAndTheContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)

	httpx.WriteJSON(rec, req, http.StatusOK, trip{ID: "kyoto", Title: "Kyoto in May"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := rec.Body.String(); got != `{"id":"kyoto","title":"Kyoto in May"}` {
		t.Errorf("body = %s", got)
	}
}

// No trailing newline.
func TestWriteJSONEmitsNoTrailingNewline(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	httpx.WriteJSON(rec, req, http.StatusOK, trip{ID: "kyoto"})

	if strings.HasSuffix(rec.Body.String(), "\n") {
		t.Errorf("body = %q, want no trailing newline", rec.Body.String())
	}
}

// The reason WriteJSON marshals to a buffer instead of streaming through an
// Encoder.
func TestWriteJSONThatCannotEncodeAnswers500AndNotAPartialBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	bad := struct {
		ID string   `json:"id"`
		Ch chan int `json:"ch"`
	}{ID: "kyoto", Ch: make(chan int)}

	httpx.WriteJSON(rec, req, http.StatusOK, bad)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Body.String(); got != `{"code":"internal"}` {
		t.Errorf("body = %s, want {\"code\":\"internal\"}", got)
	}
	if strings.Contains(rec.Body.String(), "kyoto") {
		t.Errorf("body carries the half-encoded value: %s", rec.Body.String())
	}
}

func decodeInto(t *testing.T, body string, dst any) error {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/trips/kyoto", strings.NewReader(body))
	return httpx.DecodeJSON(rec, req, dst)
}

func TestDecodeJSONReadsTheBody(t *testing.T) {
	var got trip
	if err := decodeInto(t, `{"id":"kyoto","title":"Kyoto in May"}`, &got); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.ID != "kyoto" || got.Title != "Kyoto in May" {
		t.Errorf("decoded = %+v", got)
	}
}

// : every server addition is additive-and-optional, and that is a promise
// about both directions.
func TestDecodeJSONTOLERATESAnUnknownField(t *testing.T) {
	var got trip
	err := decodeInto(t, `{"id":"kyoto","title":"Kyoto in May","sharing":{"pins":true}}`, &got)
	if err != nil {
		t.Fatalf("DecodeJSON refused an unknown field: %v", err)
	}
	if got.ID != "kyoto" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestDecodeJSONRefusesASecondValueAfterTheFirst(t *testing.T) {
	var got trip
	err := decodeInto(t, `{"id":"kyoto"}{"id":"osaka"}`, &got)
	if err == nil {
		t.Fatal("DecodeJSON accepted two documents in one body")
	}
	if c := httpx.CodeFor(err); c != httpx.CodeInvalidBody {
		t.Errorf("CodeFor = %q, want invalid_body", c)
	}
}

func TestDecodeJSONRefusesAnEmptyBodyAndAWrongType(t *testing.T) {
	for _, body := range []string{``, `{"id":42}`, `not json at all`, `[1,2,3]`} {
		var got trip
		err := decodeInto(t, body, &got)
		if err == nil {
			t.Errorf("DecodeJSON(%q) returned no error", body)
			continue
		}
		if c := httpx.CodeFor(err); c != httpx.CodeInvalidBody {
			t.Errorf("CodeFor(DecodeJSON(%q)) = %q, want invalid_body", body, c)
		}
	}
}

func TestMaxBodyBytesIsOneMebibyte(t *testing.T) {
	if httpx.MaxBodyBytes != 1<<20 {
		t.Errorf("MaxBodyBytes = %d, want %d", httpx.MaxBodyBytes, 1<<20)
	}
}

// The boundary, from both sides.
func TestTheBodyLimitIsEnforcedAtExactlyMaxBodyBytes(t *testing.T) {
	pad := func(n int64) string {
		const wrapper = `{"id":""}`
		return `{"id":"` + strings.Repeat("a", int(n)-len(wrapper)) + `"}`
	}

	atTheLimit := pad(httpx.MaxBodyBytes)
	if int64(len(atTheLimit)) != httpx.MaxBodyBytes {
		t.Fatalf("the fixture is %d bytes, not %d", len(atTheLimit), httpx.MaxBodyBytes)
	}
	var got trip
	if err := decodeInto(t, atTheLimit, &got); err != nil {
		t.Errorf("a body of exactly MaxBodyBytes was refused: %v", err)
	}

	overTheLimit := pad(httpx.MaxBodyBytes + 1)
	err := decodeInto(t, overTheLimit, &got)
	if err == nil {
		t.Fatal("a body of MaxBodyBytes+1 was accepted")
	}
	if c := httpx.CodeFor(err); c != httpx.CodePayloadTooLarge {
		t.Errorf("CodeFor = %q, want payload_too_large", c)
	}
}

// The limit is enforced by reading, not by trusting Content-Length.
func TestTheLimitHoldsWhenTheBodyDeclaresNoLength(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/trips/kyoto",
		strings.NewReader(`{"id":"`+strings.Repeat("a", int(httpx.MaxBodyBytes))+`"}`))
	req.ContentLength = -1

	var got trip
	err := httpx.DecodeJSON(rec, req, &got)
	if err == nil {
		t.Fatal("an over-long body with no declared length was accepted")
	}
	if c := httpx.CodeFor(err); c != httpx.CodePayloadTooLarge {
		t.Errorf("CodeFor = %q, want payload_too_large", c)
	}
}

// Nothing from the decoder's own error text reaches the wire.
func TestNoDecoderProseReachesTheBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/trips/kyoto",
		strings.NewReader(`{"id":"kyoto","title":`))

	var got trip
	err := httpx.DecodeJSON(rec, req, &got)
	if err == nil {
		t.Fatal("a truncated body was accepted")
	}
	httpx.WriteErrorFor(rec, req, err)

	var body map[string]any
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatalf("body is not JSON: %v", e)
	}
	if len(body) != 1 || body["code"] != "invalid_body" {
		t.Errorf("body = %v, want exactly {code: invalid_body}", body)
	}
}
