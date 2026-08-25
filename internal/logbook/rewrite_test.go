// PD-03's RewriteAssets, and the decoder `make seed` reads the fixture with.
//
// THE FUNCTION IS NAMED RATHER THAN INLINE FOR ONE MEASURED REASON. The
// client's 284 photographs carry exactly TWO distinct locators — 189 of one
// and 95 of the other (DEC-75) — so a rewrite that points one locator at the
// other's digest changes 189 rows and leaves every count, every foreign key
// and every referential-integrity check green. The only thing that can see it
// is a diff of the emitted document against the client's own, which is what
// internal/seed's round trip does. A named function is what gives that
// mutation somewhere to be applied.
package logbook_test

import (
	"os"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// The two locators the client's own document actually holds, and two digests
// that are 64 lowercase hex characters because that is what
// media_objects_id_sha256_ck constrains an id to.
const (
	cardIreland  = "assets/imagery/card-ireland.png"
	heroMountain = "assets/imagery/hero-mountain.png"

	cardDigest = "8dfb203bc0f890655a7545004866da13482af78d21b5c6deb7bd142592a5d3cd"
	heroDigest = "e66b552e6043510bb5cd474096d18208b1c975556ef1a8cfc565dd63a02835c1"
)

func fixtureMapping() map[string]string {
	return map[string]string{cardIreland: cardDigest, heroMountain: heroDigest}
}

// FOUR COLUMNS, AND THE COUNT IS THE POINT (DEC-46). DEC-40's format bump
// describes four fields whose MEANING changed, and a rewrite that reaches
// three of them leaves a bundle path in a column the schema constrains to
// 64 hex characters — which fails at the INSERT, but only for the columns
// that have the CHECK, and cities.cover_asset is one of the ones that does.
func TestRewriteAssetsReachesAllFourColumns(t *testing.T) {
	doc := logbook.Document{
		Photos: []logbook.Photo{{ID: "p1", Asset: cardIreland}},
		Trips:  []logbook.Trip{{ID: "t1", CoverAsset: ptr(heroMountain)}},
		Cities: []logbook.City{{ID: "c1", CoverAsset: ptr(cardIreland)}},
		Places: []logbook.Place{{ID: "pl1", CoverAsset: ptr(heroMountain)}},
	}

	out, err := logbook.RewriteAssets(doc, fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}

	if out.Photos[0].Asset != cardDigest {
		t.Errorf("photos[0].asset = %q, want %q", out.Photos[0].Asset, cardDigest)
	}
	if got := *out.Trips[0].CoverAsset; got != heroDigest {
		t.Errorf("trips[0].coverAsset = %q, want %q", got, heroDigest)
	}
	if got := *out.Cities[0].CoverAsset; got != cardDigest {
		t.Errorf("cities[0].coverAsset = %q, want %q", got, cardDigest)
	}
	if got := *out.Places[0].CoverAsset; got != heroDigest {
		t.Errorf("places[0].coverAsset = %q, want %q", got, heroDigest)
	}
}

// A NULL COVER IS NOT AN UNMAPPED ONE. Seven of the client's own rows have no
// cover at all — one trip, three cities, eight places — so a rewrite that
// treated absence as a miss would refuse the very document it exists for.
func TestRewriteAssetsLeavesAnAbsentCoverAbsent(t *testing.T) {
	doc := logbook.Document{
		Trips:  []logbook.Trip{{ID: "t1"}},
		Cities: []logbook.City{{ID: "c1"}},
		Places: []logbook.Place{{ID: "pl1"}},
	}

	out, err := logbook.RewriteAssets(doc, fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}
	if out.Trips[0].CoverAsset != nil || out.Cities[0].CoverAsset != nil || out.Places[0].CoverAsset != nil {
		t.Errorf("an absent cover came back present: %v %v %v",
			out.Trips[0].CoverAsset, out.Cities[0].CoverAsset, out.Places[0].CoverAsset)
	}
}

// IT REFUSES AN UNMAPPED LOCATOR AND NAMES IT. The alternative — passing an
// unknown locator through — writes a bundle path into a column whose CHECK is
// `^[0-9a-f]{64}$`, and the failure arrives as SQLSTATE 23514 naming a
// constraint rather than naming the file nobody uploaded.
func TestRewriteAssetsRefusesAnUnmappedLocatorAndNamesIt(t *testing.T) {
	for _, tc := range []struct {
		what string
		doc  logbook.Document
		want string
	}{
		{"a photograph", logbook.Document{
			Photos: []logbook.Photo{{ID: "p9", Asset: "assets/imagery/nobody.png"}}},
			"photos[0].asset"},
		{"a trip cover", logbook.Document{
			Trips: []logbook.Trip{{ID: "t9", CoverAsset: ptr("assets/imagery/nobody.png")}}},
			"trips[0].coverAsset"},
		{"a city cover", logbook.Document{
			Cities: []logbook.City{{ID: "c9", CoverAsset: ptr("assets/imagery/nobody.png")}}},
			"cities[0].coverAsset"},
		{"a place cover", logbook.Document{
			Places: []logbook.Place{{ID: "pl9", CoverAsset: ptr("assets/imagery/nobody.png")}}},
			"places[0].coverAsset"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			_, err := logbook.RewriteAssets(tc.doc, fixtureMapping())
			if err == nil {
				t.Fatalf("RewriteAssets accepted an unmapped locator in %s", tc.what)
			}
			if !strings.Contains(err.Error(), "assets/imagery/nobody.png") {
				t.Errorf("the refusal does not name the locator: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say where it was: %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// THE INPUT IS NOT MUTATED, AND THIS IS THE LEG THAT COSTS SOMETHING TO GET
// WRONG. A Document is six slice headers; assigning one copies the headers and
// shares every backing array, so the obvious implementation rewrites the
// CALLER's document in place. The round trip in internal/seed compares the
// client's own decoded document against what came back out of PostgreSQL — and
// an in-place rewrite makes both sides the same memory, so the strongest leg
// this project can write would pass against anything at all.
func TestRewriteAssetsDoesNotTouchTheDocumentItWasGiven(t *testing.T) {
	doc := logbook.Document{
		Photos: []logbook.Photo{{ID: "p1", Asset: cardIreland}},
		Cities: []logbook.City{{ID: "c1", CoverAsset: ptr(cardIreland)}},
	}
	cover := doc.Cities[0].CoverAsset

	if _, err := logbook.RewriteAssets(doc, fixtureMapping()); err != nil {
		t.Fatalf("RewriteAssets: %v", err)
	}

	if doc.Photos[0].Asset != cardIreland {
		t.Errorf("the caller's photograph was rewritten in place: %q", doc.Photos[0].Asset)
	}
	if *cover != cardIreland {
		t.Errorf("the caller's city cover was rewritten through its pointer: %q", *cover)
	}
}

// THE CLIENT'S OWN DOCUMENT, WHICH IS THE ONLY INPUT THIS FUNCTION HAS. Every
// one of the four columns is exercised by it, and the assertion is that
// NOTHING is left holding a bundle path afterwards.
func TestRewriteAssetsLeavesNoBundlePathInTheClientsOwnLog(t *testing.T) {
	out, err := logbook.RewriteAssets(clientDocument(t), fixtureMapping())
	if err != nil {
		t.Fatalf("RewriteAssets on the client's own log: %v", err)
	}

	seen := map[string]int{}
	note := func(v string) {
		if strings.Contains(v, "/") || !isHex64(v) {
			t.Errorf("a locator survived the rewrite: %q", v)
		}
		seen[v]++
	}
	for _, p := range out.Photos {
		note(p.Asset)
	}
	for _, tr := range out.Trips {
		if tr.CoverAsset != nil {
			note(*tr.CoverAsset)
		}
	}
	for _, c := range out.Cities {
		if c.CoverAsset != nil {
			note(*c.CoverAsset)
		}
	}
	for _, p := range out.Places {
		if p.CoverAsset != nil {
			note(*p.CoverAsset)
		}
	}

	// TWO DIGESTS ACROSS THE WHOLE LIBRARY, and that is DEC-38's whole
	// argument rather than a curiosity: content addressing collapses 284
	// photographs and 19 covers onto two objects.
	if len(seen) != 2 {
		t.Errorf("distinct object ids = %d (%v), want 2", len(seen), seen)
	}
	if seen[cardDigest] != 189+3+7+6 {
		t.Errorf("rows addressing %s = %d, want %d", cardDigest, seen[cardDigest], 189+3+7+6)
	}
	if seen[heroDigest] != 95+3+2+3 {
		t.Errorf("rows addressing %s = %d, want %d", heroDigest, seen[heroDigest], 95+3+2+3)
	}
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// DecodeEnvelope is what `make seed` reads the captured fixture with, and it
// is the counterpart to Emit rather than a general-purpose decoder.
func TestDecodeEnvelopeReadsTheClientsOwnFile(t *testing.T) {
	raw, err := readFixture()
	if err != nil {
		t.Fatalf("reading %s: %v", clientFixture, err)
	}

	envelope, err := logbook.DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}

	// The client's own file is format 1; the server emits 2. DecodeEnvelope
	// reports the version and does NOT judge it — the seed is loading a
	// captured document rather than serving a request, and DEC-40's refusal
	// belongs on the wire.
	if envelope.Version != 1 {
		t.Errorf("version = %d, want the client's 1", envelope.Version)
	}
	if n := len(envelope.Logbook.Photos); n != 284 {
		t.Errorf("photos = %d, want 284", n)
	}
	if envelope.Logbook.Traveller == nil || envelope.Logbook.Traveller.Name == "" {
		t.Errorf("traveller = %v, want the fixture's named one", envelope.Logbook.Traveller)
	}
}

func TestDecodeEnvelopeRefusesSomethingThatIsNotAnEnvelope(t *testing.T) {
	for _, tc := range []struct{ what, raw string }{
		{"empty", ``},
		{"not JSON", `{`},
		{"two values", `{"version":1,"logbook":{}}{"version":1,"logbook":{}}`},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if _, err := logbook.DecodeEnvelope([]byte(tc.raw)); err == nil {
				t.Errorf("DecodeEnvelope accepted %s", tc.what)
			}
		})
	}
}

func readFixture() ([]byte, error) { return os.ReadFile(clientFixture) }
