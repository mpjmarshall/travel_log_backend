// M2's note, D1's delete, N1's 'Later' and M2.2's 'Change'.
package httpapi

import (
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// snoozeBody is what N1's 'Later' answers: the rows that moved, in id order.
type snoozeBody struct {
	Photos []logbook.Photo `json:"photos"`
}

// putPhoto is `PUT /v1/photos/{id}`: M2's 'Write a note', and the create.
func putPhoto(log *slog.Logger, photos logbook.PhotoStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.PhotoWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if !reconcileID(w, r, &body.ID) {
			return
		}
		if err := logbook.ValidatePhoto(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		photo, version, err := photos.PutPhoto(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, version)
		httpx.WriteJSON(w, r, http.StatusOK, photo)
	}
}

// deletePhoto is `DELETE /v1/photos/{id}`: D1, and the only destructive route
// in this plan that answers 204.
func deletePhoto(log *slog.Logger, photos logbook.PhotoStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		version, err := photos.DeletePhoto(r.Context(), traveller.ID, r.PathValue("id"))
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}
		if tag := tagFor(version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// snoozePhotos is `POST /v1/photos/snooze`: N1's 'Later', and's second
// route in this API that takes a collection.
func snoozePhotos(log *slog.Logger, photos logbook.PhotoStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.SnoozeWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateSnooze(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		photos, version, err := photos.SnoozePhotos(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}
		if tag := tagFor(version); tag != "" {
			w.Header().Set("ETag", tag)
		}
		httpx.WriteJSON(w, r, http.StatusOK, snoozeBody{Photos: photos})
	}
}

// refilePhoto is `POST /v1/photos/{id}/refile`: M2.2's 'Change'.
func refilePhoto(log *slog.Logger, service logbook.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
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
			writeLogbookFailure(w, r, log, err)
			return
		}

		refiled, err := service.RefilePhoto(r.Context(), traveller.ID, r.PathValue("id"), body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, refiled.Version)

		if refiled.Document == nil {
			httpx.WriteJSON(w, r, http.StatusOK, refiled.Photo)
			return
		}
		envelope, err := logbook.Emit(format, *refiled.Document)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
