package httpx_test

import (
	"testing"

	"travellog/internal/httpx"
)

func TestFormatETagCarriesBothHalves(t *testing.T) {
	if got := httpx.FormatETag(2, 7); got != `W/"2-7"` {
		t.Errorf("FormatETag(2, 7) = %s, want W/\"2-7\"", got)
	}
}

// DEC-49(a), and it is the whole reason the emitter half exists. v3's ETag was
// `W/"<logbook_version>"`, which moves only on a WRITE — so a deploy that
// changed the emitted document (a field added, a date rendered differently,
// DEC-40's `"version": 2` itself) left every phone holding a cached body
// getting 304 forever and serving the OLD SHAPE until somebody happened to
// write. The validator did not cover the thing that produced the bytes.
func TestADeployThatMovesTheEmitterInvalidatesEveryCachedBody(t *testing.T) {
	before := httpx.FormatETag(1, 7)
	after := httpx.FormatETag(2, 7)

	if before == after {
		t.Fatalf("the emitter moved and the ETag did not: %s", after)
	}
	if httpx.ETagMatches(before, after) {
		t.Errorf("If-None-Match: %s matched %s — the phone would 304 onto the old shape",
			before, after)
	}
}

// "cannot be called with one half" (VS3's step text). A zero version is the
// shape a caller reaches by forgetting an argument or by reading a column that
// has not been set, and it is a programmer error rather than a client one —
// so it panics here, where the stack still names the caller, rather than
// emitting `W/"0-7"` and being discovered as a cache that never invalidates.
// The recover middleware turns it into a 500, not a dead process.
func TestFormatETagRefusesAMissingHalf(t *testing.T) {
	cases := []struct {
		name             string
		emitter, logbook int64
	}{
		{"no emitter", 0, 7},
		{"no logbook", 2, 0},
		{"neither", 0, 0},
		{"a negative logbook version", 2, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("FormatETag(%d, %d) returned instead of panicking", c.emitter, c.logbook)
				}
			}()
			_ = httpx.FormatETag(c.emitter, c.logbook)
		})
	}
}

func TestParseETagRoundTripsWhatFormatWrote(t *testing.T) {
	emitter, logbook, ok := httpx.ParseETag(httpx.FormatETag(2, 4471))
	if !ok {
		t.Fatal("ParseETag refused FormatETag's own output")
	}
	if emitter != 2 || logbook != 4471 {
		t.Errorf("ParseETag = (%d, %d), want (2, 4471)", emitter, logbook)
	}
}

// The strong form is accepted on the way IN and never emitted on the way out.
// A cache, a proxy or a hand-written curl echoes `"2-7"` without the W/, and
// refusing it would answer 200 to a client that is correctly revalidating.
func TestParseETagAcceptsTheStrongFormItNeverEmits(t *testing.T) {
	emitter, logbook, ok := httpx.ParseETag(`"2-7"`)
	if !ok || emitter != 2 || logbook != 7 {
		t.Errorf("ParseETag(\"2-7\") = (%d, %d, %v), want (2, 7, true)", emitter, logbook, ok)
	}
}

func TestParseETagRefusesEverythingThatIsNotBothHalves(t *testing.T) {
	for _, s := range []string{
		``,
		`W/"7"`,                      // the v3 ETag: one half, and the defect DEC-49 names
		`"7"`,                        //
		`W/"2-"`,                     //
		`W/"-7"`,                     //
		`W/"a-b"`,                    // not numbers
		`W/"2-7-9"`,                  // three halves is not two
		`2-7`,                        // unquoted
		`W/2-7`,                      //
		`W/"2-7`,                     // unbalanced
		`*`,                          // valid in If-None-Match, never a tag
		`W/"2-99999999999999999999"`, // overflows int64, so it is not a version this server ever wrote
	} {
		if _, _, ok := httpx.ParseETag(s); ok {
			t.Errorf("ParseETag(%q) accepted it", s)
		}
	}
}

func TestETagMatchesUsesWeakComparison(t *testing.T) {
	tag := httpx.FormatETag(2, 7)

	cases := []struct {
		name        string
		ifNoneMatch string
		want        bool
	}{
		{"the same weak tag", `W/"2-7"`, true},
		{"the strong spelling of it — weak comparison ignores the W/", `"2-7"`, true},
		{"a list holding it", `W/"2-6", W/"2-7"`, true},
		{"a list holding it, unspaced", `W/"2-6",W/"2-7"`, true},
		{"the wildcard", `*`, true},
		{"a different logbook version", `W/"2-6"`, false},
		{"a different emitter version", `W/"1-7"`, false},
		{"nothing", ``, false},
		{"junk", `garbage`, false},
		{"a prefix of it", `W/"2-"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := httpx.ETagMatches(c.ifNoneMatch, tag); got != c.want {
				t.Errorf("ETagMatches(%q, %s) = %v, want %v", c.ifNoneMatch, tag, got, c.want)
			}
		})
	}
}

// An empty current tag must never match, INCLUDING against `*`. A handler that
// reached the comparison with no ETag computed has a bug, and answering 304 to
// it hands the client an empty body it will treat as "unchanged" forever —
// which is DEC-49(b)'s permanent-empty-app failure arriving by a second route.
func TestNothingMatchesAnEmptyCurrentTag(t *testing.T) {
	for _, ifNoneMatch := range []string{`*`, `W/"2-7"`, ``} {
		if httpx.ETagMatches(ifNoneMatch, "") {
			t.Errorf("ETagMatches(%q, \"\") = true", ifNoneMatch)
		}
	}
}
