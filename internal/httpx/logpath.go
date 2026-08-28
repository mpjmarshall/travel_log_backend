// The one place a request path is made safe to write down (PD-08), and it is a
// SIBLING OF ClientKey rather than a middleware.
//
// WHY IT IS A FUNCTION AND NOT A WRAPPER. The obvious shape is a middleware
// that rewrites `r.URL.Path` on the way in, and it is wrong twice over: the
// mux matches on that path, so rewriting it above the mux routes every share
// read to nothing; and rewriting it BELOW the mux leaves the access log, which
// sits above, printing the raw one. What every site actually needs is the
// string to LOG, which is a value, so this is a function four callers pass a
// request to — the same shape `ClientKey` already has for the same reason.
//
// WHY THE ATTRIBUTE REDACTOR CANNOT DO IT. `internal/logging` redacts on the
// attribute KEY — `token`, `passphrase`, `authorization`. The key here is
// `path` and the capability is inside the VALUE, so that mechanism can never
// fire on this and adding `path` to its list would blank every path in the
// file. Measured on the running container:
//
//	{"msg":"request","method":"GET","path":"/l/CAPABILITY7XY","status":404}
//
// WHAT A SHARE PATH IS. `GET /l/{token}` is the only unauthenticated route in
// this API and its path segment is a pure bearer capability — whoever reads
// the log line reads the trip. Every other path in this application carries
// ids that are already in the document the caller is authenticated for.
package httpx

import (
	"net/http"
	"strings"
)

// SharePathPrefix is what the public read is mounted under. It is here rather
// than in internal/httpapi because this package must be able to recognise the
// path without importing the one that registers it.
const SharePathPrefix = "/l/"

// RedactedSharePath is what goes in the log instead.
//
// IT NAMES THE ROUTE AND HIDES ONLY THE SEGMENT THAT IS A SECRET. A blanked
// path — `[redacted]` alone — would make "how many public reads were there,
// and how many 404ed" unanswerable, which is the operational question this
// route raises most. And it is greppable: the acceptance check counts
// occurrences of it as the POSITIVE CONTROL for the absence check beside it.
const RedactedSharePath = "/l/[redacted]"

// LoggedPath is the path this request may be written down under.
//
// EVERY PATH UNDER `/l/` IS REDACTED, INCLUDING THE ONES NO ROUTE MATCHES.
// `/l/` with nothing after it carries no capability and `/l/a/b` is not a
// registered pattern, and both are redacted anyway: the alternative is a rule
// that has to decide what a token looks like, which would print the ones that
// merely look wrong — a typo'd capability is still a capability, and one
// character away from a real one.
func LoggedPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if strings.HasPrefix(r.URL.Path, SharePathPrefix) {
		return RedactedSharePath
	}
	return r.URL.Path
}
