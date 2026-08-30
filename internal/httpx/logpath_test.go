package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"travellog/internal/httpx"
)

// two-sided, which is the lesson applied to A REDACTOR.
func TestLoggedPathHidesTheCapabilityAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/l/mnpqrstuvwxy", httpx.RedactedSharePath},
		{"/l/kyoto-9f2a", httpx.RedactedSharePath},
		{"/l/mnpqrstuvwxy/extra", httpx.RedactedSharePath},
		{"/l/", httpx.RedactedSharePath},

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
