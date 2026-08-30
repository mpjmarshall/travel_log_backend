// H1's three writes, over the real mux, the real middleware chain and the
// real auth, with a fake store.
package httpapi

import (
	"net/http"
	"testing"
)

// The critic called this leg exactly right at v1 and it is carried verbatim
// in substance.
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

// absent means leave alone, on the route the client actually calls this way.
func TestAShareWriteNamingOneSwitchLeavesTheOtherTwoAlone(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.put(t, "/v1/trips/kyoto", aTrip, token); got.status != http.StatusOK {
		t.Fatalf("PUT /v1/trips/kyoto = %d %s", got.status, got.body)
	}

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

// A body naming nothing is legal and writes nothing.
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

// The three share routes are setters and an unknown trip is a 404, which is
// the other half of the asymmetry D3's delete carries.
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

// The mint refuses A capability somebody can guess, and it names the field.
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
