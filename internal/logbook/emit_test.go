// The emitter, against the client's own 85 KB document.
//
// THE STRONGEST LEG IN THIS FILE IS THE ROUND TRIP, and it is strong because
// the reference is not a fixture written beside the code that has to satisfy
// it. `testdata/client_sample_log.json` is what the Flutter app's own encoder
// produced before its fixture was deleted (DEC-75, sha256 03e88872…): seven
// trips, twelve cities, seventeen places, 284 photographs, two walks, and the
// edge cases the schema was designed around. Decoding it into these types and
// emitting it back asserts every key, every date string and every number at
// once, against a document neither this package nor its tests wrote.
package logbook_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"travellog/internal/logbook"
)

const clientFixture = "testdata/client_sample_log.json"

// clientLogbook is the `logbook` object out of the client's envelope, as raw
// JSON. The envelope's own `version` is the client's 1 and is deliberately not
// carried in: DEC-40 moves the server to 2, and the leg about the version is
// separate from the leg about the shape.
func clientLogbook(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(clientFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", clientFixture, err)
	}
	var envelope struct {
		Version int             `json:"version"`
		Logbook json.RawMessage `json:"logbook"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decoding %s: %v", clientFixture, err)
	}
	return envelope.Logbook
}

func clientDocument(t *testing.T) logbook.Document {
	t.Helper()
	var doc logbook.Document
	if err := json.Unmarshal(clientLogbook(t), &doc); err != nil {
		t.Fatalf("decoding the client's log into these types: %v", err)
	}
	return doc
}

func emitted(t *testing.T, doc logbook.Document) []byte {
	t.Helper()
	envelope, err := logbook.Emit(logbook.FormatVersion, doc)
	if err != nil {
		t.Fatalf("Emit(%d): %v", logbook.FormatVersion, err)
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshalling the envelope: %v", err)
	}
	return out
}

// THE ROUND TRIP. Every date string, every coordinate, every null and every
// key, in one assertion against a document this repository did not author.
func TestTheClientsOwnLogRoundTripsThroughTheseTypes(t *testing.T) {
	out := emitted(t, clientDocument(t))

	var got struct {
		Logbook map[string]any `json:"logbook"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-decoding what Emit produced: %v", err)
	}

	var want map[string]any
	if err := json.Unmarshal(clientLogbook(t), &want); err != nil {
		t.Fatalf("re-decoding the client's log: %v", err)
	}

	if reflect.DeepEqual(got.Logbook, want) {
		return
	}
	for _, diff := range firstDifferences(t, got.Logbook, want, 6) {
		t.Errorf("%s", diff)
	}
}

// DEC-68 asks for one golden round-trip leg PER DATE-BEARING FIELD, asserting
// the emitted string is byte-identical to what the client sent. There are six,
// and three of them are `date` columns in storage that must come back out as
// midnight UTC with milliseconds.
func TestEveryDateBearingFieldIsByteIdenticalToWhatTheClientSent(t *testing.T) {
	got := walkStrings(t, emitted(t, clientDocument(t)))
	want := walkStrings(t, clientLogbook(t))

	for _, field := range []struct{ name, path string }{
		{"trips[].start — a date column", "logbook.trips[].start"},
		{"trips[].end — a date column", "logbook.trips[].end"},
		{"walks[].recordedOn — a date column", "logbook.walks[].recordedOn"},
		{"places[].visits[].at — timestamptz", "logbook.places[].visits[].at"},
		{"photos[].takenAt — timestamptz", "logbook.photos[].takenAt"},
	} {
		t.Run(field.name, func(t *testing.T) {
			mine := got[field.path]
			theirs := want[strings.TrimPrefix(field.path, "logbook.")]
			if len(theirs) == 0 {
				t.Fatalf("the client's log carries no %s to compare against", field.path)
			}
			if !reflect.DeepEqual(mine, theirs) {
				t.Errorf("emitted %d value(s), the client sent %d\n  first emitted: %v\n  first sent:    %v",
					len(mine), len(theirs), firstOf(mine), firstOf(theirs))
			}
		})
	}
}

// THE SIXTH DATE-BEARING FIELD HAS NO FIXTURE, AND THAT IS A MEASUREMENT
// RATHER THAN AN OMISSION: all 284 photographs in the client's log carry
// `"filedLater": null`, so the leg above has nothing to compare it against and
// says so rather than passing vacuously. It is the one field that needs a
// synthesised value, and this is that leg — written against the string a Dart
// `DateTime.toIso8601String()` produces, not against what Go happens to do.
//
// The two inputs are chosen for what they break: a source time in a NON-UTC
// zone (Go renders the offset unless it is converted first) and a whole second
// with no fractional part (Go's RFC 3339 marshaller trims `.000` away).
func TestASynthesisedInstantCarriesMillisecondsAndZ(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)

	for _, tc := range []struct {
		name string
		in   time.Time
		want string
	}{
		{"a whole second in UTC", time.Date(2027, 10, 2, 10, 15, 0, 0, time.UTC), "2027-10-02T10:15:00.000Z"},
		{"midnight, which is what a date column scans as", time.Date(2027, 9, 17, 0, 0, 0, 0, time.UTC), "2027-09-17T00:00:00.000Z"},
		{"a non-UTC zone", time.Date(2027, 10, 2, 19, 15, 0, 0, tokyo), "2027-10-02T10:15:00.000Z"},
		{"real milliseconds", time.Date(2027, 10, 2, 10, 15, 0, 123000000, time.UTC), "2027-10-02T10:15:00.123Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filed := logbook.At(tc.in)
			doc := logbook.Document{Photos: []logbook.Photo{{
				ID: "ph-0", TripID: "t", CityID: "c", Asset: "a",
				TakenAt: filed, FiledLater: &filed,
			}}}
			for path, values := range walkStrings(t, emitted(t, doc)) {
				if path != "logbook.photos[].filedLater" && path != "logbook.photos[].takenAt" {
					continue
				}
				if len(values) != 1 || values[0] != tc.want {
					t.Errorf("%s = %v, want [%q]", path, values, tc.want)
				}
			}
		})
	}
}

// L2 FOUND THIS GAP AND IT IS WORTH THE EXTRA LEG. `At` converts to UTC, so
// every leg that builds an Instant through it proves At's conversion and never
// the marshaller's — deleting `.UTC()` from MarshalJSON left the whole package
// green. A store scanning a `timestamptz` gets whatever zone the pgx session is
// in and may reach for the bare conversion, which is the caller `At` does not
// protect.
func TestAnInstantBuiltByConversionIsStillRenderedInUTC(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*60*60)
	raw := logbook.Instant(time.Date(2027, 10, 2, 19, 15, 0, 0, tokyo))

	doc := logbook.Document{Photos: []logbook.Photo{{
		ID: "ph-0", TripID: "t", CityID: "c", Asset: "a", TakenAt: raw,
	}}}

	got := walkStrings(t, emitted(t, doc))["logbook.photos[].takenAt"]
	if want := []string{"2027-10-02T10:15:00.000Z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("takenAt = %v, want %v — a non-UTC instant reached the wire with "+
			"the wall-clock time of another zone under a Z", got, want)
	}
}

// A nil slice marshals to `null`, which is neither an absent key nor an empty
// list — and `as List<dynamic>` throws on it. This is the leg behind "the four
// unimplemented lists are EMPTY rather than ABSENT".
func TestEveryListIsEmptyRatherThanNull(t *testing.T) {
	out := emitted(t, logbook.Document{})

	var got struct {
		Logbook map[string]json.RawMessage `json:"logbook"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-decoding: %v", err)
	}

	for _, key := range []string{"trips", "cities", "places", "photos", "walks"} {
		raw, held := got.Logbook[key]
		if !held {
			t.Errorf("%q is ABSENT from an empty log; the shape is final and every key is always there", key)
			continue
		}
		if string(raw) != "[]" {
			t.Errorf("%q = %s, want []", key, raw)
		}
	}
}

func TestAnUnnamedTravellerIsNullAndNeverAnEmptyObject(t *testing.T) {
	var got struct {
		Logbook map[string]json.RawMessage `json:"logbook"`
	}
	if err := json.Unmarshal(emitted(t, logbook.Document{}), &got); err != nil {
		t.Fatalf("re-decoding: %v", err)
	}

	raw, held := got.Logbook["traveller"]
	if !held {
		t.Fatalf("`traveller` is absent; the client null-checks the key and cannot see a missing one")
	}
	if string(raw) != "null" {
		t.Errorf("traveller = %s, want null — the client casts json['name'] as String, "+
			"non-nullable, so {} throws", raw)
	}
}

func TestTheEnvelopeCarriesTheFormatVersionItWasGiven(t *testing.T) {
	var got struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(emitted(t, logbook.Document{}), &got); err != nil {
		t.Fatalf("re-decoding: %v", err)
	}
	if got.Version != logbook.FormatVersion {
		t.Errorf("version = %d, want %d (DEC-40)", got.Version, logbook.FormatVersion)
	}
	if logbook.FormatVersion != 2 {
		t.Errorf("FormatVersion = %d, want 2 — DEC-40 moves the wire to 2 in the same phase "+
			"the client moves, and a server emitting anything else is DEC-40's refetch loop",
			logbook.FormatVersion)
	}
}

// The parameter exists so a SECOND version is possible; today there is one
// value, and a version the emitter cannot write is refused rather than
// silently emitted as the one it can.
func TestEmitRefusesAFormatVersionItCannotWrite(t *testing.T) {
	for _, v := range []int{0, 1, 3, -1} {
		if _, err := logbook.Emit(v, logbook.Document{}); err == nil {
			t.Errorf("Emit(%d) = nil error; an unknown format version must be refused, "+
				"or DEC-53's 406 answers a request the emitter has already mis-served", v)
		}
	}
}

func TestFormatsNamesWhatTheEmitterCanWrite(t *testing.T) {
	got := logbook.Formats()
	if want := []int{logbook.FormatVersion}; !reflect.DeepEqual(got, want) {
		t.Errorf("Formats() = %v, want %v — this is what the 406 response names", got, want)
	}
}

// DEC-49's first half, asserted on the constant so it cannot be dropped.
func TestTheEmitterVersionStartsAtOne(t *testing.T) {
	if logbook.EmitterVersion != 1 {
		t.Errorf("EmitterVersion = %d, want 1 — the shape is final at VS7, so the "+
			"re-plan inherits it without a bump", logbook.EmitterVersion)
	}
}

// The golden key file. It is the shape as a checked-in artefact, so a renamed
// or added key reddens even for a field the client fixture happens not to
// exercise — and the leg below asserts the golden IS the client's key set, so
// a golden regenerated to match a mistake reddens too.
func TestTheKeySetAtEveryLevelEqualsTheGolden(t *testing.T) {
	got := keyPaths(t, emitted(t, clientDocument(t)))

	const golden = "testdata/logbook_keys.golden"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
	}
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate with UPDATE_GOLDEN=1)", golden, err)
	}
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the emitted key set differs from %s:\n  added:   %v\n  missing: %v",
			filepath.Base(golden), missing(got, want), missing(want, got))
	}
}

// THE ONE THAT MAKES THE GOLDEN EVIDENCE RATHER THAN A RECORDING. A golden
// regenerated from a broken emitter agrees with itself; this leg asks the
// client's own document what the keys are.
func TestTheGoldenKeySetIsTheClientFixturesKeySet(t *testing.T) {
	raw, err := os.ReadFile("testdata/logbook_keys.golden")
	if err != nil {
		t.Fatalf("reading the golden: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")

	raw, err = os.ReadFile(clientFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", clientFixture, err)
	}
	fixture := keyPaths(t, raw)
	if !reflect.DeepEqual(got, fixture) {
		t.Errorf("the golden and the client's own log disagree:\n  only in the golden:  %v\n  only in the client's: %v",
			missing(got, fixture), missing(fixture, got))
	}
}

// DEC-49's cache dies the moment a value in the body changes on every request,
// and a presigned URL is exactly such a value. THE ASSERTION IS STRUCTURAL —
// the key set above is the guard — and this is the direct one beside it,
// deterministic in the slice because nothing here mints a URL.
func TestNoValueInTheEmittedDocumentIsAURL(t *testing.T) {
	urlish := regexp.MustCompile(`^https?://`)
	for path, values := range walkStrings(t, emitted(t, clientDocument(t))) {
		for _, v := range values {
			if urlish.MatchString(v) {
				t.Errorf("%s carries %q — a presigned URL changes on every mint, so the "+
					"body differs every request and 304 never fires", path, v)
			}
		}
	}
}

// Two consecutive emissions of one document are byte-identical. Map iteration
// order is the classic way this fails, and it fails intermittently.
func TestTwoEmissionsOfOneDocumentAreByteIdentical(t *testing.T) {
	doc := clientDocument(t)
	first := emitted(t, doc)
	for i := range 8 {
		if again := emitted(t, doc); string(again) != string(first) {
			t.Fatalf("emission %d differs from the first", i+2)
		}
	}
}

// keyPaths is every key in the document, as a sorted set of paths with list
// indices collapsed to `[]`.
func keyPaths(t *testing.T, raw []byte) []string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding for the key walk: %v", err)
	}
	seen := map[string]bool{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch value := v.(type) {
		case map[string]any:
			for k, child := range value {
				path := k
				if prefix != "" {
					path = prefix + "." + k
				}
				seen[path] = true
				walk(path, child)
			}
		case []any:
			for _, child := range value {
				walk(prefix+"[]", child)
			}
		}
	}
	walk("", decoded)

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// walkStrings collects every string value in the document, by key path.
func walkStrings(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding for the string walk: %v", err)
	}
	out := map[string][]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch value := v.(type) {
		case map[string]any:
			for k, child := range value {
				path := k
				if prefix != "" {
					path = prefix + "." + k
				}
				walk(path, child)
			}
		case []any:
			for _, child := range value {
				walk(prefix+"[]", child)
			}
		case string:
			out[prefix] = append(out[prefix], value)
		}
	}
	walk("", decoded)
	return out
}

func missing(from, in []string) []string {
	held := map[string]bool{}
	for _, s := range in {
		held[s] = true
	}
	var out []string
	for _, s := range from {
		if !held[s] {
			out = append(out, s)
		}
	}
	return out
}

// brief keeps a failure readable: the round trip's reference is 85 KB, and a
// difference reported by printing both documents is a difference nobody reads.
func brief(v any) string {
	s := fmt.Sprintf("%#v", v)
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return "<none>"
	}
	return values[0]
}

// firstDifferences reports where two decoded documents disagree, so the round
// trip's failure names a field rather than printing 85 KB twice.
func firstDifferences(t *testing.T, got, want map[string]any, limit int) []string {
	t.Helper()
	var out []string
	var walk func(path string, a, b any)
	walk = func(path string, a, b any) {
		if len(out) >= limit {
			return
		}
		switch left := a.(type) {
		case map[string]any:
			right, ok := b.(map[string]any)
			if !ok {
				out = append(out, fmt.Sprintf("%s: emitted an object, the client sent %T", path, b))
				return
			}
			for k := range left {
				if _, held := right[k]; !held {
					out = append(out, fmt.Sprintf("%s.%s: emitted but not in the client's log", path, k))
				}
			}
			for k, rv := range right {
				lv, held := left[k]
				if !held {
					out = append(out, fmt.Sprintf("%s.%s: in the client's log and not emitted", path, k))
					continue
				}
				walk(path+"."+k, lv, rv)
			}
		case []any:
			right, ok := b.([]any)
			if !ok {
				out = append(out, fmt.Sprintf("%s: emitted a list, the client sent %T", path, b))
				return
			}
			if len(left) != len(right) {
				out = append(out, fmt.Sprintf("%s: emitted %d items, the client sent %d", path, len(left), len(right)))
				return
			}
			for i := range left {
				walk(fmt.Sprintf("%s[%d]", path, i), left[i], right[i])
			}
		default:
			if !reflect.DeepEqual(a, b) {
				out = append(out, fmt.Sprintf("%s: emitted %s, the client sent %s", path, brief(a), brief(b)))
			}
		}
	}
	walk("logbook", any(got), any(want))
	return out
}
