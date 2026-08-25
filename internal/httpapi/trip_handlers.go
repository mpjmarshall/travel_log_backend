// D3's delete, and the one write in this API that answers the whole log.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What the cascade
// takes is internal/postgres's, spelled as foreign keys rather than as
// statements; what is here is the status, the tag, the format negotiation and
// the mapping from a domain sentinel to a word from DEC-12's closed
// vocabulary.
//
// ------------------------------------------------------------------------
// THE NAME-CONFIRMATION GATE IS DECLINED, AND THE REASON IS HERE RATHER THAN
// IN A LENS REPORT (SAF-MAJ-7, PD-22).
//
// D3 is the most guarded control in the client. It itemises four consequences
// beside the words 'deleted' and 'kept', and then the user types the trip's
// name out before the action arms — `delete_sheets.dart:663` labels the field
// 'Type ${trip.name} to confirm', `:701` says 'The name does not match yet'
// until `armed = _confirm.text.trim() == trip.name`. This route requires a
// bearer token and nothing else. The safety lens offered two branches and this
// plan takes the second, which the lens itself names: say so, with the reason.
//
// THE GATE THE SHEET HAS IS A GATE ON THE HUMAN. Its value is the pause before
// the typing — the seconds in which somebody reads four itemised consequences
// and decides. A body field is a gate on the CLIENT, and the only client is the
// one that already drew the sheet: it would be the same software satisfying its
// own guard, twice, in one round trip.
//
// IT WOULD ALSO MAKE THE API'S GUARD AND THE SHEET'S GUARD TWO COPIES OF ONE
// STRING THAT CAN DRIFT. Rename the trip on one device and the other device's
// cached name no longer arms the delete — a failure the sheet does not have,
// introduced by the mechanism meant to strengthen it.
//
// AND THE THREAT IT IS OFFERED AGAINST IS LARGELY CLOSED ELSEWHERE. The lens's
// own note is that "an unconfirmed destructive route behind an OPEN
// registration surface is two gaps that compound", and it is right about the
// half that was compounding: DEC-86 shuts registration in this same step, so a
// stranger cannot hold a token at all.
//
// TRIGGER FOR REVISITING: a second traveller, or any caller of this route that
// is not the sheet.
// ------------------------------------------------------------------------
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// deleteTrip answers 200 AND THE WHOLE LOGBOOK, not 204.
//
// THE CACHE CANNOT SPLICE A CASCADE. Every other write in this plan answers a
// bare entity because DEC-32's response is something the phone patches into
// its cached document; D3 removes rows from five tables and clears a column on
// rows in a sixth. A client handed a 204 would have to re-derive that from the
// sheet's own copy, which is two implementations of one rule — and the
// client's `deleteTrip` already IS the second one, so the two would drift the
// first time the cascade changed.
//
// THE PATH ID IS NOT VALIDATED, AND THAT IS THE CLIENT'S OWN ASYMMETRY RATHER
// THAN AN OMISSION. `PUT /v1/trips/{id}` refuses an id that is not the shape
// of an id, because a SET "asks for a value the log then has to hold"
// (logbook.dart:753-757). A DELETE asks for something to be absent, and an
// absent thing satisfies it — `DELETE /v1/trips/Kyoto%20in%20May` names no
// trip, and answering 422 would put a failure line in front of a user whose
// request had already been met. The statement is parameterised, so the value
// reaches nothing but a `WHERE id = $2`.
func deleteTrip(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		// IT NEGOTIATES THE FORMAT FOR THE REASON THE READ DOES: what leaves
		// here is a whole envelope, so a client that can only read version 2
		// has to be told when this build writes something else. Answering the
		// current version regardless is DEC-40's refetch loop wearing a 200.
		format, readable := requestedFormat(r)
		if !readable {
			w.Header().Set(formatHeader, emittableFormats())
			httpx.WriteError(w, r, httpx.CodeUnsupportedFormat)
			return
		}

		snapshot, err := deps.Logbook.DeleteTrip(r.Context(), traveller.ID, r.PathValue("id"))
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		if snapshot.Document == nil {
			// A delete always assembles: `assemble` is not a parameter here,
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
		// reachable here in a way it is not on a write. A delete that removed
		// nothing moves no version, so a traveller who has never written
		// anything and deletes a trip they do not have is still at version 0 —
		// and `W/"2-0"` is exactly the tag DEC-49 panics rather than mint.
		if tag := tagFor(snapshot.Version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
