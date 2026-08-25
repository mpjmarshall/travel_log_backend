// H1's three writes: the switches, the new link, and the stop.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a share write
// may contain is internal/logbook's, what it does to the rows is
// internal/postgres's, and what is here is the verb, the status, the tag and
// the one mapping from a domain sentinel to a word from DEC-12's closed
// vocabulary.
//
// THREE VERBS ON ONE PATH, AND THE SPLIT IS THE CLIENT'S THREE METHODS RATHER
// THAN A REST HABIT. `setShareOptions` writes flags and never touches a link;
// `newShareLink` mints one and leaves the flags where the user just set them;
// `stopSharing` kills the link AND puts the flags back. Folding them into one
// PUT would mean a body that has to say which of the three it meant, which is
// the request the verb already carries.
//
// ALL THREE ANSWER A WHOLE Trip (DEC-32) rather than a status, because the
// phone splices the answer into its cached log. Only ONE of them can leave
// `shareLinkId` non-nil — see the mint — and after DEC-85 no read ever can.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// setShareOptions is `PUT /v1/trips/{id}/share`: H1's three switches.
//
// A SETTING SAVES ON THE FLICK, which is the client's own decision and is why
// there is no Done button on H1 and no batching here. "A save deferred to 'on
// the way out' has nowhere to report a failure and no guarantee of running at
// all", and two file writes saved is not worth a coordinate switch lost to a
// swipe.
func setShareOptions(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.ShareWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}

		// THERE IS NO VALIDATOR CALL HERE AND THAT IS NOT AN OMISSION. Every
		// field of ShareWrite is a `*bool`: the only two values JSON can put
		// in one are legal, and a body naming none of the three is DEC-89's
		// leave-alone rather than an error. internal/logbook/share.go says so
		// where a reader looking for `ValidateShareOptions` will find it.
		trip, version, err := deps.Share.SetShareOptions(
			r.Context(), traveller.ID, r.PathValue("id"), body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		writeTrip(w, r, http.StatusOK, trip, version)
	}
}

// newShareLink is `POST /v1/trips/{id}/share`: H1's 'New link', and U1 has no
// equivalent.
//
// IT IS 201 AND NOT 200, matching `POST /v1/auth/session` — the other route in
// this API that mints a capability the caller did not have a moment ago. Both
// are non-idempotent by construction: asking twice produces two links, the
// second of which kills the first.
//
// THE TOKEN COMES FROM THE CLIENT. On a trip that was never shared this is
// what STARTS sharing; there is no other control that mints one, and the sheet
// is reachable from T4 for any trip. The switches are deliberately left where
// they are: on a trip that has never been shared they are already the
// defaults, and on one being re-linked they are the choices the user just made
// on this screen.
func newShareLink(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.ShareMint
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateShareMint(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		trip, version, err := deps.Share.NewShareLink(
			r.Context(), traveller.ID, r.PathValue("id"), *body.Token)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		writeTrip(w, r, http.StatusCreated, trip, version)
	}
}

// stopSharing is `DELETE /v1/trips/{id}/share`: H1's 'Stop sharing' and U1's
// own 'Stop', which are the same client method reached from two screens.
//
// IT POPS ONCE, NOT TWICE — which is the client's business and is quoted here
// because it is what decides the response shape. "Where a destructive control
// leaves you is read off the nav map, per control": D1 pops past the viewer
// and D3 pops past T4, because a screen about the thing you just destroyed
// must not be what you land on. 'Stop sharing' pops ONCE, because the trip
// behind it still exists. So this answers the TRIP and not the log: nothing
// the user is looking at has gone.
//
// AN UNKNOWN TRIP IS A 404 DESPITE THE VERB. It is `stopSharing`, which goes
// through the client's `_replaceTrip` and answers false for an id the log does
// not hold — "a set asks for a value the log then has to hold" — and its
// answer is a whole Trip, which cannot be produced for a trip that is not
// there. Revoking NOTHING on a trip that IS there is a success, because that
// is the retry case.
func stopSharing(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		trip, version, err := deps.Share.StopSharing(r.Context(), traveller.ID, r.PathValue("id"))
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		writeTrip(w, r, http.StatusOK, trip, version)
	}
}

// writeTrip is DEC-32's answer, spelled once for the four routes that give it.
//
// EmitTrip IS NOT OPTIONAL. A bare `Trip` with no cities marshals
// `"cityIds":null`, and `trip.g.dart` reads it as `(json['cityIds'] as
// List<dynamic>)` with no null branch — so the app throws on the answer to its
// own write. The GET was correct the whole time, because Emit normalises; the
// two paths had one rule and one implementation of it, and this is the second.
//
// THE TAG IS ALWAYS PRESENT HERE. Every one of these writes goes through
// WithTravellerTx, which bumps before the body runs, so the version is at
// least 1 and `FormatETag`'s zero-half panic is unreachable — unlike D3's
// delete, where a miss leaves version 0 and `tagFor` has a branch to take.
func writeTrip(w http.ResponseWriter, r *http.Request, status int, trip logbook.Trip, version int64) {
	w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
	httpx.WriteJSON(w, r, status, logbook.EmitTrip(trip))
}
