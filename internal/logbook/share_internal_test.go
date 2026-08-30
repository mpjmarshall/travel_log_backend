// The two compiled patterns, and the difference between them.
package logbook

import "testing"

// the expression is asserted as A STRING, in one falsifiable place, because
// the two bounds are interpolated into it.
func TestTheShareTokenExpressionIsWhatTheBoundsSay(t *testing.T) {
	const want = `^[a-z0-9]{12,64}$`
	if got := shareTokenPattern.String(); got != want {
		t.Errorf("shareTokenPattern = %q, want %q — the bounds are %d and %d",
			got, want, MinShareTokenBytes, MaxShareTokenBytes)
	}
}

// the hyphen case is the one worth saying out loud.
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
