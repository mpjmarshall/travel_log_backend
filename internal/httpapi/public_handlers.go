// `GET /l/{token}` — the only route in this API that no bearer token stands
// in front of.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
	"travellog/internal/media"
)

// publicShare resolves a share token and answers the envelope.
func publicShare(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		link, err := deps.Public.ShareLink(r.Context(),
			logbook.HashShareToken(r.PathValue("token")))

		switch {
		case errors.Is(err, logbook.ErrNoShare) || (err == nil && link.Revoked):
			httpx.WriteError(w, r, httpx.CodeNotFound)
			return
		case err != nil:
			writePublicFailure(w, r, deps.Log, err)
			return
		}

		src, err := deps.Public.PublicLog(r.Context(), link.TravellerID, link.TripID)
		if err != nil {
			writePublicFailure(w, r, deps.Log, err)
			return
		}

		envelope, err := logbook.EmitPublic(src, func(objectID string) (string, error) {
			return deps.Objects.PresignGet(r.Context(),
				media.Key{Traveller: link.TravellerID, Object: objectID}, media.Public)
		})
		if err != nil {
			writePublicFailure(w, r, deps.Log, err)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}

// writePublicFailure is the one mapping for the public read, and it is
// deliberately SHORTER than its siblings.
func writePublicFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, logbook.ErrNoTrip), errors.Is(err, logbook.ErrNoTraveller):
		httpx.WriteError(w, r, httpx.CodeNotFound)
	case httpx.DependencyIsDown(err):
		logPublicFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logPublicFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}

// logPublicFailure is the one line per 500, and it goes through
// httpx.LoggedPath for the reason every other site here does.
func logPublicFailure(r *http.Request, log *slog.Logger, err error) {
	if log == nil {
		log = slog.Default()
	}
	log.LogAttrs(r.Context(), slog.LevelError, "the public read failed",
		slog.String("path", httpx.LoggedPath(r)),
		slog.String("requestId", httpx.RequestIDFrom(r.Context())),
		slog.String("err", err.Error()),
	)
}
