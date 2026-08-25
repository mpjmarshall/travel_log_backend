// The two compiled patterns, and the difference between them (spec L23).
//
// IT IS package logbook FOR THE REASON validate_internal_test.go IS: the
// expression a reader has to be able to check is `shareTokenPattern`'s own
// String(), which nothing exports and nothing should. Its sibling file
// share_test.go is the external one and holds everything reachable through the
// package's own API.
package logbook

import "testing"

// THE EXPRESSION IS ASSERTED AS A STRING, in one falsifiable place, because the
// two bounds are interpolated into it. Without this the constants and the
// pattern are one derivation nothing checks, which is the shape
// `contentTypePattern` already has a leg for.
func TestTheShareTokenExpressionIsWhatTheBoundsSay(t *testing.T) {
	const want = `^[a-z0-9]{12,64}$`
	if got := shareTokenPattern.String(); got != want {
		t.Errorf("shareTokenPattern = %q, want %q — the bounds are %d and %d",
			got, want, MinShareTokenBytes, MaxShareTokenBytes)
	}
}

// THE HYPHEN CASE IS THE ONE WORTH SAYING OUT LOUD. `idPattern` accepts a
// hyphen because the client's ids are hand-written slugs — `autumn-crossing`,
// `fushimi-inari` — and must round-trip. A share token is not an id: it is
// minted by `_randomId()` from `abcdefghjkmnpqrstuvwxyz23456789`, which has no
// hyphen in it, and the fixture's captured `kyoto-9f2a` is a HAND-WRITTEN
// stand-in in a document nobody minted through this route. So this leg is what
// records that the two patterns are deliberately different, and that the
// fixture's own token would be refused if it arrived through POST — which it
// cannot, because migration 0004 hashes it on the way in and the mint route
// never sees it.
func TestTheShareTokenPatternIsNotTheIdPattern(t *testing.T) {
	const both = "kyotomay9f2a"
	if !idPattern.MatchString(both) || !shareTokenPattern.MatchString(both) {
		t.Fatalf("the premise failed: %q should satisfy both patterns", both)
	}
	const idOnly = "autumn-crossing"
	if !idPattern.MatchString(idOnly) {
		t.Errorf("idPattern refuses %q, which is a real trip id in the client's log", idOnly)
	}
	if shareTokenPattern.MatchString(idOnly) {
		t.Errorf("shareTokenPattern accepts %q. A token goes in a URL path and is drawn "+
			"from a hyphen-free alphabet; accepting an id's shape would let a caller "+
			"mint a capability that reads like a slug.", idOnly)
	}
}
