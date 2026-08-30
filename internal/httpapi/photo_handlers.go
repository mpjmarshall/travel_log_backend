// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change'.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// snoozeBody is what N1's 'Later' answers: the rows that moved, in id order.
type snoozeBody struct {
	Photos []logbook.Photo `json:"photos"`
}

// putPhoto is `PUT /v1/photos/{id}`: M2's 'Write a note', and the create.
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
		if tag := tagFor(version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// snoozePhotos is `POST /v1/photos/snooze`: N1's 'Later', and's second
// route in this API that takes a COLLECTION.
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
