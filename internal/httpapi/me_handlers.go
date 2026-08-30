// U1's pencil, and the route that is deliberately alone on its path.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// travellerName is the body of `PATCH /v1/me`, and the pointer is the contract
// Than decoration.
type travellerName struct {
	Name *string `json:"name"`
}

// travellerBody is the answer, and it is the client's `Traveller` exactly:
// one key, `name`.
func patchMe(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body travellerName
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if body.Name == nil {
			httpx.WriteFieldError(w, r, "name")
			return
		}

		named, version, err := deps.Logbook.SetTravellerName(r.Context(), traveller.ID, *body.Name)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
		httpx.WriteJSON(w, r, http.StatusOK, named)
	}
}
