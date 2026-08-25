// N1's 'Name it' and N1's 'Discard' — one route, two controls, and the third
// place this API has had to say that a nil slice marshals to `null`.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a walk write
// may contain is internal/logbook's, and the `CASE WHEN` that keeps a day's
// recording alive through a flag write is internal/postgres's.
//
// THERE IS NO `DELETE /v1/walks/{id}`, AND THAT IS THE CLIENT'S OWN DESIGN
// RATHER THAN AN OMISSION. N1's 'Discard' is a flag: "Discarding the nudge and
// discarding the recording are different things, and only the first is drawn
// on N1." D2's sheet promises the track stays with its day on both branches,
// and `walks` has no `place_id` at all. Nothing in this app authorises
// destroying a walk, so no route offers one — the same argument that leaves
// `DELETE /v1/cities/{id}` out of R6.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putWalk is `PUT /v1/walks/{id}`: `setWalkName`, `dismissWalk`, and DEC-33's
// create.
//
// THE PATH WINS AND A DISAGREEING BODY IS REFUSED — `putTrip`'s rule.
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

		// EmitWalk IS NOT OPTIONAL, AND THIS ROUTE IS THE ONE THE CLIENT
		// THROWS ON. A bare `Walk` with no points marshals `"points":null`,
		// and `photo.g.dart:47-49` reads it as
		// `(json['points'] as List<dynamic>).map(...)` with no null branch —
		// so the app throws on the answer to its own write. The same defect
		// against `"cityIds":null` was measured on a running server before
		// EmitTrip existed, and against `"visits":null` before EmitPlace.
		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitWalk(walk))
	}
}
