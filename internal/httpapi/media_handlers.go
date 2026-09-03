// The three media routes: begin, commit and mint.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
	"travellog/internal/media"
)

// beginBody is `POST /v1/media`'s answer.
type beginBody struct {
	ID            string            `json:"id"`
	AlreadyExists bool              `json:"alreadyExists"`
	UploadURL     string            `json:"uploadUrl,omitempty"`
	ExpiresAt     *time.Time        `json:"expiresAt,omitempty"`
	UploadHeaders map[string]string `json:"uploadHeaders,omitempty"`
}

// mediaBody is what commit answers: the row, as the client should now believe
// it is.
type mediaBody struct {
	ID            string     `json:"id"`
	ByteSize      int64      `json:"byteSize"`
	ContentType   string     `json:"contentType"`
	AlreadyExists bool       `json:"alreadyExists"`
	UploadedAt    *time.Time `json:"uploadedAt"`
}

// mintBody is a list, in the order the ids were asked for.
type mintBody struct {
	URLs []string `json:"urls"`
}

// beginMedia is `POST /v1/media`.
func beginMedia(log *slog.Logger, rows logbook.MediaStore, objects media.Store, maxBytes int64, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.MediaBegin
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateMediaBegin(body, maxBytes); err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}

		row, err := rows.BeginMedia(r.Context(), traveller.ID, body)
		if err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}

		answer := beginBody{ID: row.ID, AlreadyExists: row.Committed()}
		if !answer.AlreadyExists {
			url, headers, err := objects.PresignPut(r.Context(),
				media.Key{Traveller: traveller.ID, Object: row.ID},
				media.Upload{SHA256: row.ID, ByteSize: row.ByteSize, ContentType: row.ContentType})
			if err != nil {
				writeMediaFailure(w, r, log, err)
				return
			}
			lifetime, err := media.ExpiresIn(url)
			if err != nil {
				writeMediaFailure(w, r, log, err)
				return
			}
			expires := now().Add(lifetime)
			answer.UploadURL, answer.UploadHeaders, answer.ExpiresAt = url, headers, &expires
		}
		httpx.WriteJSON(w, r, http.StatusCreated, answer)
	}
}

// commitMedia is `POST /v1/media/{id}/commit`: it reconciles what the bucket
// holds against what the row declares, and is the one rule that is not a store call.
func commitMedia(log *slog.Logger, rows logbook.MediaStore, objects media.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		id := r.PathValue("id")
		if err := logbook.ValidateMediaID(id); err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}

		row, err := logbook.CommitMedia(r.Context(), rows, objects, traveller.ID, id)
		if err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, mediaBody{
			ID:            row.ID,
			ByteSize:      row.ByteSize,
			ContentType:   row.ContentType,
			AlreadyExists: row.Committed(),
			UploadedAt:    row.UploadedAt,
		})
	}
}

// mintMedia is `POST /v1/media/mint`: a list of ids, one round trip for a
// twelve-photograph grid.
func mintMedia(log *slog.Logger, rows logbook.MediaStore, objects media.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.MediaMint
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateMediaMint(body); err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}

		rows, err := rows.MediaObjects(r.Context(), traveller.ID, *body.IDs)
		if err != nil {
			writeMediaFailure(w, r, log, err)
			return
		}
		committed := make(map[string]bool, len(rows))
		for _, row := range rows {
			committed[row.ID] = row.Committed()
		}

		urls := make([]string, 0, len(*body.IDs))
		for _, id := range *body.IDs {
			state, known := committed[id]
			if !known {
				writeMediaFailure(w, r, log, logbook.ErrNoMediaObject)
				return
			}
			if !state {
				writeMediaFailure(w, r, log, logbook.ErrUploadIncomplete)
				return
			}
			url, err := objects.PresignGet(r.Context(),
				media.Key{Traveller: traveller.ID, Object: id}, media.Private)
			if err != nil {
				writeMediaFailure(w, r, log, err)
				return
			}
			urls = append(urls, url)
		}
		httpx.WriteJSON(w, r, http.StatusOK, mintBody{URLs: urls})
	}
}

// writeMediaFailure is the one mapping for this half of the API.
func writeMediaFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var invalid logbook.InvalidFieldError

	switch {
	case errors.As(err, &invalid):
		httpx.WriteFieldError(w, r, invalid.Field)
	case errors.Is(err, logbook.ErrNoTraveller):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, logbook.ErrNoMediaObject):
		httpx.WriteError(w, r, httpx.CodeNotFound)
	case errors.Is(err, logbook.ErrUploadIncomplete):
		httpx.WriteError(w, r, httpx.CodeUploadIncomplete)
	case httpx.DependencyIsDown(err):
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}
