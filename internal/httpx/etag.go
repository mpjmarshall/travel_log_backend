// The ETag, both halves of it.
package httpx

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatETag writes both halves, and panics if it is handed less than that.
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

// ParseETag reads a tag back.
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
// under rfc 9110's weak comparison.
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
