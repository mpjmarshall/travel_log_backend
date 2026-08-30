// Instant's two ends, against's two inputs no fixture in this repository
// contains.
package logbook_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

// The server keeps refusing A zoneless date, and the client is the one that
// changes.
func TestAZonelessDateIsRefusedAndNotReinterpreted(t *testing.T) {
	var w logbook.TripWrite
	err := json.Unmarshal([]byte(`{"id":"autumn","start":"2027-10-12T00:00:00.000"}`), &w)
	if err == nil {
		t.Fatalf("a zoneless date decoded to %v — the server must refuse it rather "+
			"than assume a zone; a silent reinterpretation is a date that means one "+
			"thing on the phone and another in the log", logbook.Value(w.Start))
	}
	if !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("error = %q, want it to name RFC 3339 — the message is what tells "+
			"a client developer to call .toUtc()", err)
	}
}

// The precision half, which the fixture also cannot supply.
func TestASixDigitFractionDecodesAndIsReEmittedTruncated(t *testing.T) {
	var six logbook.TripWrite
	if err := json.Unmarshal([]byte(`{"id":"a","start":"2027-10-12T00:00:00.123456Z"}`), &six); err != nil {
		t.Fatalf("a six-digit fraction was refused: %v — millisecond precision is "+
			"the contract and the client truncates before sending, so this must "+
			"decode", err)
	}
	start := logbook.Value(six.Start)
	if start == nil {
		t.Fatalf("start decoded to nil")
	}
	raw, err := start.MarshalJSON()
	if err != nil {
		t.Fatalf("re-emitting: %v", err)
	}
	if got := strings.Trim(string(raw), `"`); got != "2027-10-12T00:00:00.123Z" {
		t.Errorf("re-emitted as %q, want the truncation stated as the contract "+
			"— .123456 truncates to .123 and does not round to .123", got)
	}
}

// The rounding half, which is the one a `.000` layout gets wrong quietly.
func TestTheContractTruncatesRatherThanRounds(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2027-10-12T00:00:00.123999Z", "2027-10-12T00:00:00.123Z"},
		{"2027-10-12T00:00:00.999999Z", "2027-10-12T00:00:00.999Z"},
		{"2027-10-12T00:00:00.000999Z", "2027-10-12T00:00:00.000Z"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			var w logbook.TripWrite
			if err := json.Unmarshal([]byte(`{"id":"a","start":"`+tc.in+`"}`), &w); err != nil {
				t.Fatalf("decoding %s: %v", tc.in, err)
			}
			raw, err := logbook.Value(w.Start).MarshalJSON()
			if err != nil {
				t.Fatalf("re-emitting: %v", err)
			}
			if got := strings.Trim(string(raw), `"`); got != tc.want {
				t.Errorf("%s re-emitted as %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A zone offset is accepted and normalised.
func TestAnOffsetIsAcceptedAndComesBackInUTC(t *testing.T) {
	var w logbook.TripWrite
	if err := json.Unmarshal([]byte(`{"id":"a","start":"2027-10-12T09:00:00.000+09:00"}`), &w); err != nil {
		t.Fatalf("an offset was refused: %v — it is a complete instant, unlike a "+
			"zoneless string, and refusing it would be refusing something "+
			"unambiguous", err)
	}
	raw, err := logbook.Value(w.Start).MarshalJSON()
	if err != nil {
		t.Fatalf("re-emitting: %v", err)
	}
	if got := strings.Trim(string(raw), `"`); got != "2027-10-12T00:00:00.000Z" {
		t.Errorf("re-emitted as %q, want it converted to UTC with the trailing Z", got)
	}
}

// `expiresAt` is outside's six fields and must not be "fixed" into an
// Instant.
func TestTheSessionExpiryIsNotAnInstantAndKeepsGoesOwnRendering(t *testing.T) {
	type issued struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}
	at := time.Date(2027, time.October, 12, 0, 0, 0, 123456789, time.UTC)
	raw, err := json.Marshal(issued{ExpiresAt: at})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(raw), "00:00:00.123456789Z") {
		t.Errorf("expiresAt = %s, want Go's own nine digits — this field is not one "+
			"of DEC-68's six and is not read through the client's date codec, so "+
			"'fixing' it into an Instant would change a contract nothing asked "+
			"about", raw)
	}
	logged, err := logbook.At(at).MarshalJSON()
	if err != nil {
		t.Fatalf("marshalling through Instant: %v", err)
	}
	if got := strings.Trim(string(logged), `"`); got != "2027-10-12T00:00:00.123Z" {
		t.Errorf("through Instant = %q, want three digits — if these two ever agree, "+
			"one of them has been changed to match the other", got)
	}
}
