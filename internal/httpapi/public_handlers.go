// `GET /l/{token}` — the only route in this API that no bearer token stands in
// front of.
//
// EVERYTHING HERE IS A DECISION ABOUT WHAT A STRANGER HOLDING A URL CAN SEE.
// The three sharing switches are the contract, docs/PUBLIC-ENVELOPE.md is the
// allowlist they act on, and a switch that is off has to REMOVE THE DATA FROM
// THE ENVELOPE rather than hide it in whatever renders the page — there is no
// page, and the JSON is what somebody's browser will hold.
//
// THE THREE LAYERS, EACH OWNING ONE QUESTION, none of which is answered twice:
//
//	internal/postgres/share_read.go  WHICH ROWS   the three row rules, in SQL
//	internal/logbook/public.go       WHICH KEYS   the allowlist and the flags
//	this file                        WHAT HTTP    the status, the headers, the
//	                                              mint, and the two answers
//	                                              that have to be one answer
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
//
// REVOKED AND UNKNOWN DO THE SAME WORK, NOT MERELY ANSWER THE SAME BYTES
// (PD-12, DEC-10). DEC-10 says "the SAME 404 WITH THE SAME WORK DONE" and
// byte-identical is necessary and is not sufficient: a handler that returned
// early on "no row" but, for a revoked row, resolved the trip, read three
// flags and minted a dozen URLs would answer identical bytes and still be a
// clean oracle for "this token was once real" — timing, database load, bucket
// signatures, every one of them a channel. DEC-67's revoke-and-keep design
// makes that worth attacking, because every token ever issued is still a row.
//
// SO THE LOOKUP IS THE ONLY BRANCH. `ShareLink` selects regardless of
// `revoked_at`; the two failures are folded into ONE condition and ONE call to
// WriteError, so there is no second path for a defect to grow in.
//
// THE TOKEN IS NOT VALIDATED, AND THAT IS DELIBERATE RATHER THAN AN OMISSION.
// `logbook.ValidateShareMint` exists for the route where the CLIENT MINTS a
// token and refuses anything under twelve characters of `[a-z0-9]` — an
// entropy floor on a capability this server is about to create. Applying it
// HERE would refuse tokens this server already issued: the client's own
// captured log carries `kyoto-9f2a`, ten characters with a hyphen, which is
// what `make seed` writes into `share_links` and what the fixture's only live
// link IS. A validating handler would answer 404 for a live capability. A
// token that could not have been minted simply fails the digest lookup, which
// is the same answer with no second rule to keep in step.
func publicShare(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		link, err := deps.Public.ShareLink(r.Context(),
			logbook.HashShareToken(r.PathValue("token")))

		switch {
		case errors.Is(err, logbook.ErrNoShare) || (err == nil && link.Revoked):
			// ONE CONDITION, ONE WRITE. Splitting these would be the defect
			// PD-12 describes wearing a tidier shape.
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
			// THE PUBLIC AUDIENCE, AND IT IS ONE WRONG WORD AWAY FROM THE
			// PRIVATE ONE (DEC-84). Fifteen minutes rather than two, because
			// this envelope has NOTHING TO RE-MINT ITS URLS WITH: the reader
			// holds no credential and `POST /v1/media/mint` is authenticated.
			//
			// SO FIFTEEN MINUTES IS A HARD WALL AND NOT A ROLLING WINDOW. It
			// runs from the moment this envelope was generated; whoever
			// renders a share page must re-GET `/l/{token}` to refresh them.
			// The honest client sentence that follows: stopping a share stops
			// new links at once, and a photograph already loaded may keep
			// working for up to fifteen minutes.
			//
			// THE TRAVELLER IS THE LINK'S OWN. It came out of the token
			// lookup, and the bucket is keyed the same way the table is, so
			// reading it from anywhere else would be minting against a
			// traveller nobody named.
			return deps.Objects.PresignGet(r.Context(),
				media.Key{Traveller: link.TravellerID, Object: objectID}, media.Public)
		})
		if err != nil {
			writePublicFailure(w, r, deps.Log, err)
			return
		}

		// NO ETag, AND THE ABSENCE IS A DECISION. `GET /v1/logbook` carries
		// one because the phone re-reads the whole log constantly and a 304 is
		// what makes that cheap. Here the body embeds capabilities that expire
		// in fifteen minutes, so a validator inviting an intermediary to ask
		// "is this still fresh?" is inviting it to serve a cached envelope
		// full of URLs — which is the exact thing `Cache-Control: no-store`
		// on this row exists to prevent. A tag and a no-store are not a
		// contradiction to a correct cache, and they are an invitation to a
		// careless one.
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}

// writePublicFailure is DEC-62's one mapping for the public read, and it is
// deliberately SHORTER than its siblings.
//
// THERE IS NO FIELD ERROR AND NO 422 BRANCH: this route has no body and no
// parameters, so there is nothing a caller can get wrong except the token, and
// a wrong token is a 404. What is left is the two failures that are the
// server's — a dependency that is down, and everything else — and both of them
// tell a stranger nothing about whether the token was real.
//
// EVERY BRANCH PASSES A NAMED CONSTANT, which is what keeps httpx's AST sweep
// able to see this file.
func writePublicFailure(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, logbook.ErrNoTrip), errors.Is(err, logbook.ErrNoTraveller):
		// A LIVE LINK NAMING A TRIP THAT HAS GONE, which is reachable:
		// `share_links_trip_fk` is ON DELETE CASCADE, so D3 takes the link
		// with the trip — and a link resolved a moment before that commit is a
		// token in this handler's hand naming a row that is no longer there.
		// It is the same 404 an unknown token gets, for the same reason.
		httpx.WriteError(w, r, httpx.CodeNotFound)
	case httpx.DependencyIsDown(err):
		// DEC-96. A request that could not reach the database — or the bucket
		// — has not encountered a handler bug, and a 500 tells the client the
		// opposite: do not retry, the request is poison. `CodeTimeout` is the
		// word this vocabulary already uses for it, and `httpx.RetryAfter`
		// sets the header above the whole chain.
		logPublicFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeTimeout)
	default:
		logPublicFailure(r, log, err)
		httpx.WriteError(w, r, httpx.CodeInternal)
	}
}

// logPublicFailure is DEC-101's one line per 500, and it goes through
// httpx.LoggedPath for the reason every other site here does: the path IS the
// capability.
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
