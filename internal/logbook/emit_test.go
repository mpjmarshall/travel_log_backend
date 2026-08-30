// The emitter, against the client's own 85 KB document.
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

// clientLogbook is the `logbook` object out of the client the envelope, as raw
// JSON.
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

// serverAddedKeys is every key the server emits that the client's own
// document does not have, named once and read by's three legs below.
var serverAddedKeys = []string{"logbook.trips[].shared"}

// the round trip.
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

	stripServerAdded(t, got.Logbook)

	if reflect.DeepEqual(got.Logbook, want) {
		return
	}
	for _, diff := range firstDifferences(t, got.Logbook, want, 6) {
		t.Errorf("%s", diff)
	}
}

// asks for one golden round-trip leg per date-bearing field, asserting the
// emitted string is byte-identical to what the client sent.
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

// the sixth date-bearing field has no fixture, and that is a measurement
// Than an omission.
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

// L2 found this gap and it is worth the extra leg.
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
// list — and `as List<dynamic>` throws on it.
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

// The nested lists, and the one route that answers a bare entity.
func TestATripsCityIdsAreEmptyRatherThanNull(t *testing.T) {
	inside := emitted(t, logbook.Document{Trips: []logbook.Trip{{ID: "kyoto", Name: "Kyoto"}}})
	if !strings.Contains(string(inside), `"cityIds":[]`) {
		t.Errorf("inside the document: %s, want `\"cityIds\":[]`", inside)
	}

	bare, err := json.Marshal(logbook.EmitTrip(logbook.Trip{ID: "kyoto", Name: "Kyoto"}))
	if err != nil {
		t.Fatalf("marshalling a bare trip: %v", err)
	}
	if !strings.Contains(string(bare), `"cityIds":[]`) {
		t.Errorf("the write's answer: %s, want `\"cityIds\":[]`", bare)
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
// value.
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

// The first half, asserted on the constant so it cannot be dropped.
func TestTheEmitterVersionMovedWhenTheShapeDid(t *testing.T) {
	want := int64(1 + len(serverAddedKeys))
	if logbook.EmitterVersion != want {
		t.Errorf("EmitterVersion = %d, want %d — VS7 froze the shape at emitter 1 and "+
			"serverAddedKeys names %d key(s) added since; a cached body under an "+
			"unmoved emitter version is a phone that keeps serving the old shape "+
			"until somebody happens to write",
			logbook.EmitterVersion, want, len(serverAddedKeys))
	}
}

// The golden key file.
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

// the one that makes the golden evidence rather than A RECORDING.
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

	if extra := missing(got, fixture); !reflect.DeepEqual(extra, serverAddedKeys) {
		t.Errorf("the golden holds %v beyond the client's own log, want exactly %v — "+
			"every server-added key needs a line in serverAddedKeys and a ruling "+
			"behind it", extra, serverAddedKeys)
	}
	if gone := missing(fixture, got); len(gone) != 0 {
		t.Errorf("the client's log holds %v and the server does not emit them", gone)
	}
}

// stripServerAdded removes exactly the keys in serverAddedKeys from a decoded
// document, so the round trip compares like with like.
func stripServerAdded(t *testing.T, doc map[string]any) {
	t.Helper()
	for _, path := range serverAddedKeys {
		parts := strings.Split(strings.TrimPrefix(path, "logbook."), "[].")
		if len(parts) != 2 {
			t.Fatalf("serverAddedKeys holds %q, which stripServerAdded cannot read — "+
				"it understands `logbook.<list>[].<key>` and nothing else", path)
		}
		list, ok := doc[parts[0]].([]any)
		if !ok {
			t.Fatalf("%q names %q, which is not a list in the emitted document", path, parts[0])
		}
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("%q holds something that is not an object", parts[0])
			}
			if _, held := entry[parts[1]]; !held {
				t.Fatalf("%q is in serverAddedKeys and the emitter does not write it", path)
			}
			delete(entry, parts[1])
		}
	}
}

// The cache dies the moment a value in the body changes on every request, and
// a presigned URL is exactly such a value.
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

// Two consecutive emissions of one document are byte-identical.
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

// brief keeps a failure readable.
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

// the size premise, measured through this build rather than carried.
func TestTheEmittedSizeIsLargerThanTheClientsFileAndSaysBySoMuch(t *testing.T) {
	clientFile, err := os.ReadFile(clientFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", clientFixture, err)
	}
	doc := clientDocument(t)

	asIs := len(emitted(t, doc))
	withObjectIDs := len(emitted(t, withContentAddresses(doc)))

	t.Logf("the client's file on disk:            %d bytes", len(clientFile))
	t.Logf("emitted through THIS build, as-is:    %d bytes", asIs)
	t.Logf("emitted with DEC-46 object ids:       %d bytes", withObjectIDs)

	if asIs <= len(clientFile) {
		t.Errorf("the emitted body is %d bytes against a %d-byte client file — "+
			"DEC-91's `shared` alone makes it bigger, so an argument resting on "+
			"the file size is understating the wire", asIs, len(clientFile))
	}
	if withObjectIDs <= asIs {
		t.Errorf("object ids made the body SMALLER (%d against %d) — a 64-hex id is "+
			"longer than every bundle path in the fixture, so this cannot happen "+
			"unless the substitution did nothing", withObjectIDs, asIs)
	}
}

// withContentAddresses is the fixture as it will be AFTER: every asset
// locator a 64-character hex object id.
func withContentAddresses(doc logbook.Document) logbook.Document {
	id := func(n int) string {
		return fmt.Sprintf("%064x", n)
	}
	n := 0
	next := func() string { n++; return id(n) }
	swap := func(p **string) {
		if *p != nil {
			v := next()
			*p = &v
		}
	}
	for i := range doc.Photos {
		doc.Photos[i].Asset = next()
	}
	for i := range doc.Trips {
		swap(&doc.Trips[i].CoverAsset)
	}
	for i := range doc.Cities {
		swap(&doc.Cities[i].CoverAsset)
	}
	for i := range doc.Places {
		swap(&doc.Places[i].CoverAsset)
	}
	return doc
}

// EmitPlace is's second half of the same rule, and the measurement is in
// the failure message.
func TestAPlacesVisitsAreEmptyRatherThanNull(t *testing.T) {
	wishlist := logbook.Place{ID: "tofuku-ji", CityID: "kyoto", Name: "Tofuku-ji"}
	if wishlist.Visits != nil {
		t.Fatalf("this leg's input already carries a list, so it proves nothing")
	}

	inside := emitted(t, logbook.Document{Places: []logbook.Place{wishlist}})
	if !strings.Contains(string(inside), `"visits":[]`) {
		t.Errorf("inside the document: %s, want `\"visits\":[]`", inside)
	}

	bare, err := json.Marshal(wishlist)
	if err != nil {
		t.Fatalf("marshalling a bare place: %v", err)
	}
	if !strings.Contains(string(bare), `"visits":null`) {
		t.Fatalf("a bare Place no longer marshals `\"visits\":null` (%s), so the rest of "+
			"this leg is about nothing — re-derive the reason EmitPlace exists before "+
			"deleting it", bare)
	}

	answered, err := json.Marshal(logbook.EmitPlace(wishlist))
	if err != nil {
		t.Fatalf("marshalling the write's answer: %v", err)
	}
	if !strings.Contains(string(answered), `"visits":[]`) {
		t.Errorf("the write's answer: %s, want `\"visits\":[]`", answered)
	}
}

// A walk's points are the same rule A third time, and this is the route the
// client throws on.
func TestAWalksPointsAreEmptyRatherThanNull(t *testing.T) {
	unread := logbook.Walk{ID: "w-busan", TripID: "autumn-crossing", CityID: "busan"}
	if unread.Points != nil {
		t.Fatalf("this leg's input already carries a list, so it proves nothing")
	}

	inside := emitted(t, logbook.Document{Walks: []logbook.Walk{unread}})
	if !strings.Contains(string(inside), `"points":[]`) {
		t.Errorf("inside the document: %s, want `\"points\":[]`", inside)
	}

	bare, err := json.Marshal(unread)
	if err != nil {
		t.Fatalf("marshalling a bare walk: %v", err)
	}
	if !strings.Contains(string(bare), `"points":null`) {
		t.Fatalf("a bare Walk no longer marshals `\"points\":null` (%s), so the rest of "+
			"this leg is about nothing — re-derive the reason EmitWalk exists before "+
			"deleting it", bare)
	}

	answered, err := json.Marshal(logbook.EmitWalk(unread))
	if err != nil {
		t.Fatalf("marshalling the write's answer: %v", err)
	}
	if !strings.Contains(string(answered), `"points":[]`) {
		t.Errorf("the write's answer: %s, want `\"points\":[]`", answered)
	}
}

// The three entities that need no EmitX are asserted to need none, so
// nobody adds three functions that are noise and nobody deletes the reason.
func TestACityATravellerAndAPhotoCarryNoListAndThereforeNeedNoEmitter(t *testing.T) {
	for _, entity := range []struct {
		name  string
		value any
	}{
		{"City", logbook.City{ID: "kyoto", Name: "Kyoto"}},
		{"Traveller", logbook.Traveller{Name: "Matt"}},
		{"Photo", logbook.Photo{ID: "ph-0", TripID: "autumn-crossing", CityID: "seoul"}},
	} {
		raw, err := json.Marshal(entity.value)
		if err != nil {
			t.Fatalf("marshalling a bare %s: %v", entity.name, err)
		}
		if strings.Contains(string(raw), "null") && strings.Contains(string(raw), "[") {
			t.Errorf("a bare %s = %s", entity.name, raw)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatalf("re-decoding a bare %s: %v", entity.name, err)
		}
		for key, value := range keys {
			if len(value) > 0 && value[0] == '[' {
				t.Errorf("%s.%s is a list, so this entity now needs an Emit%s and this "+
					"leg is what says so: a nil slice marshals to null, and every list "+
					"key in this document is read by the client as a non-nullable List",
					entity.name, key, entity.name)
			}
		}

		for i, kind := 0, reflect.TypeOf(entity.value); i < kind.NumField(); i++ {
			switch field := kind.Field(i); field.Type.Kind() {
			case reflect.Slice, reflect.Array:
				t.Errorf("%s.%s is a %s, so this entity now needs an Emit%s: a nil "+
					"slice marshals to `null`, and the client reads every list key "+
					"in this document as a non-nullable List",
					entity.name, field.Name, field.Type, entity.name)
			}
		}
	}
}
