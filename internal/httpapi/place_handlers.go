// C1's pin and D2's removal.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a place write
// may contain — including the one thing this whole step is about, that an
// ABSENT `visits` key is not `visits: []` — is internal/logbook's; the
// statement order that makes D2's delete branch mean what the sheet says is
// internal/postgres's; and the question the sheet makes the user answer is
// `logbook.Service.RemovePlace`'s, because a type with no usable zero value is
// the only place "there is no default" cannot be forgotten.
//
// THE TWO ROUTES ANSWER DIFFERENT SHAPES AND EACH IS READ OFF THE NAV MAP.
// `PUT` answers a bare Place, which the phone splices into its cached log
// (DEC-32). `DELETE` answers the WHOLE LOG, for the reason `DELETE
// /v1/trips/{id}` does: the cache cannot splice a cascade. Removing a place
// takes its visits either way and then either clears two columns on the
// photographs filed there or deletes them outright — rows in three tables from
// one request, and the client's own `removePlace` already computes all of it,
// so a 204 would leave two implementations of one rule to drift.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putPlace is `PUT /v1/places/{id}`: C1's pin on a create, and the whole
// ordered visits array on any write that carries one.
//
// THE PATH WINS AND A DISAGREEING BODY IS REFUSED — `putTrip`'s rule, for its
// reason.
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

		// EmitPlace IS NOT OPTIONAL, AND THIS ROUTE IS WHERE THE RULE WOULD
		// HAVE BEEN BROKEN FIRST. A bare `Place` with no visits marshals
		// `"visits":null`, and `place.g.dart:30-32` reads it as
		// `(json['visits'] as List<dynamic>)` with no null branch — so the app
		// throws on the answer to its own write. C1's pin is exactly that
		// request: a wishlist place has no visits at all, so it is the
		// ordinary create rather than an edge case. The same defect against
		// `"cityIds":null` was measured on a running server before EmitTrip
		// existed.
		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitPlace(place))
	}
}

// removePlace is `DELETE /v1/places/{id}?photos=keep|delete`.
//
// THE PARAMETER IS REQUIRED AND ABSENT IS A 422 NAMING IT. "A default is a
// silent answer to the question D2 makes the user answer on screen", and the
// sheet makes them answer it because the two branches destroy different
// amounts: keep clears the pin and the occasion on every photograph filed
// there and leaves their date, their city and their caption; delete takes the
// photographs and the notes written on them.
//
// IT IS READ FROM THE QUERY AND NOT FROM THE BODY, and that is the shape R5's
// `?scope=all` established one step earlier: one destructive act, a parameter
// choosing how far it reaches. Where the two differ is that THAT parameter is
// optional and this one is not — there, the path is singular and the default
// is the smaller act; here, neither branch is obviously smaller from the
// caller's side and the sheet itself refuses to guess.
//
// THE PATH ID IS NOT VALIDATED, which is `deleteTrip`'s asymmetry and the
// client's own: a DELETE asks for something to be absent, and an absent thing
// satisfies it. `DELETE /v1/places/Fushimi%20Inari` names no place, and
// answering 422 would put a failure line in front of a user whose request had
// already been met. The statement is parameterised, so the value reaches
// nothing but a `WHERE id = $2`.
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

		// THE REFUSAL COMES BEFORE THE STORE IS TOUCHED. It is a fact about
		// the request rather than about any row, so it needs no transaction
		// and no advisory lock — and a request that never said how far to
		// reach must not get as far as a statement that reaches.
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
			// A removal always assembles: `assemble` is not a parameter here,
			// so a nil document is a store that answered nothing at all.
			writeLogbookFailure(w, r, deps.Log, logbook.ErrNoTraveller)
			return
		}

		envelope, err := logbook.Emit(format, *snapshot.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		// `tagFor` AND NOT FormatETag DIRECTLY, and the branch it carries is
		// reachable for D3's reason: a removal that removed nothing moves no
		// version, so a traveller who has never written anything and removes a
		// place they do not have is still at version 0 — and `W/"2-0"` is
		// exactly the tag DEC-49 panics rather than mint.
		if tag := tagFor(snapshot.Version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
