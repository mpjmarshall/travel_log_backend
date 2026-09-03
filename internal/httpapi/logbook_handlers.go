// The one conditional read and the one whole-state write.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// formatHeader is the negotiation, and it is used in both directions.
const formatHeader = "X-Logbook-Format"

func readLogbook(log *slog.Logger, books logbook.Store) http.HandlerFunc {
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

		ifNoneMatch := r.Header.Get("If-None-Match")
		var etag string
		snapshot, err := books.Read(r.Context(), traveller.ID, func(version int64) bool {
			etag = tagFor(version)
			return !httpx.ETagMatches(ifNoneMatch, etag)
		})
		if err != nil {
			writeLogbookFailure(w, r, log, err)
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
			writeLogbookFailure(w, r, log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}

// putTrip is the whole-state upsert on a client-minted key, so it is
// idempotent by construction and needs no idempotency apparatus.
func putTrip(log *slog.Logger, books logbook.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traveller, held := travellerOf(w, r)
		if !held {
			return
		}

		var body logbook.TripWrite
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteErrorFor(w, r, err)
			return
		}

		if !reconcileID(w, r, &body.ID) {
			return
		}
		if err := logbook.ValidateTrip(body); err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		trip, version, err := books.PutTrip(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, log, err)
			return
		}

		setTag(w, version)
		httpx.WriteJSON(w, r, http.StatusOK, logbook.EmitTrip(trip))
	}
}

// tagFor answers the empty string for a log nothing has ever written to, and
// that is a decision rather than a gap.
func tagFor(version int64) string {
	if version < 1 {
		return ""
	}
	return httpx.FormatETag(logbook.EmitterVersion, version)
}

// requestedFormat reads the header.
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

// writeLogbookFailure is the one mapping for this half of the API: the
// sentinel is the domain's word and the code is the wire's.
func writeLogbookFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var invalid logbook.InvalidFieldError

	switch {
	case errors.As(err, &invalid):
		httpx.WriteFieldError(w, r, invalid.Field)
	case errors.Is(err, logbook.ErrNoTraveller):
		httpx.WriteError(w, r, httpx.CodeUnauthenticated)
	case errors.Is(err, logbook.ErrNoTrip),
		errors.Is(err, logbook.ErrNoPlace),
		errors.Is(err, logbook.ErrNoPhoto),
		errors.Is(err, logbook.ErrNoWalk):
		httpx.WriteError(w, r, httpx.CodeNotFound)
	case errors.Is(err, logbook.ErrUnsupportedFormat):
		w.Header().Set(formatHeader, emittableFormats())
		httpx.WriteError(w, r, httpx.CodeUnsupportedFormat)
	case httpx.DependencyIsDown(err):
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}
