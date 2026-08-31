// The city search, over the real mux and the real auth, with a fake geocoder.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"travellog/internal/geocode"
)

type fakeGeocoder struct {
	found []geocode.City
	err   error
	asked string
	limit int
}

func (f *fakeGeocoder) Search(_ context.Context, q string, limit int) ([]geocode.City, error) {
	f.asked, f.limit = q, limit
	return f.found, f.err
}

func searchHarness(t *testing.T, geo *fakeGeocoder) (*harness, string) {
	t.Helper()
	h := newHarness(t, options{geocoder: geo})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.signIn(t, "matt@example.com")
	token, _ := issued.decode(t)["token"].(string)
	if token == "" {
		t.Fatalf("no token in %s", issued.body)
	}
	return h, "Bearer " + token
}

func TestCitySearchAnswersWhatTheGeocoderFound(t *testing.T) {
	geo := &fakeGeocoder{found: []geocode.City{{
		Name: "Gulf Shores", Region: "Alabama",
		CountryCode: "US", CountryName: "United States",
		Lat: 30.2711, Lon: -87.6894,
	}}}
	h, bearer := searchHarness(t, geo)

	got := h.do(t, http.MethodGet, "/v1/cities/search?q=Gulf+Shores", "", bearer)
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", got.status, got.body)
	}
	if geo.asked != "Gulf Shores" {
		t.Errorf("the geocoder was asked %q", geo.asked)
	}
	if geo.limit != MaxCitySuggestions {
		t.Errorf("limit = %d, want %d", geo.limit, MaxCitySuggestions)
	}
	for _, want := range []string{"Gulf Shores", "Alabama", "US", "30.2711"} {
		if !strings.Contains(string(got.body), want) {
			t.Errorf("the answer does not carry %q:\n%s", want, got.body)
		}
	}
}

func TestCitySearchNeedsASession(t *testing.T) {
	h, _ := searchHarness(t, &fakeGeocoder{})

	got := h.do(t, http.MethodGet, "/v1/cities/search?q=Kyoto", "", "")
	if got.status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: a free geocoder behind an open route is "+
			"somebody else's quota, spent by anyone who finds it", got.status)
	}
}

func TestABlankQueryAnswersAnEmptyListWithoutAsking(t *testing.T) {
	geo := &fakeGeocoder{}
	h, bearer := searchHarness(t, geo)

	got := h.do(t, http.MethodGet, "/v1/cities/search?q=%20%20", "", bearer)
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.status)
	}
	if geo.asked != "" {
		t.Errorf("a blank query reached the geocoder as %q", geo.asked)
	}
	if !strings.Contains(string(got.body), `"cities":[]`) {
		t.Errorf("want an empty list rather than null:\n%s", got.body)
	}
}

func TestAGeocoderThatFailedIsTryAgainAndNotNoSuchPlace(t *testing.T) {
	h, bearer := searchHarness(t, &fakeGeocoder{err: errors.New("429 upstream")})

	got := h.do(t, http.MethodGet, "/v1/cities/search?q=Kyoto", "", bearer)
	if got.status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: an empty list would tell the traveller "+
			"the place does not exist", got.status)
	}
}

func TestNoResultsIsAnEmptyListAndNotNull(t *testing.T) {
	h, bearer := searchHarness(t, &fakeGeocoder{})

	got := h.do(t, http.MethodGet, "/v1/cities/search?q=zzzzzz", "", bearer)
	if got.status != http.StatusOK ||
		!strings.Contains(string(got.body), `"cities":[]`) {
		t.Errorf("status %d, body %s", got.status, got.body)
	}
}
