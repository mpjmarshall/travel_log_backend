package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"travellog/internal/httpx"
)

// TWO-SIDED, WHICH IS DEC-109's LESSON APPLIED TO A REDACTOR. A leg asserting
// only that `/l/…` disappears is satisfied by a function that answers
// `[redacted]` for every request in the application, which would take the
// access log's usefulness with it — and nothing else in this repository asserts
// that an ordinary path survives being logged.
func TestLoggedPathHidesTheCapabilityAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/l/mnpqrstuvwxy", httpx.RedactedSharePath},
		{"/l/kyoto-9f2a", httpx.RedactedSharePath},
		// NOT A REGISTERED PATTERN AND REDACTED ANYWAY. A rule that decided
		// what a token looks like would print the ones that merely look wrong,
		// and a typo'd capability is one character from a real one.
		{"/l/mnpqrstuvwxy/extra", httpx.RedactedSharePath},
		{"/l/", httpx.RedactedSharePath},

		// The other side. `/l` is not under the prefix — there is no segment
		// after it to be a secret — and every path in the rest of the API
		// carries ids the caller is already authenticated for.
		{"/l", "/l"},
		{"/v1/logbook", "/v1/logbook"},
		{"/v1/trips/autumn-crossing/share", "/v1/trips/autumn-crossing/share"},
		{"/healthz", "/healthz"},
	} {
		got := httpx.LoggedPath(httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got != tc.want {
			t.Errorf("LoggedPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
