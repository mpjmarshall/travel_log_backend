// The one conditional read and the one whole-state write.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a trip may
// contain is internal/logbook's; what the log looks like on the wire is its
// emitter's; what one read sees is internal/postgres's snapshot. What is here
// is the tag, the condition, the status, and the one function that turns a
// domain sentinel into a word from DEC-12's closed vocabulary.
//
// THE ORDER INSIDE THE READ IS THE WHOLE OF DEC-31 AND IT IS EASY TO GET
// BACKWARDS. The version is taken first, inside the snapshot; the tag is built
// from it; If-None-Match is compared; and only then — and only if the
// comparison says the client is behind — are the five lists assembled. A 304
// computed after building the body saves bandwidth and no server work at all,
// which is half the point, and it is the reason Store.Read takes a callback
// rather than answering a document.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// formatHeader is DEC-53's negotiation, and it is used in BOTH directions: the
// client declares what it can read, and a 406 names what this build can write.
const formatHeader = "X-Logbook-Format"

func readLogbook(deps Deps) http.HandlerFunc {
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

		ifNoneMatch := r.Header.Get("If-None-Match")
		var etag string
		snapshot, err := deps.Logbook.Read(r.Context(), traveller.ID, func(version int64) bool {
			etag = tagFor(version)
			return !httpx.ETagMatches(ifNoneMatch, etag)
		})
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		if snapshot.Document == nil {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		envelope, err := logbook.Emit(format, *snapshot.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}

// putTrip is the whole-state upsert on a client-minted key (DEC-33), so it is
// idempotent by construction and needs no idempotency apparatus.
//
// THE PATH WINS AND A DISAGREEING BODY IS REFUSED. A body with no id is the
// ordinary case — the path already carries it — but a body naming a DIFFERENT
// trip is a client that believes it is writing somewhere else, and honouring
// either half of that would put the write where nobody asked for it.
func putTrip(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := auth.TravellerFrom(r.Context())
		if !held {
			httpx.WriteError(w, r, httpx.CodeInternal)
			return
		}

		var body logbook.TripWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}

		id := r.PathValue("id")
		if body.ID == "" {
			body.ID = id
		}
		if body.ID != id {
			httpx.WriteFieldError(w, r, "id")
			return
		}
		if err := logbook.ValidateTrip(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		trip, version, err := deps.Logbook.PutTrip(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		// A write always leaves a version of at least 1: the bump is taken
		// before the body runs, so there is no `tagFor` branch to take here.
		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, version))
		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitTrip(trip))
	}
}

// tagFor answers the empty string for a log nothing has ever written to, and
// that is a decision rather than a gap.
//
// A traveller starts at logbook_version 0 and DEC-49's tag needs BOTH halves —
// FormatETag panics on a zero, deliberately, because a tag with one half is
// the defect the first half exists to prevent. So the read serves 200 with no
// tag at all rather than `W/"1-0"`. httpx.ETagMatches then answers false for
// every If-None-Match including `*`, which is the right answer: a 304 against
// a log the client has never held hands it an empty body it will treat as
// unchanged, which is DEC-49(b)'s permanently empty app.
func tagFor(version int64) string {
	if version < 1 {
		return ""
	}
	return httpx.FormatETag(logbook.EmitterVersion, version)
}

// requestedFormat reads DEC-53's header. A MISSING HEADER IS THE CURRENT
// VERSION, so the header is additive and a client that never learned to send
// it is no worse off than one that cannot.
func requestedFormat(r *http.Request) (int, bool) {
	asked := r.Header.Get(formatHeader)
	if asked == "" {
		return logbook.FormatVersion, true
	}
	version, err := strconv.Atoi(asked)
	if err != nil {
		return 0, false
	}
	for _, emittable := range logbook.Formats() {
		if version == emittable {
			return version, true
		}
	}
	return 0, false
}

func emittableFormats() string {
	emittable := logbook.Formats()
	text := make([]string, len(emittable))
	for i, version := range emittable {
		text[i] = strconv.Itoa(version)
	}
	return strings.Join(text, ", ")
}

// writeLogbookFailure is DEC-62's one mapping for this half of the API: the
// sentinel is the domain's word and the code is the wire's.
//
// A TRAVELLER WHO HAS GONE IS A 401 AND NOT A 500, and the difference matters
// to the phone. The row can be deleted between the credential being accepted
// and the query running; the honest report is that the credential is not live,
// and the answer is to sign in again. A 500 would have the client wait for a
// server that is perfectly well.
//
// EVERY BRANCH PASSES A NAMED CONSTANT, which is what keeps httpx's AST sweep
// able to see this file: its one exemption is WriteErrorFor.
func writeLogbookFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var invalid logbook.InvalidFieldError

	switch {
	case errors.As(err, &invalid):
		httpx.WriteFieldError(w, r, invalid.Field)
	case errors.Is(err, logbook.ErrNoTraveller):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, logbook.ErrNoTrip):
		httpx.WriteError(w, r, httpx.CodeNotFound)
	case errors.Is(err, logbook.ErrUnsupportedFormat):
		w.Header().Set(formatHeader, emittableFormats())
		httpx.WriteError(w, r, httpx.CodeUnsupportedFormat)
	default:
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}
