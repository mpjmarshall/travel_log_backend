// D3's route, over the real mux, the real middleware chain and the real auth,
// with a fake store.
//
// WHAT ONLY THIS CAN SAY. internal/seed's cascade legs are the counts, against
// the real schema and the client's own document; internal/postgres has the
// version behaviour. These are about what leaves the process: a 200 rather
// than a 204, a WHOLE ENVELOPE rather than a bare trip, and a repeated delete
// that is a success rather than a 404.
package httpapi

import (
	"net/http"
	"testing"

	"travellog/internal/logbook"
)

// THE ANSWER IS THE WHOLE LOGBOOK, IN THE CLIENT'S OWN ENVELOPE.
//
// The cache cannot splice a cascade: D3 removes rows from five tables and
// clears a column on rows in a sixth, so a bare trip or a 204 would leave the
// phone re-deriving the cascade from the sheet's copy — two implementations of
// one rule, and the client's `deleteTrip` is already the other one.
func TestDeletingATripAnswersTheWholeLogbook(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	got := h.do(t, http.MethodDelete, "/v1/trips/kyoto", "", token)
	if got.status != http.StatusOK {
		t.Fatalf("DELETE /v1/trips/kyoto = %d %s, want 200", got.status, got.body)
	}

	body := got.decode(t)
	if body["version"] != float64(logbook.FormatVersion) {
		t.Errorf("version = %v, want %d — the answer is an ENVELOPE, so it carries the "+
			"format version the client compares against its own constant",
			body["version"], logbook.FormatVersion)
	}
	inner, held := body["logbook"].(map[string]any)
	if !held {
		t.Fatalf("no `logbook` object: %s — a bare Trip is what every OTHER write "+
			"answers, and it cannot describe a cascade", got.body)
	}
	trips, isList := inner["trips"].([]any)
	if !isList {
		t.Fatalf("trips = %#v, want a list", inner["trips"])
	}
	if len(trips) != 0 {
		t.Errorf("the log came back holding %d trips after its only one was deleted", len(trips))
	}
	// The five lists are still LISTS and not nulls. Emit normalises, and this
	// is the second path through it — the write path is where that rule was
	// broken before (`"cityIds":null`).
	for _, key := range []string{"cities", "places", "photos", "walks"} {
		if _, isList := inner[key].([]any); !isList {
			t.Errorf("%s = %#v, want a list — a nil Go slice marshals to null, and the "+
				"client's decoder has no null branch", key, inner[key])
		}
	}
}

// A REPEATED DELETE IS A SUCCESS AND CARRIES THE SAME TAG.
//
// The client's own rule: an unknown id satisfies a delete, "the caller asked
// for that trip to be absent and it is". DEC-103 is the reason it matters —
// deletes are precisely what a client retries against a server that answered
// 404 for a route it did not have — so the second call must not 404 AND must
// not invalidate the phone's whole cached document by moving the version.
func TestARepeatedDeleteIsASuccessAndDoesNotMoveTheTag(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	first := h.do(t, http.MethodDelete, "/v1/trips/kyoto", "", token)
	if first.status != http.StatusOK {
		t.Fatalf("the first DELETE = %d %s", first.status, first.body)
	}
	again := h.do(t, http.MethodDelete, "/v1/trips/kyoto", "", token)
	if again.status != http.StatusOK {
		t.Errorf("the second DELETE = %d %s, want 200 — a delete asks for something to "+
			"be absent, and an absent thing satisfies it", again.status, again.body)
	}
	if got, want := again.header.Get("ETag"), first.header.Get("ETag"); got != want {
		t.Errorf("the repeated delete's ETag is %q and the first's was %q.\n"+
			"    Nothing changed, so the tag must not: a new tag on every retry\n"+
			"    throws away the phone's whole cached document for no write.", got, want)
	}
	if h.logbook.deleteCount() != 2 {
		t.Errorf("the store saw %d deletes, want 2 — this leg is worthless if the "+
			"handler short-circuited", h.logbook.deleteCount())
	}
}

// A TRIP THAT NEVER EXISTED IS THE SAME ANSWER, AND ON A LOG NOBODY HAS
// WRITTEN TO IT CARRIES NO ETag AT ALL.
//
// `tagFor` answers "" below version 1, because `FormatETag` panics on a zero
// half — a tag with one half is the defect DEC-49's first half exists to
// prevent. That branch is unreachable from `PUT /v1/trips/{id}`, which always
// bumps; it is reachable here, because a delete that removed nothing moves no
// version.
func TestDeletingATripFromALogNobodyHasWrittenCarriesNoTag(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.do(t, http.MethodDelete, "/v1/trips/never-existed", "", token)
	if got.status != http.StatusOK {
		t.Fatalf("DELETE of an unknown trip = %d %s, want 200", got.status, got.body)
	}
	if tag := got.header.Get("ETag"); tag != "" {
		t.Errorf("ETag = %q on a log at version 0, want none — W/\"2-0\" is exactly the "+
			"tag DEC-49 exists to prevent, and a 304 against it hands the client an "+
			"empty body it will treat as unchanged for ever", tag)
	}
}

// AND IT NEGOTIATES THE FORMAT, because what leaves here is a whole envelope.
// A client that can only read a version this build cannot write has to be told
// so; answering the current version regardless is DEC-40's refetch loop
// wearing a 200.
func TestDeletingATripRefusesAFormatThisBuildCannotWrite(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	got := h.doWithHeaders(t, http.MethodDelete, "/v1/trips/kyoto", "", token,
		map[string]string{formatHeader: "3"})
	if got.status != http.StatusNotAcceptable {
		t.Fatalf("DELETE at format 3 = %d %s, want 406", got.status, got.body)
	}
	if got := got.header.Get(formatHeader); got != "2" {
		t.Errorf("the 406 names %q as writable, want \"2\"", got)
	}
	if h.logbook.deleteCount() != 0 {
		t.Errorf("the store saw %d deletes on a request that was refused — the "+
			"negotiation has to happen BEFORE the cascade, not after it",
			h.logbook.deleteCount())
	}
}
