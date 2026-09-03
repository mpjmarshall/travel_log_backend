// N1's 'Name it' and N1's 'Discard' — one route, two controls, and the third
// place this API has had to say that a nil slice marshals to `null`.
package httpapi

import (
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putWalk is `PUT /v1/walks/{id}`: `setWalkName`, `dismissWalk`, and the
// create.
func putWalk(log *slog.Logger, walks logbook.WalkStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.WalkWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if !reconcileID(w, r, &body.ID) {
			return
		}
		if err := logbook.ValidateWalk(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		walk, version, err := walks.PutWalk(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, version)

		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitWalk(walk))
	}
}
