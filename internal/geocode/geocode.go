// Turning a typed name into a city with coordinates, which the app cannot do
// for itself and must not ask a third party for on every install.
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// City is one answer, in the shape the logbook stores.
type City struct {
	Name        string  `json:"name"`
	Region      string  `json:"region"`
	CountryCode string  `json:"countryCode"`
	CountryName string  `json:"countryName"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
}

// Photon is Komoot's OSM geocoder. It needs no key and answers the country
// code and region without a second request, which Nominatim does not.
type Photon struct {
	base   string
	agent  string
	client *http.Client
}

func NewPhoton(base, agent string, client *http.Client) *Photon {
	return &Photon{base: strings.TrimSuffix(base, "/"), agent: agent, client: client}
}

// places are the osm_value words that name somewhere a traveller goes. A
// street matching the query is not a city and must not become one.
var places = map[string]bool{
	"city": true, "town": true, "village": true, "hamlet": true,
	"municipality": true, "borough": true, "suburb": true, "island": true,
}

type photonAnswer struct {
	Features []struct {
		Geometry struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			Name        string `json:"name"`
			State       string `json:"state"`
			County      string `json:"county"`
			Country     string `json:"country"`
			CountryCode string `json:"countrycode"`
			OSMValue    string `json:"osm_value"`
		} `json:"properties"`
	} `json:"features"`
}

// Search answers the places matching q, or an error. An empty query asks
// nobody: it would spend somebody else's quota to answer nothing.
func (p *Photon) Search(ctx context.Context, q string, limit int) ([]City, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	ask := p.base + "/api/?" + url.Values{
		"q":     {q},
		"limit": {strconv.Itoa(limit)},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ask, nil)
	if err != nil {
		return nil, fmt.Errorf("geocode: building the request: %w", err)
	}
	req.Header.Set("User-Agent", p.agent)

	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geocode: asking %s: %w", p.base, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocode: %s answered %d", p.base, res.StatusCode)
	}

	var answer photonAnswer
	if err := json.NewDecoder(res.Body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("geocode: reading the answer: %w", err)
	}

	out := []City{}
	for _, f := range answer.Features {
		if !places[f.Properties.OSMValue] || f.Properties.Name == "" {
			continue
		}
		if len(f.Geometry.Coordinates) != 2 {
			continue
		}
		region := f.Properties.State
		if region == "" {
			region = f.Properties.County
		}
		out = append(out, City{
			Name:        f.Properties.Name,
			Region:      region,
			CountryCode: strings.ToUpper(f.Properties.CountryCode),
			CountryName: f.Properties.Country,
			Lon:         f.Geometry.Coordinates[0],
			Lat:         f.Geometry.Coordinates[1],
		})
	}
	return out, nil
}
