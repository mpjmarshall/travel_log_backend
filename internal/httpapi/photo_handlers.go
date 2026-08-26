// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change'.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a photograph
// write may contain — including the thing this whole step is about, that
// `PhotoWrite` has no slot for a place or an occasion — is internal/logbook's;
// the statements that keep the pair coherent are internal/postgres's; and the
// server's refusal to CHOOSE an occasion is `logbook.Service.RefilePhoto`'s,
// because that is where an operation's rules live rather than a body's.
//
// FOUR ROUTES AND FOUR DIFFERENT ANSWERS, EACH READ OFF WHAT MOVED:
//
//	PUT    /v1/photos/{id}          200 + a bare Photo        + ETag
//	DELETE /v1/photos/{id}          204                       + ETag
//	POST   /v1/photos/snooze        200 + the rows it wrote   + ETag
//	POST   /v1/photos/{id}/refile   200 + a Photo, OR the WHOLE ENVELOPE
//
// A BARE `Photo` IS SAFE AND THE REASON IS MEASURED, not assumed: it carries
// no list field, so there is no nil slice for the marshaller to write as
// `null`. `internal/logbook/emit.go` holds the marshalled proof and
// `emit_sweep_test.go` holds the mechanism — an `EmitPhoto` would be the empty
// forwarding method DEC-62 warns against one layer up.
//
// THE REFILE ANSWERS TWO SHAPES AND THE SHAPE IS A PROPERTY OF THE VALUE.
// `PhotoRefiled.Document` is nil exactly when no occasion was minted, which is
// the device `CityWritten` uses for `attachTo` and for its reason: asking the
// REQUEST which shape it earned would be two readings of one fact, and the
// failure mode is a 200 whose body is not the shape its own write implies.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// snoozeBody is what N1's 'Later' answers: the rows that moved, in id order.
//
// IT IS A LOCAL WRAPPER AND NOT A BARE ARRAY, on `mintBody`'s precedent — the
// other route in this API that takes a collection. A top-level JSON array is
// the one shape that cannot grow a sibling key without breaking every client
// that reads it, and this one has an obvious future sibling: how many ids were
// skipped.
//
// WHAT IS IN IT IS WHAT WAS WRITTEN, so what is ABSENT from it is what was
// skipped — the client holds the ids it asked for and can subtract. A map
// keyed by id would be the same data with a second way to get the pairing
// wrong.
type snoozeBody struct {
	Photos []logbook.Photo `json:"photos"`
}

// putPhoto is `PUT /v1/photos/{id}`: M2's 'Write a note', and DEC-33's create.
//
// THE PATH WINS AND A DISAGREEING BODY IS REFUSED — `putTrip`'s rule, for its
// reason.
func putPhoto(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.PhotoWrite
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
		if err := logbook.ValidatePhoto(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		photo, version, err := deps.Photos.PutPhoto(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
		httpx.WriteJSON(w, r, http.StatusOK, photo)
	}
}

// deletePhoto is `DELETE /v1/photos/{id}`: D1, and the only destructive route
// in this plan that answers 204.
//
// THE CACHE CAN SPLICE THIS ONE, WHICH IS WHY IT IS NOT THE WHOLE LOG. D2 and
// D3 answer an envelope because a cascade removes rows from several tables and
// the phone cannot re-derive that from a sheet's copy. Nothing in this schema
// references a photograph: one row leaves and the log is coherent, so the
// client's own `deletePhoto` — which drops the id from its list and nothing
// else — is already the whole of it.
//
// IT STILL CARRIES AN ETag, and that is what the 204 is for rather than a bare
// success. The phone has just spliced a deletion into a document it caches
// under a version; without the new tag its next conditional GET either refetches
// the whole log or, worse, keeps serving a body that still holds the photograph.
//
// THE PATH ID IS NOT VALIDATED, which is `deleteTrip`'s asymmetry and the
// client's own: a DELETE asks for something to be absent, and an absent thing
// satisfies it.
func deletePhoto(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		version, err := deps.Photos.DeletePhoto(r.Context(), traveller.ID, r.PathValue("id"))
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		// `tagFor` AND NOT FormatETag DIRECTLY, and the branch it carries is
		// reachable for D3's reason: a delete that deleted nothing moves no
		// version, so a traveller who has never written anything is still at
		// version 0 — and `W/"2-0"` is exactly the tag DEC-49 panics rather
		// than mint.
		if tag := tagFor(version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// snoozePhotos is `POST /v1/photos/snooze`: N1's 'Later', and the second route
// in this API that takes a COLLECTION.
//
// IT IS A POST ON A COLLECTION PATH AND NOT A PUT ON EACH PHOTOGRAPH, because
// one row on N1 stands for every unfiled photograph from one city on one trip.
// Thirty photographs snoozed one at a time is thirty version bumps, thirty
// round trips, and thirty chances to stop half way — a partial failure the
// client has no state for. All-or-nothing in one transaction with ONE bump is
// what makes "there is no partial" a fact rather than a hope.
//
// AN EMPTY MATCH IS A 200 AND SAYS SO. The client's own method "returns false
// without writing when the group is empty", so a 404 or a 422 would put a
// failure line in front of a user whose request was met by doing nothing. The
// answer carries an empty `photos` array and the ETag does not move.
func snoozePhotos(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.SnoozeWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateSnooze(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		photos, version, err := deps.Photos.SnoozePhotos(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		if tag := tagFor(version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, snoozeBody{Photos: photos})
	}
}

// refilePhoto is `POST /v1/photos/{id}/refile`: M2.2's 'Change'.
//
// IT IS A POST AND NOT A PUT, AND THAT IS NOT A REST HABIT. `PUT
// /v1/photos/{id}` already exists and is idempotent on a client-minted key;
// this is a different operation on the same resource — the client's own
// `refilePhoto`, which moves a pin AND an occasion together and may OPEN an
// occasion as a side effect. H1's three writes set the precedent one path
// over: three verbs on one path, chosen to match the client's three methods
// rather than a resource shape.
//
// IT GOES THROUGH `logbook.Service` AND NOT STRAIGHT TO THE STORE, which is
// the only route in this file that does. What the Service owns is the server's
// authority to choose an occasion, and it refuses to have any — see
// service.go, where the deletion test has a measured answer.
//
// THE FORMAT IS NEGOTIATED HERE AND NOT ON THE OTHER THREE, for the reason
// `deleteTrip` negotiates it: this route can answer a whole envelope, so a
// client that reads only version 2 has to be told when the build writes
// something else. The three that answer a bare entity carry no `version` key
// at all — DEC-32's splice is into a document whose format the client has
// already negotiated on its GET.
func refilePhoto(deps Deps) http.HandlerFunc {
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

		var body logbook.RefileWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateRefile(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		refiled, err := deps.photos().RefilePhoto(r.Context(), traveller.ID, r.PathValue("id"), body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, refiled.Version))

		// THE SHAPE IS READ OFF THE ANSWER. A minted occasion moved the PLACE
		// as well as the photograph — and renumbered every one of that place's
		// ordinals — so the phone cannot splice what it was not sent.
		if refiled.Document == nil {
			httpx.WriteJSON(w, r, http.StatusOK, refiled.Photo)
			return
		}
		envelope, err := logbook.Emit(format, *refiled.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
