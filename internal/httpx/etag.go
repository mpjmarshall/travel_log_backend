// The ETag, both halves of it (DEC-49).
//
// `W/"<emitterVersion>-<logbookVersion>"`. The second half is the data; the
// FIRST half is the code that rendered it, and it is there because of two
// defects found in the revision that had only the second:
//
//	(a) `W/"<logbookVersion>"` moves only on a WRITE. A deploy that changed the
//	    emitted document — a field added, a date rendered differently, DEC-40's
//	    `"version": 2` itself — did not move it, so every phone holding a
//	    cached body got 304 forever and kept serving the OLD SHAPE until
//	    somebody happened to write. The validator did not cover the thing that
//	    produced the bytes.
//
//	(b) the ETag was described as living beside the cached document rather than
//	    INSIDE it. Discard the file, keep the ETag, re-fetch: 304 with an empty
//	    body, and the phone has no log and no way to get one, permanently and
//	    silently. That half is a client-side lifecycle rule and it is recorded
//	    in the client prerequisites; what this file can do is make sure the
//	    server never emits a tag with a half missing.
//
// WEAK ON PURPOSE. The tag is a claim that the document is semantically the
// same, not that the bytes are — Go's map iteration order alone would break a
// byte-for-byte promise. If-None-Match uses WEAK COMPARISON (RFC 9110
// §8.8.3.2), so `W/"2-7"` and `"2-7"` match, which is what lets a proxy or a
// hand-written curl that dropped the `W/` still revalidate correctly.
package httpx

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatETag writes both halves, and PANICS if it is handed less than that.
//
// A zero version is what a caller reaches by forgetting an argument or by
// reading a column nobody set. It is a programmer error, not a client one, so
// it fails where the stack still names the caller — rather than emitting
// `W/"0-7"` and being found months later as a cache that never invalidates.
// The recover middleware turns the panic into a 500 with the request id in the
// log; it does not take the process down.
func FormatETag(emitterVersion, logbookVersion int64) string {
	if emitterVersion < 1 || logbookVersion < 1 {
		panic(fmt.Sprintf(
			"httpx: FormatETag(%d, %d): an ETag needs BOTH halves — the emitter "+
				"version AND the logbook version. A tag with one half is the "+
				"DEC-49 defect: it validates the data and not the code that "+
				"rendered it.",
			emitterVersion, logbookVersion))
	}
	return `W/"` + strconv.FormatInt(emitterVersion, 10) + "-" +
		strconv.FormatInt(logbookVersion, 10) + `"`
}

// ParseETag reads a tag back. It accepts the STRONG spelling on the way in and
// never emits it: a cache, a proxy or a curl echoes `"2-7"` without the `W/`,
// and refusing that would answer 200 to a client that is revalidating
// correctly.
//
// It refuses anything that is not two positive integers, which is what makes
// the one-half tag from DEC-49(a) unparseable rather than merely unusual.
func ParseETag(s string) (emitterVersion, logbookVersion int64, ok bool) {
	inner, isTag := opaqueTag(s)
	if !isTag {
		return 0, 0, false
	}
	inner = strings.Trim(inner, `"`)

	left, right, found := strings.Cut(inner, "-")
	if !found {
		return 0, 0, false
	}
	emitterVersion, err := strconv.ParseInt(left, 10, 64)
	if err != nil || emitterVersion < 1 {
		return 0, 0, false
	}
	logbookVersion, err = strconv.ParseInt(right, 10, 64)
	if err != nil || logbookVersion < 1 {
		return 0, 0, false
	}
	return emitterVersion, logbookVersion, true
}

// ETagMatches reports whether an If-None-Match header names the current tag,
// under RFC 9110's weak comparison.
//
// AN EMPTY CURRENT TAG MATCHES NOTHING, INCLUDING `*`. A handler that reached
// here with no tag computed has a bug, and answering 304 to it hands the client
// an empty body it will treat as "unchanged" — DEC-49(b)'s permanently empty
// app, arriving by a second route.
func ETagMatches(ifNoneMatch, etag string) bool {
	current, ok := opaqueTag(etag)
	if !ok {
		return false
	}

	header := strings.TrimSpace(ifNoneMatch)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}

	for _, candidate := range strings.Split(header, ",") {
		if tag, ok := opaqueTag(candidate); ok && tag == current {
			return true
		}
	}
	return false
}

// opaqueTag returns the quoted opaque-tag — quotes included, weak marker
// stripped — which is exactly what weak comparison compares.
func opaqueTag(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	return s, true
}
