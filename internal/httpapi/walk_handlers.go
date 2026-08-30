// N1's 'Name it' and N1's 'Discard' — one route, two controls, and the third
// place this API has had to say that a nil slice marshals to `null`.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putWalk is `PUT /v1/walks/{id}`: `setWalkName`, `dismissWalk`, and the
// create.
func putWalk(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.WalkWrite
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
		if err := logbook.ValidateWalk(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		walk, version, err := deps.Walks.PutWalk(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))

		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitWalk(walk))
	}
}
