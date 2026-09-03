// T5's 'Add a city', and the one route in this API that answers two shapes.
package httpapi

import (
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putCity is `PUT /v1/cities/{id}`: createCity, and setTripCities' one
// reachable case folded into it.
func putCity(log *slog.Logger, cities logbook.CityStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		format, readable := requestedFormat(r)
		if !readable {
			w.Header().Set(formatHeader, emittableFormats())
			httpx.WriteError(w, r, httpx.CodeUnsupportedFormat)
			return
		}

		var body logbook.CityWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if !reconcileID(w, r, &body.ID) {
			return
		}
		if err := logbook.ValidateCity(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		written, err := cities.PutCity(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, written.Version)

		if written.Document == nil {
			httpx.WriteJSON(w, r, http.StatusOK, written.City)
			return
		}
		envelope, err := logbook.Emit(format, *written.Document)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
