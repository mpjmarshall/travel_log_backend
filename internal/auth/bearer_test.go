// Reading the credential off the request, and carrying the traveller down the
// chain. Test-first.
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestWith(header string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/logbook", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

func TestBearerReadsTheTokenAndAcceptsTheSchemeInAnyCasing(t *testing.T) {
	for _, header := range []string{
		"Bearer abc123", "bearer abc123", "BEARER abc123", "BeArEr abc123",
	} {
		got, ok := Bearer(requestWith(header))
		if !ok || got != "abc123" {
			t.Errorf("Bearer(%q) = (%q, %v), want (\"abc123\", true).\n"+
				"    RFC 7235 makes the scheme case-insensitive, and a client that sends\n"+
				"    `bearer` is not wrong — it is just unlucky.", header, got, ok)
		}
	}
}

func TestBearerRefusesEverythingThatIsNotOne(t *testing.T) {
	for name, header := range map[string]string{
		"absent":                        "",
		"no scheme":                     "abc123",
		"another scheme":                "Basic abc123",
		"a scheme that starts the same": "BearerToken abc123",
		"the scheme alone":              "Bearer",
		"the scheme and a space":        "Bearer ",
		"only spaces after it":          "Bearer    ",
		"two tokens":                    "Bearer abc123 def456",
	} {
		if got, ok := Bearer(requestWith(header)); ok {
			t.Errorf("%s: Bearer(%q) = (%q, true), want false", name, header, got)
		}
	}
}

func TestBearerDoesNotTrimTheTokenItself(t *testing.T) {
	// A token is base64url and holds no spaces, so anything that survives the
	// single separator is the client's problem and not this function's to
	// tidy. Trimming here would make "Bearer  abc" and "Bearer abc" the same
	// credential, and the store would then be asked about a token the client
	// did not send.
	if got, ok := Bearer(requestWith("Bearer  abc123")); ok {
		t.Errorf("Bearer(%q) = (%q, true); a second space is not a credential", "Bearer  abc123", got)
	}
}

func TestTheTravellerCrossesTheContextIntact(t *testing.T) {
	name := "Matt"
	want := Traveller{ID: "abc", Email: "Matt@Example.com", Name: &name}

	ctx := WithTraveller(context.Background(), want)
	got, ok := TravellerFrom(ctx)
	if !ok {
		t.Fatalf("TravellerFrom answered false for a context that carries one")
	}
	if got.ID != want.ID || got.Email != want.Email || got.Name == nil || *got.Name != name {
		t.Errorf("TravellerFrom = %+v, want %+v", got, want)
	}
}

func TestTravellerFromAnswersFalseForAContextThatCarriesNone(t *testing.T) {
	if tr, ok := TravellerFrom(context.Background()); ok {
		t.Errorf("TravellerFrom on a bare context = (%+v, true), want false.\n"+
			"    A handler that reads a zero Traveller and believes it would serve one\n"+
			"    traveller's log to everybody.", tr)
	}
}

// The context key is unexported and typed, so nothing outside this package can
// plant a traveller in a request. A string key would let any package — or any
// middleware from anywhere — write to the same slot.
func TestAStringKeyCannotForgeATraveller(t *testing.T) {
	//lint:ignore SA1029 that is the point of the leg
	ctx := context.WithValue(context.Background(), "traveller", Traveller{ID: "forged"})
	if tr, ok := TravellerFrom(ctx); ok {
		t.Errorf("a string key planted %+v into the traveller slot", tr)
	}
}
