// U1's pencil, and the route that is deliberately alone on its path.
//
// `GET /v1/me` IS DELETED AND THIS FILE IS PATCH ONLY (OE-7). The only
// traveller field the client renders is `name`, and it arrives inside
// `GET /v1/logbook`'s `traveller` object; the client's `Traveller` type has
// exactly one field and DELIBERATELY no id, because an id belongs to the
// account a backend issues. `POST /v1/auth/register` already answers 201 with
// the traveller, so the sign-up flow needs no second fetch either. A read
// endpoint answering data the one read already carries is exactly the shape
// DEC-31 exists to refuse — and it would be a SECOND place in the tree where
// the traveller's wire shape is defined, which is the thing that drifts.
//
// If a settings screen ever needs to show the signed-in address, `GET /v1/me`
// comes back WITH THAT CALLER, in the same commit.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// travellerName is the body of `PATCH /v1/me`, and the pointer is DEC-89's
// contract rather than decoration.
//
// A `string` would make `{}` and `{"name":""}` the same value, so a client
// that forgot the key would be told its name is empty — which is the defect
// DEC-89 was ruled about wearing a smaller hat. Absent means leave alone; on
// this route there is exactly one field, so leaving it alone means writing
// nothing, and that is a 422 rather than a 200 that did nothing: PATCH /v1/me
// with no name is a request that asks for nothing at all, and answering 200
// would tell a client its rename landed.
type travellerName struct {
	Name *string `json:"name"`
}

// travellerBody is the answer, and it is the client's `Traveller` exactly: one
// key, `name`. No id — see the file comment.
//
// IT IS A WHOLE OBJECT AND NOT A BARE STRING, so the phone splices it into the
// `traveller` slot of its cached document the way DEC-32's Trip is spliced
// into `trips`.
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

		// THE TRIM AND THE EMPTY-NAME REFUSAL ARE THE STORE'S, not this
		// handler's, and that is DEC-62 rather than laziness: the rule is
		// "an empty name is refused and is not a way to clear it", which is a
		// business rule about what a log may hold. The store answers
		// InvalidFieldError and writeLogbookFailure turns it into the 422 that
		// names the field.
		named, version, err := deps.Logbook.SetTravellerName(r.Context(), traveller.ID, *body.Name)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
		httpx.WriteJSON(w, r, http.StatusOK, named)
	}
}
