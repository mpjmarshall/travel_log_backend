// U1's pencil, and the route that is deliberately alone on its path.
package httpapi

import (
	"log/slog"
	"net/http"
	"travellog/internal/logbook"

	"travellog/internal/httpx"
)

// travellerName is the body of `PATCH /v1/me`, and the pointer is the contract
// Than decoration.
type travellerName struct {
	Name *string `json:"name"`
}

// travellerBody is the answer, and it is the client's `Traveller` exactly:
// one key, `name`.
func patchMe(log *slog.Logger, books logbook.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
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

		named, version, err := books.SetTravellerName(r.Context(), traveller.ID, *body.Name)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, version)
		httpx.WriteJSON(w, r, http.StatusOK, named)
	}
}
