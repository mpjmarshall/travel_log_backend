// The one place a request path is made safe to write down, and it is a
// sibling of ClientKey rather than a middleware.
package httpx

import (
	"net/http"
	"strings"
)

// SharePathPrefix is what the public read is mounted under.
const SharePathPrefix = "/l/"

// RedactedSharePath is what goes in the log instead.
const RedactedSharePath = "/l/[redacted]"

// LoggedPath is the path this request may be written down under.
func LoggedPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if strings.HasPrefix(r.URL.Path, SharePathPrefix) {
		return RedactedSharePath
	}
	return r.URL.Path
}
