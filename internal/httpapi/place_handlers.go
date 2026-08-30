// C1's pin and D2's removal.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putPlace is `PUT /v1/places/{id}`: C1's pin on a create, and the whole
// ordered visits array on any write that carries one.
func putPlace(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.PlaceWrite
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
		if err := logbook.ValidatePlace(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		place, version, err := deps.Places.PutPlace(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))

		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitPlace(place))
	}
}

// removePlace is `DELETE /v1/places/{id}?photos=keep|delete`.
func removePlace(deps Deps) http.HandlerFunc {
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

		photos, err := logbook.ParsePhotoDisposition(r.URL.Query().Get("photos"))
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		snapshot, err := deps.places().RemovePlace(r.Context(), traveller.ID, r.PathValue("id"), photos)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		if snapshot.Document == nil {
			writeLogbookFailure(w, r, deps.Log, logbook.ErrNoTraveller)
			return
		}

		envelope, err := logbook.Emit(format, *snapshot.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		if tag := tagFor(snapshot.Version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
