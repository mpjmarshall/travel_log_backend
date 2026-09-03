// C1's pin and D2's removal.
package httpapi

import (
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putPlace is `PUT /v1/places/{id}`: C1's pin on a create, and the whole
// ordered visits array on any write that carries one.
func putPlace(log *slog.Logger, places logbook.PlaceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.PlaceWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if !reconcileID(w, r, &body.ID) {
			return
		}
		if err := logbook.ValidatePlace(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		place, version, err := places.PutPlace(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, version)

		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitPlace(place))
	}
}

// removePlace is `DELETE /v1/places/{id}?photos=keep|delete`.
func removePlace(log *slog.Logger, service logbook.Service) http.HandlerFunc {
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

		photos, err := logbook.ParsePhotoDisposition(r.URL.Query().Get("photos"))
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		snapshot, err := service.RemovePlace(r.Context(), traveller.ID, r.PathValue("id"), photos)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}
		if snapshot.Document == nil {
			writeLogbookFailure(w, r, log, logbook.ErrNoTraveller)
			return
		}

		envelope, err := logbook.Emit(format, *snapshot.Document)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		if tag := tagFor(snapshot.Version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
