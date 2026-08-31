package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"travellog/internal/geocode"
	"travellog/internal/httpx"
)

// A geocoder that refused or never answered is CodeTimeout: 503, and the one
// code this client already reads as try-again rather than as no such place.

// MaxCitySuggestions is what one search may answer. Small on purpose: the
// screen shows a short list and a bigger one is somebody else's quota.
const MaxCitySuggestions = 8

// Geocoder is what turns a typed name into places. The server holds it so no
// install is a separate unthrottled caller of a free service.
type Geocoder interface {
	Search(ctx context.Context, q string, limit int) ([]geocode.City, error)
}

func searchCities(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Geocode == nil {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			httpx.WriteJSON(w, r, http.StatusOK, citiesAnswer{Cities: []geocode.City{}})
			return
		}

		found, err := deps.Geocode.Search(r.Context(), q, MaxCitySuggestions)
		if err != nil {
			deps.Log.Warn("geocode: the search did not answer",
				slog.String("err", err.Error()))
			httpx.WriteError(w, r, httpx.CodeTimeout)
			return
		}
		if found == nil {
			found = []geocode.City{}
		}
		httpx.WriteJSON(w, r, http.StatusOK, citiesAnswer{Cities: found})
	}
}

type citiesAnswer struct {
	Cities []geocode.City `json:"cities"`
}
