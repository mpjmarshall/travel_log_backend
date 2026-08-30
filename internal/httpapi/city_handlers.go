// T5's 'Add a city', and the one route in this API that answers two shapes.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putCity is `PUT /v1/cities/{id}`: createCity, and setTripCities' one
// reachable case folded into it.
func putCity(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
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
		id := r.PathValue("id")
		if body.ID == nil {
			body.ID = &id
		}
		if *body.ID != id {
			httpx.WriteFieldError(w, r, "id")
			return
		}
		if err := logbook.ValidateCity(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		written, err := deps.Cities.PutCity(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, written.Version))

		if written.Document == nil {
			httpx.WriteJSON(w, r, http.StatusOK, written.City)
			return
		}
		envelope, err := logbook.Emit(format, *written.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
