// H1's three writes, over the real mux, the real middleware chain and the real
// auth, with a fake store.
//
// THE FAILING TEST THIS STEP WAS WRITTEN AGAINST IS THE FIRST ONE BELOW, and
// it is the plan's own leg carried over rather than invented here: the critic
// called it at v1 and it is the one where a wrong answer is a PRIVACY LEAK
// rather than a bug. It is spelled in this repository's harness idiom —
// `newHarness`/`bearer`/`h.put` rather than the plan's sketch of
// `newAPI`/`registerAndSignIn`/`putJSON` — because a leg written against
// helpers that do not exist is a leg nobody runs.
//
// EXPECTED RED, and this is what it actually said before the routes existed:
// `PUT share = 404, want 200: 404 page not found`. The plan's sketch expected
// "the mux's 404 inside the envelope" and that is a fact about cmd/api rather
// than about this harness: `internal/httpx/mux.go` is what brings an unmatched
// route inside DEC-12's vocabulary, and it is wired in cmd/api. Here the
// server is `httpx.Chain(http.NewServeMux(), …)`, so an unmatched route is
// net/http's own plain-text 404. Recorded because a red quoted from a plan
// rather than from a run is the class this project keeps finding.
package httpapi

import (
	"net/http"
	"testing"
)

// THE CRITIC CALLED THIS LEG EXACTLY RIGHT AT v1 AND IT IS CARRIED VERBATIM IN
// SUBSTANCE: turn coordinates ON, stop sharing, mint a NEW link, and assert
// the new link does not carry coordinates. Removing the reset is a privacy
// leak, not a tidiness issue — the next link hands out exact pins without
// anybody having turned that on.
//
// AND THE RESET IS NOT "the column defaults". Migration 0002 moved the
// DEFAULTs to the client's true/true/false, and an UPDATE that does not name a
// column does not reach its default. The three values are asserted BY NAME.
//
// (The plan's own text overstated that as "a DEFAULT does not reach an
// UPDATE", which is false of `SET col = DEFAULT` — see the mutation table in
// CLAUDE.md, where the mutation the plan named goes GREEN and is replaced by
// one that can redden.)
func TestStoppingSharingDisarmsTheSwitchesForTheNextLink(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/kyoto = %d %s", got.status, got.body)
	}

	if r := h.put(t, "/v1/trips/kyoto/share", `{"shareCoordinates":true}`, token); r.status != 200 {
		t.Fatalf("PUT share = %d, want 200: %s", r.status, r.body)
	}

	if r := h.do(t, http.MethodDelete, "/v1/trips/kyoto/share", "", token); r.status != 200 {
		t.Fatalf("DELETE share = %d, want 200: %s", r.status, r.body)
	}

	minted := h.do(t, http.MethodPost, "/v1/trips/kyoto/share", `{"token":"mnpqrstuvwxy"}`, token)
	if minted.status != 201 {
		t.Fatalf("POST share = %d, want 201: %s", minted.status, minted.body)
	}

	trip := minted.decode(t)
	if trip["shareCoordinates"] != false {
		t.Errorf("shareCoordinates = %v on a NEW link after stopping, want false — "+
			"a killed link must not leave a coordinate switch armed for the next one",
			trip["shareCoordinates"])
	}
	// The other two go the OTHER way, and that asymmetry is the client's:
	// stopSharing writes Trip.defaultSharePhotos and defaultShareNotes, both true.
	if trip["sharePhotos"] != true {
		t.Errorf("sharePhotos = %v, want true — stopSharing resets to the client's "+
			"defaults, and a reset that touches only share_links leaves all three "+
			"where the user left them", trip["sharePhotos"])
	}
	if trip["shareNotes"] != true {
		t.Errorf("shareNotes = %v, want true", trip["shareNotes"])
	}
	if trip["shareLinkId"] != "mnpqrstuvwxy" {
		t.Errorf("shareLinkId = %v, want the token the client supplied", trip["shareLinkId"])
	}
	if trip["shared"] != true {
		t.Errorf("shared = %v after minting a link, want true — DEC-91's boolean is what "+
			"restores 'Stop sharing' on a device that does not hold the plaintext",
			trip["shared"])
	}
}

// ABSENT MEANS LEAVE ALONE, ON THE ROUTE THE CLIENT ACTUALLY CALLS THIS WAY
// (DEC-89). H1 flicks ONE switch at a time — every writing control on the
// screen goes inert while a write is in flight, because two changes inside one
// save are both computed from the state as it was and the second puts the
// first back — so a body naming one flag must not reset the other two.
func TestAShareWriteNamingOneSwitchLeavesTheOtherTwoAlone(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/kyoto = %d %s", got.status, got.body)
	}

	// Two flicks, one at a time, exactly as H1 makes them.
	if r := h.put(t, "/v1/trips/kyoto/share", `{"sharePhotos":false}`, token); r.status != 200 {
		t.Fatalf("PUT share = %d %s", r.status, r.body)
	}
	after := h.put(t, "/v1/trips/kyoto/share", `{"shareCoordinates":true}`, token)
	if after.status != 200 {
		t.Fatalf("the second PUT share = %d %s", after.status, after.body)
	}

	trip := after.decode(t)
	if trip["sharePhotos"] != false {
		t.Errorf("sharePhotos = %v after a body that did not name it, want the false the "+
			"user set. Absent means leave alone, and a whole-state write here means "+
			"every flick of one switch silently puts the other two back.",
			trip["sharePhotos"])
	}
	if trip["shareNotes"] != true {
		t.Errorf("shareNotes = %v, want true — it was never named by either body", trip["shareNotes"])
	}
	if trip["shareCoordinates"] != true {
		t.Errorf("shareCoordinates = %v, want true", trip["shareCoordinates"])
	}
}

// A BODY NAMING NOTHING IS LEGAL AND WRITES NOTHING. It is the same request as
// a re-send, and DEC-89's contract is that absence is not an error. The leg
// exists because the obvious alternative — 422 on an empty body — would refuse
// exactly the retry a client makes after a lost response.
func TestAShareWriteNamingNothingIsAcceptedAndChangesNothing(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/kyoto = %d %s", got.status, got.body)
	}
	if r := h.put(t, "/v1/trips/kyoto/share", `{"shareCoordinates":true}`, token); r.status != 200 {
		t.Fatalf("PUT share = %d %s", r.status, r.body)
	}

	got := h.put(t, "/v1/trips/kyoto/share", `{}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PUT share with an empty body = %d %s, want 200", got.status, got.body)
	}
	if trip := got.decode(t); trip["shareCoordinates"] != true {
		t.Errorf("shareCoordinates = %v, want the true that was already there", trip["shareCoordinates"])
	}
}

// THE THREE SHARE ROUTES ARE SETTERS AND AN UNKNOWN TRIP IS A 404, which is
// the OTHER half of the asymmetry D3's delete carries. The client's own
// sentence: "An unknown id is a failure here, where it is a success for a
// delete. A delete asks for something to be absent and an absent thing
// satisfies it; a set asks for a value the log then has to hold."
//
// `DELETE /v1/trips/{id}/share` IS ON THIS SIDE OF THE LINE DESPITE ITS VERB,
// and that is worth one assertion rather than an argument: it is
// `stopSharing`, which goes through the client's `_replaceTrip` and answers
// false for an unknown id, and its response is a whole Trip — which cannot be
// produced for a trip that is not there.
func TestTheShareRoutesAre404OnATripThatIsNotInTheLog(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	cases := []struct {
		method, body string
	}{
		{http.MethodPut, `{"sharePhotos":true}`},
		{http.MethodPost, `{"token":"mnpqrstuvwxy"}`},
		{http.MethodDelete, ""},
	}
	for _, c := range cases {
		got := h.do(t, c.method, "/v1/trips/never-existed/share", c.body, token)
		if got.status != http.StatusNotFound {
			t.Errorf("%s /v1/trips/never-existed/share = %d %s, want 404",
				c.method, got.status, got.body)
		}
		if code := got.decode(t)["code"]; code != "not_found" {
			t.Errorf("%s: code = %v, want not_found — `unsupported_route` is 'this build "+
				"does not have that route' and this build does", c.method, code)
		}
	}
}

// THE MINT REFUSES A CAPABILITY SOMEBODY CAN GUESS, and it names the field.
//
// The token is the CLIENT's — the server cannot mint it, because DEC-85 means
// the server can never hand a plaintext back on any later read — so this is
// the only place a short one can be stopped. Twelve characters of the client's
// 31-character alphabet is 59.5 bits; the schema's own check is `token <> ”`.
func TestMintingALinkRefusesAGuessableToken(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/kyoto = %d %s", got.status, got.body)
	}

	for _, body := range []string{`{}`, `{"token":""}`, `{"token":"short"}`, `{"token":"NOTLOWERCASE"}`} {
		got := h.do(t, http.MethodPost, "/v1/trips/kyoto/share", body, token)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("POST share %s = %d %s, want 422", body, got.status, got.body)
			continue
		}
		if field := got.decode(t)["field"]; field != "token" {
			t.Errorf("POST share %s: the 422 names %v, want \"token\"", body, field)
		}
	}
}
