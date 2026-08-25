// The three media routes: begin, commit and mint.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What may be stored
// is internal/logbook's allowlist; what a signature covers is internal/media's;
// what a commit means is internal/logbook.Service's. What is here is the body
// shapes, the statuses, and the loop that turns a list of ids into a list of
// capabilities.
//
// WHAT AN UPLOAD ACTUALLY IS, ONCE, because three handlers only make sense
// against it: begin -> presigned PUT straight to the bucket -> commit ->
// reference. The API never sees a photograph's bytes (DEC-36), so the two
// requests it does see are small JSON ones and the megabytes never join the
// contention DB_MAX_OPEN_CONNS was sized against.
//
// AND THE ORDERING IS NOT ADVISORY. DEC-58 put real foreign keys on all four
// asset columns, so an object must be COMMITTED before any row can reference
// it — enforced twice, in Go for the 422 that names the field and in the schema
// as the guarantee. The Go check exists to produce a message, not to be the
// guard.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
	"travellog/internal/media"
)

// beginBody is `POST /v1/media`'s answer.
//
// THREE FIELDS ARE OMITTED WHEN THE OBJECT IS ALREADY THERE, and that is a
// decision rather than tidiness (V4-SF5a). An `alreadyExists` response must not
// hand out a LIVE WRITE CAPABILITY for an object whose bytes are already
// committed and already referenced. DEC-24 made exactly this call for the
// public envelope — "no URL is minted at all, not an unusable one, not an
// expired one" — and handing out a working PUT against a committed address is
// worse than a merely redundant one, because `If-None-Match: *` is what stops
// it landing and that is a property of the SIGNATURE rather than of this
// response.
//
// `uploadHeaders` IS NOT OPTIONAL WHEN THERE IS A URL (DEC-88, STO-BLO-2). A
// presigned PUT whose signature covers `x-amz-checksum-sha256`, `Content-Type`,
// `Content-Length` and `If-None-Match` is UNUSABLE unless the caller replays
// each one exactly — and the checksum's value is the BASE64 digest while `id`
// and the wire format are HEX. Measured: header omitted -> 400 AccessDenied
// with the object absent; header present with the wrong value -> 403
// SignatureDoesNotMatch; header present and correct -> 200. A Flutter client
// handed only `uploadUrl` gets a 400 on every upload, for ever, with no way to
// derive the required header from the response. So the map's values are
// ALREADY ENCODED SERVER-SIDE and the client replays them verbatim.
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

// mintBody is a list, IN THE ORDER THE IDS WERE ASKED FOR. The client holds the
// ids and pairs by index; answering a map keyed by id would be the same data
// with a second way to get the pairing wrong.
type mintBody struct {
	URLs []string `json:"urls"`
}

// beginMedia is `POST /v1/media`.
//
// 201 ON BOTH PATHS, AND THAT IS DELIBERATE. This is an upsert on a
// CLIENT-MINTED content address, so it is idempotent by construction — the
// same shape `PUT /v1/trips/{id}` has, which answers 200 for a create and an
// update alike. Answering 200-vs-201 would be a SECOND signal for the one fact
// `alreadyExists` already carries, and two signals for one fact is how they
// drift.
func beginMedia(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.MediaBegin
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		// THE BOUND IS CHECKED BEFORE ANYTHING IS SIGNED, which is what
		// MEDIA_MAX_BYTES is: an API-side refusal to MINT, taken before the
		// capability exists (PD-20). It can never be a ceiling at the bucket,
		// because SigV4 signs an exact header VALUE — so what the signature
		// pins is `== byteSize` and never `<= MEDIA_MAX_BYTES`.
		if err := logbook.ValidateMediaBegin(body, deps.MediaMaxBytes); err != nil {
			writeMediaFailure(w, r, deps.Log, err)
			return
		}

		row, err := deps.Media.BeginMedia(r.Context(), traveller.ID, body)
		if err != nil {
			writeMediaFailure(w, r, deps.Log, err)
			return
		}

		answer := beginBody{ID: row.ID, AlreadyExists: row.Committed()}
		if !answer.AlreadyExists {
			url, headers, err := deps.Objects.PresignPut(r.Context(),
				media.Key{Traveller: traveller.ID, Object: row.ID},
				media.Upload{SHA256: row.ID, ByteSize: row.ByteSize, ContentType: row.ContentType})
			if err != nil {
				writeMediaFailure(w, r, deps.Log, err)
				return
			}
			// `expiresAt` IS READ BACK OFF THE URL THE SIGNER PRODUCED, not
			// computed from a second copy of the lifetime. A `PresignTTL`
			// field on Deps would be two variables holding one fact — the
			// mistake R2 refused for `Key.Object` and `Upload.SHA256` — and
			// the one that goes wrong here is silent: the client is told a
			// window that is not the window the signature carries, and the
			// upload fails with SignatureDoesNotMatch some minutes later.
			lifetime, err := media.ExpiresIn(url)
			if err != nil {
				writeMediaFailure(w, r, deps.Log, err)
				return
			}
			expires := deps.Clock()().Add(lifetime)
			answer.UploadURL, answer.UploadHeaders, answer.ExpiresAt = url, headers, &expires
		}
		httpx.WriteJSON(w, r, http.StatusCreated, answer)
	}
}

// commitMedia is `POST /v1/media/{id}/commit` — PD-05's first Service
// operation, and the only route in R1-R8 that spans the bucket and the
// database.
//
// A SECOND COMMIT IS 200 AND NOT 409 (SAF-MIN-12). The bucket-versus-database
// seam is the one non-atomic thing in the plan: the bucket confirms, the update
// fails, and the object exists with `uploaded_at` NULL — bytes the user has
// uploaded and cannot attach, with no route to retry that says so. This is that
// route, and it is only a retry if asking twice is allowed.
func commitMedia(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		id := r.PathValue("id")
		// THE PATH IS UNTRUSTED AND IS VALIDATED BEFORE IT REACHES A STORE.
		// Every other pushed screen's guard exists for the same reason: an id
		// arriving through route arguments has not been through a body
		// validator.
		if err := logbook.ValidateMediaID(id); err != nil {
			writeMediaFailure(w, r, deps.Log, err)
			return
		}

		row, err := deps.mediaService().CommitMedia(r.Context(), traveller.ID, id)
		if err != nil {
			writeMediaFailure(w, r, deps.Log, err)
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

// mintMedia is `POST /v1/media/mint`: a LIST of ids, one round trip for a
// twelve-photograph grid.
//
// THERE IS NO SINGULAR `GET /v1/media/{id}` (OE-1). It would be this route with
// a one-element list — same auth, same capability headers, same lifetime, same
// payload — costing a second handler, a second set of no-store legs, a second
// route-coverage entry and a second place to get the private-versus-public
// lifetime wrong.
//
// THE LOOP IS HERE AND NOT IN THE STORE (OE-2). Presigning is a local HMAC
// with no network call once the region is pinned (internal/media's New says
// why, and R2 measured the branch), so a batch method on the object store would
// save nothing a loop does not while adding a second site where the lifetime
// choice can diverge from PresignGet's. The DATABASE read is the opposite case
// and IS batched: a hundred ids is a hundred round trips.
//
// IT IS THE `POST` THAT WRITES NOTHING, which is the row `Route.Mutating`
// would have existed for — see routes.go for why the field went anyway.
func mintMedia(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.MediaMint
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}
		if err := logbook.ValidateMediaMint(body); err != nil {
			writeMediaFailure(w, r, deps.Log, err)
			return
		}

		rows, err := deps.Media.MediaObjects(r.Context(), traveller.ID, *body.IDs)
		if err != nil {
			writeMediaFailure(w, r, deps.Log, err)
			return
		}
		committed := make(map[string]bool, len(rows))
		for _, row := range rows {
			committed[row.ID] = row.Committed()
		}

		urls := make([]string, 0, len(*body.IDs))
		for _, id := range *body.IDs {
			// TWO MISSES, TWO ANSWERS, AND THE CLIENT ACTS ON THEM
			// DIFFERENTLY. An id this traveller has never begun is `not_found`
			// — a wrong reference, and nothing to wait for. An id begun and
			// not committed is `upload_incomplete` — the object's STATE is
			// wrong and the answer is to finish the upload and ask again.
			// Collapsing them would tell a client to retry a reference that
			// can never resolve.
			state, known := committed[id]
			if !known {
				writeMediaFailure(w, r, deps.Log, logbook.ErrNoMediaObject)
				return
			}
			if !state {
				writeMediaFailure(w, r, deps.Log, logbook.ErrUploadIncomplete)
				return
			}
			// THE PRIVATE AUDIENCE, AND IT IS ONE WRONG WORD AWAY FROM THE
			// PUBLIC ONE (DEC-84). This is the phone's own read and is the
			// revocation knob (DEC-44); `media.Public` is fifteen minutes and
			// belongs to `GET /l/{token}` alone.
			url, err := deps.Objects.PresignGet(r.Context(),
				media.Key{Traveller: traveller.ID, Object: id}, media.Private)
			if err != nil {
				writeMediaFailure(w, r, deps.Log, err)
				return
			}
			urls = append(urls, url)
		}
		httpx.WriteJSON(w, r, http.StatusOK, mintBody{URLs: urls})
	}
}

// writeMediaFailure is DEC-62's one mapping for this half of the API.
//
// EVERY BRANCH PASSES A NAMED CONSTANT, which is what keeps httpx's AST sweep
// able to see this file: its one exemption is WriteErrorFor.
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
		// 409 AND NOT 422. The row EXISTS and the request is well-formed; what
		// is wrong is the object's STATE, which is a conflict. httpx's table
		// left the door open for the media step to overturn it — "the media
		// step may overturn this, it owns the flow" — and the flow agrees with
		// the table, so it stands.
		httpx.WriteError(w, r, httpx.CodeUploadIncomplete)
	case httpx.DependencyIsDown(err):
		// DEC-96. A request that could not reach the database — or the bucket
		// — has not encountered a handler bug, and a 500 tells the client the
		// opposite: do not retry, the request is poison.
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}
