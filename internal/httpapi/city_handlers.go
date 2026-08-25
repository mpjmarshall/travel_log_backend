// T5's 'Add a city', and the one route in this API that answers two shapes.
//
// THIS PACKAGE TRANSLATES HTTP AND NOTHING MORE (DEC-62). What a city write
// may contain is internal/logbook's, what it does to the rows is
// internal/postgres's, and what is here is the status, the tag, the format
// negotiation and the choice between the two answers — which is READ OFF THE
// STORE'S ANSWER rather than off the request a second time.
//
// TWO SHAPES ON ONE ROUTE IS A REAL COST AND IT WAS PAID DELIBERATELY. Without
// `attachTo` this is DEC-32's ordinary write response: a bare City the phone
// splices into its cached log. With it, TWO entities moved — the city was
// created AND a trip's `cityIds` grew — and a phone handed only the city would
// have to re-derive the itinerary from its own copy of the rule, which is the
// second implementation D3's answer shape exists to avoid. So the cascading
// case answers the whole envelope, exactly as `DELETE /v1/trips/{id}` does.
//
// THE ALTERNATIVE WAS TWO ROUTES AND IT IS WORSE. `PUT /v1/cities/{id}` then
// `PUT /v1/trips/{id}` is two round trips, two version bumps and a window in
// which a city exists and belongs to no trip — and the client cannot even
// express it: `createCity` takes `attachTo` and does both under ONE `_commit`,
// so a failure between them is a state its own model has no name for.
package httpapi

import (
	"net/http"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// putCity is `PUT /v1/cities/{id}`: createCity, and setTripCities' one
// reachable case folded into it.
//
// THE PATH WINS AND A DISAGREEING BODY IS REFUSED, which is `putTrip`'s rule
// and is here for its reason: a body naming a different city is a client that
// believes it is writing somewhere else, and honouring either half of that
// puts the write where nobody asked for it.
//
// IT NEGOTIATES THE FORMAT EVEN THOUGH ONLY ONE OF ITS TWO ANSWERS IS AN
// ENVELOPE. A client that can only read version 2 must be told when this build
// would write something else, and which shape this particular request earns is
// not known until the store answers — so the negotiation happens first, on
// every request, rather than on the branch that happens to need it.
func putCity(deps Deps) http.HandlerFunc {
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

		var body logbook.CityWrite
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
		if err := logbook.ValidateCity(body); err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		written, err := deps.Cities.PutCity(r.Context(), traveller.ID, body)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}

		// A write always leaves a version of at least 1: WithTravellerTx bumps
		// before the body runs, so there is no `tagFor` branch to take here.
		w.Header().Set("ETag", httpx.FormatETag(logbook.EmitterVersion, written.Version))

		// THE SHAPE IS DECIDED BY THE STORE'S ANSWER AND NOT BY RE-READING THE
		// REQUEST. `written.Document` is non-nil exactly when the attach
		// happened, so the two cannot disagree — where `body.AttachTo != nil`
		// asked here would be a second reading of the same fact, and the
		// failure mode is a 200 whose body is not the shape its own headers
		// and its own write imply.
		if written.Document == nil {
			// NO EmitCity, AND THAT IS THE MEASUREMENT RATHER THAN AN
			// OMISSION: a bare `City` carries no list key at all, so there is
			// no nil slice for encoding/json to write as null. emit.go states
			// it where somebody reaching for symmetry with EmitTrip will find
			// it.
			httpx.WriteJSON(w, r, http.StatusOK, written.City)
			return
		}
		envelope, err := logbook.Emit(format, *written.Document)
		if err != nil {
			writeLogbookFailure(w, r, deps.Log, err)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, envelope)
	}
}
