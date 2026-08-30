// H1's three writes: the switches, the new link, and the stop.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// setShareOptions is `PUT /v1/trips/{id}/share`: H1's three switches.
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

// writeTrip is the answer, spelled once for the four routes that give it.
func writeTrip(w http.ResponseWriter, r *http.Request, status int, trip logbook.Trip, version int64) {
	w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
	httpx.WriteJSON(w, r, status, logbook.EmitTrip(trip))
}
