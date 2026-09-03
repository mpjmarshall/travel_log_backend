// The parts of a request every route repeats: the credential the middleware
// resolved, and the tag that pairs the emitter with the logbook version.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
)

// travellerOf answers the credential RequireTraveller put on the context. A
// request that reaches a handler without one is a wiring fault, not a client's.
func travellerOf(w http.ResponseWriter, r *http.Request) (auth.Traveller, bool) {
	traveller, held := auth.TravellerFrom(r.Context())
	if !held {
		httpx.WriteError(w, r, httpx.CodeInternal)
		return auth.Traveller{}, false
	}
	return traveller, true
}

// setTag writes the entity tag for a logbook version, and writes none for a
// log nobody has written to yet.
func setTag(w http.ResponseWriter, version int64) {
	if tag := tagFor(version); tag != "" {
		w.Header().Set("ETag", tag)
	}
}

// reconcileID makes the client-minted id in the path win. A body naming a
// different one is a client writing somewhere it did not ask to.
func reconcileID(w http.ResponseWriter, r *http.Request, bodyID **string) bool {
	id := r.PathValue("id")
	if *bodyID == nil {
		*bodyID = &id
	}
	if **bodyID != id {
		httpx.WriteFieldError(w, r, "id")
		return false
	}
	return true
}
