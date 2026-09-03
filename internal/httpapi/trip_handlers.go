// D3's delete, and the one write in this API that answers the whole log.
package httpapi

import (
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// deleteTrip answers 200 and the whole logbook, not 204.
func deleteTrip(log *slog.Logger, books logbook.Store) http.HandlerFunc {
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

		snapshot, err := books.DeleteTrip(r.Context(), traveller.ID, r.PathValue("id"))
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
