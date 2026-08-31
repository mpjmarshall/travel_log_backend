// What a geocoder answers, and what this package refuses to pass on.
package geocode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"travellog/internal/geocode"
)

const photonGulfShores = `{"features":[
 {"geometry":{"coordinates":[-87.6894,30.2711],"type":"Point"},
  "properties":{"name":"Gulf Shores","state":"Alabama","country":"United States",
                "countrycode":"US","osm_value":"town","osm_key":"place"}},
 {"geometry":{"coordinates":[-81.7723,26.1220],"type":"Point"},
  "properties":{"name":"Gulf Shores","state":"Florida","country":"United States",
                "countrycode":"US","osm_value":"residential","osm_key":"highway"}}
]}`

func serving(t *testing.T, status int, body string) *geocode.Photon {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("User-Agent") == "" {
				t.Error("no User-Agent: OSM's policy requires an identifying one")
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	t.Cleanup(server.Close)
	return geocode.NewPhoton(server.URL, "travel-log-test/1.0", server.Client())
}

func TestASearchAnswersPlacesAndNotStreets(t *testing.T) {
	found, err := serving(t, 200, photonGulfShores).
		Search(context.Background(), "Gulf Shores", 5)
	if err != nil {
		t.Fatalf("Search() = %v", err)
	}

	if len(found) != 1 {
		t.Fatalf("Search() = %d results, want 1: a residential street is not a "+
			"city, and offering one puts a street in the log's geography", len(found))
	}
	got := found[0]
	if got.Name != "Gulf Shores" || got.Region != "Alabama" ||
		got.CountryCode != "US" || got.CountryName != "United States" {
		t.Errorf("Search() = %+v", got)
	}
	if got.Lat < 30.27 || got.Lat > 30.28 || got.Lon > -87.68 || got.Lon < -87.70 {
		t.Errorf("coordinates = %f,%f, want Gulf Shores Alabama: Photon gives "+
			"them longitude first and a swap puts the pin in the wrong hemisphere",
			got.Lat, got.Lon)
	}
}

func TestAnEmptyQueryAsksNobody(t *testing.T) {
	asked := false
	server := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { asked = true }))
	t.Cleanup(server.Close)

	found, err := geocode.NewPhoton(server.URL, "t/1", server.Client()).
		Search(context.Background(), "   ", 5)
	if err != nil || len(found) != 0 {
		t.Fatalf("Search(blank) = %v, %v", found, err)
	}
	if asked {
		t.Error("a blank query reached the geocoder, which spends somebody " +
			"else's quota to answer nothing")
	}
}

func TestARefusalIsAnErrorAndNotAnEmptyList(t *testing.T) {
	_, err := serving(t, 429, "slow down").
		Search(context.Background(), "Kyoto", 5)
	if err == nil {
		t.Error("a 429 answered no error, so being rate-limited reads to the " +
			"traveller as a place that does not exist")
	}
}

func TestNonsenseIsNotAResult(t *testing.T) {
	found, err := serving(t, 200, `{"features":[]}`).
		Search(context.Background(), "zzzzzz", 5)
	if err != nil || len(found) != 0 {
		t.Fatalf("Search() = %v, %v, want no results and no error", found, err)
	}
}

func TestTheLimitIsPassedOn(t *testing.T) {
	var asked string
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			asked = r.URL.Query().Get("limit")
			_, _ = w.Write([]byte(`{"features":[]}`))
		}))
	t.Cleanup(server.Close)

	_, _ = geocode.NewPhoton(server.URL, "t/1", server.Client()).
		Search(context.Background(), "Kyoto", 7)
	if asked != "7" {
		t.Errorf("limit reached the geocoder as %q, want 7", asked)
	}
}

func TestASlowGeocoderDoesNotHangTheRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := geocode.NewPhoton(server.URL, "t/1", server.Client()).
		Search(ctx, "Kyoto", 5); err == nil {
		t.Error("a geocoder that never answers must fail the search, not hold " +
			"the traveller's request open")
	}
}
