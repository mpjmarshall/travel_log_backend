// The RewriteAssets, and the decoder `make seed` reads the fixture with.
package logbook_test

import (
	"os"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// The two locators the client's own document actually holds.
const (
	cardIreland  = "assets/imagery/card-ireland.png"
	heroMountain = "assets/imagery/hero-mountain.png"

	cardDigest = "8dfb203bc0f890655a7545004866da13482af78d21b5c6deb7bd142592a5d3cd"
	heroDigest = "e66b552e6043510bb5cd474096d18208b1c975556ef1a8cfc565dd63a02835c1"
)

func fixtureMapping() map[string]string {
	return map[string]string{cardIreland: cardDigest, heroMountain: heroDigest}
}

// four columns, and the count is the point.
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

// A null cover is not an unmapped one.
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

// it refuses an unmapped locator and names it.
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

// the input is not mutated, and this is the leg that costs something to get
// wrong.
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

// the client's own document, which is the only input this function has.
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
