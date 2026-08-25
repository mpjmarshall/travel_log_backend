// Instant's two ends, against the two inputs no fixture in this repository
// contains (DEC-90).
//
// WHY A SEPARATE FILE FROM emit_test.go. Every leg there is measured against
// `testdata/client_sample_log.json`, and that document was built with
// `DateTime.utc(...)`: every date string in it carries a Z and none carries
// more than three fractional digits. So DEC-68's "byte-identical golden round
// trip per date-bearing field" is measured against exactly the data that
// cannot expose either of the two shapes below. A leg needing an input the
// golden document cannot supply is a leg that has to synthesise one, and
// synthesised inputs are worth keeping apart from measured ones.
package logbook_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

// THE SERVER KEEPS REFUSING A ZONELESS DATE, AND THE CLIENT IS THE ONE THAT
// CHANGES (DEC-90).
//
// MEASURED LIVE by the client-fidelity lens and re-measured against a
// container built at e4a3b94: `PUT /v1/trips/autumn` with
// `"start":"2027-10-12T00:00:00.000"` -> `400 invalid_body`; the identical
// body with `...000Z` -> 200. The client's ONLY date input is `pickLogDate`
// (lib/src/ui/app_fields.dart:23), which is `showDatePicker` returning
// `DateTime(y,m,d)` in the DEVICE'S LOCAL ZONE. T4's `_addDates`
// (lib/src/screens/trip_screen.dart:89) hands it to `setTripDates`, which is a
// pure pass-through, and `trip.g.dart:27` writes `toIso8601String()`, which
// emits NO ZONE SUFFIX for a non-UTC value. Verified on `wipe/mock-data`:
// `grep -rn 'toUtc' lib/src/` answers two lines, both `DateTime.now().toUtc()`
// in the clock, and neither on a user-picked date. So T4's 'Add dates' fails
// on EVERY request.
//
// THE FIX IS THE CLIENT'S `.toUtc()`, and it is a CLIENT-PREREQUISITES item
// with a LOCAL consequence said out loud: it changes what `logbook.json`
// holds, so it is a local-format change as well as a wire one. The server does
// NOT guess, because guessing at a string's meaning is the class of thing
// `logbookFormatVersion` exists to refuse — a date that means one thing on the
// phone and another in the log is worse than a date the write refused.
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

// AND THE PRECISION HALF, which the fixture also cannot supply.
//
// `instantLayout` is `.000` and Go TRUNCATES; Dart writes SIX digits whenever
// `microsecond != 0`, which `logbookNow()` always has on the VM — so
// `Photo.filedLater` and a minted `Visit.at` can both carry six. Millisecond
// precision on the wire is the CONTRACT and the client truncates before
// sending, so the round trip IS byte-identical; what this leg pins is that a
// six-digit fraction DECODES rather than being refused alongside the zoneless
// string above, and that what comes back out is the truncation and not a
// rounding.
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

// AND THE ROUNDING HALF, which is the one a `.000` layout gets wrong quietly.
// Go's Format truncates rather than rounding, so a fraction that would round
// UP still emits the lower millisecond. Pinned because "truncates" is a claim
// about the layout and not about the number, and .999999 is where the two
// answers differ by a whole second.
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

// A ZONE OFFSET IS ACCEPTED AND NORMALISED, which is the third shape and the
// one that distinguishes "the server refuses what it cannot interpret" from
// "the server only takes Z". RFC 3339 says `+09:00` is a complete instant, so
// refusing it would be refusing something unambiguous — and the emitter's own
// job is to render it back in UTC.
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

// `expiresAt` IS OUTSIDE DEC-68's SIX FIELDS AND MUST NOT BE "FIXED" INTO AN
// Instant. It is `time.Time` on the auth response and marshals as RFC 3339
// with Go's own variable precision — nine digits when the value has them —
// and that is CORRECT, because it is not part of the log document and no
// client codec reads it through the six-field contract. The leg exists so that
// somebody sweeping for "inconsistent date rendering" finds an argument here
// rather than making the change and discovering nothing red.
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
	// The control: the same instant THROUGH Instant is the log's contract, and
	// the two are deliberately different.
	logged, err := logbook.At(at).MarshalJSON()
	if err != nil {
		t.Fatalf("marshalling through Instant: %v", err)
	}
	if got := strings.Trim(string(logged), `"`); got != "2027-10-12T00:00:00.123Z" {
		t.Errorf("through Instant = %q, want three digits — if these two ever agree, "+
			"one of them has been changed to match the other", got)
	}
}
