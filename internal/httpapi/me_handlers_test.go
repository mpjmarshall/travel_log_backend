// U1's pencil and the one revocation surface, over the real mux, the real
// middleware chain and the real auth.
package httpapi

import (
	"net/http"
	"testing"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
)

// PATCH /v1/me ANSWERS THE TRAVELLER AND A TAG.
//
// It answers a whole object rather than a bare string because the phone
// splices it into the `traveller` slot of its cached document, exactly as
// DEC-32's Trip goes into `trips`. And the tag moves, because the name is IN
// that document — a write that left the ETag alone would leave every phone
// answering 304 to a log whose owner has been renamed.
func TestNamingTheTravellerAnswersTheTravellerAndMovesTheTag(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	before := h.get(t, "/v1/logbook", token, nil).header.Get("ETag")

	got := h.do(t, http.MethodPatch, "/v1/me", `{"name":"Matt"}`, token)
	if got.status != http.StatusOK {
		t.Fatalf("PATCH /v1/me = %d %s, want 200", got.status, got.body)
	}
	if name := got.decode(t)["name"]; name != "Matt" {
		t.Errorf("name = %v, want \"Matt\"", name)
	}
	if tag := got.header.Get("ETag"); tag == "" || tag == before {
		t.Errorf("the write's ETag is %q and the read's was %q — the traveller's name is "+
			"the sixth key of the emitted document, so a rename that does not move "+
			"the tag is a rename no phone ever sees", tag, before)
	}

	// AND THE WHOLE LOG AGREES WITH THE WRITE. The splice the client makes is
	// only safe if the next full read says the same thing.
	body := h.get(t, "/v1/logbook", token, nil).decode(t)
	inner := body["logbook"].(map[string]any)
	traveller, held := inner["traveller"].(map[string]any)
	if !held || traveller["name"] != "Matt" {
		t.Errorf("the whole log's traveller = %#v, want {name: Matt}", inner["traveller"])
	}
}

// AN EMPTY NAME IS 422 AND DOES NOT CLEAR IT, matching the client exactly.
//
// `setTravellerName` returns false on a trimmed-empty name, and the client's
// own reason is the one that decides the server's answer too: "a log with an
// owner keeps one, and 'no traveller' is a state a log arrives in and never
// returns to". So there is no way to clear a traveller's name over this API,
// deliberately.
//
// THE SECOND HALF IS THE ONE THAT MATTERS. A 422 that had already written NULL
// would be a refusal in the response and a clear in the database.
func TestAnEmptyNameIsRefusedAndDoesNotClearTheOne(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)
	if got := h.do(t, http.MethodPatch, "/v1/me", `{"name":"Matt"}`, token); got.status != http.StatusOK {
		t.Fatalf("the first PATCH = %d %s", got.status, got.body)
	}

	for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{}`} {
		got := h.do(t, http.MethodPatch, "/v1/me", body, token)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("PATCH /v1/me %s = %d %s, want 422", body, got.status, got.body)
			continue
		}
		if field := got.decode(t)["field"]; field != "name" {
			t.Errorf("PATCH /v1/me %s: the 422 names %v, want \"name\"", body, field)
		}
	}

	log := h.get(t, "/v1/logbook", token, nil).decode(t)
	inner := log["logbook"].(map[string]any)
	traveller, held := inner["traveller"].(map[string]any)
	if !held || traveller["name"] != "Matt" {
		t.Errorf("the traveller reads %#v after three refused writes, want {name: Matt} — "+
			"a refusal that has already written NULL is a refusal in the response and "+
			"a clear in the database", inner["traveller"])
	}
}

// GET /v1/me IS DELETED AND STAYS DELETED (OE-7).
//
// A leg rather than an absence, because a route with no caller is exactly the
// kind of thing that gets added back by somebody who assumes PATCH implies
// GET. The name is in `GET /v1/logbook`'s `traveller` object, the client's
// Traveller type has one field and no id, and register already answers 201
// with the traveller.
func TestThereIsNoGetOnMe(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.get(t, "/v1/me", token, nil); got.status != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/me = %d %s, want 405 — the only traveller field the client "+
			"renders arrives inside GET /v1/logbook, and a second definition of the "+
			"traveller's wire shape is the thing that drifts", got.status, got.body)
	}
}

// === the revocation surface ===

// A REVOKED TOKEN IS A 401 ON THE NEXT REQUEST, which is the whole of what the
// route is for.
//
// Against a thirty-day untuned TTL this is the only bound on a token that has
// left the device. The leg is the pair — the token works, then it does not —
// because either half alone passes against a build that answers 204 and does
// nothing.
func TestRevokingASessionMakesTheNextRequestA401(t *testing.T) {
	h := newHarness(t, options{})
	token := bearer(t, h)

	if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusOK {
		t.Fatalf("the premise failed: GET /v1/logbook = %d %s", got.status, got.body)
	}

	revoked := h.do(t, http.MethodDelete, "/v1/auth/session", "", token)
	if revoked.status != http.StatusNoContent {
		t.Fatalf("DELETE /v1/auth/session = %d %s, want 204", revoked.status, revoked.body)
	}
	if len(revoked.body) != 0 {
		t.Errorf("the 204 carries %d bytes: %q", len(revoked.body), revoked.body)
	}

	after := h.get(t, "/v1/logbook", token, nil)
	if after.status != http.StatusUnauthorized {
		t.Errorf("the revoked token still reads the log: %d %s", after.status, after.body)
	}
	if string(after.body) != `{"code":"unauthenticated"}` {
		t.Errorf("body = %s, want {\"code\":\"unauthenticated\"}", after.body)
	}
}

// `?scope=all` REVOKES THE OTHER DEVICE TOO, AND THE CALLER'S OWN TOKEN WITH
// IT.
//
// The security lens's argument: a stolen token is precisely the case where you
// do not know which row to delete, and 'this token' is the one the thief will
// not use. THE TWO-SESSION SETUP IS THE POINT — with one session, `all` and
// `this` are indistinguishable, so a leg written on one token proves nothing
// about the parameter.
//
// AND IT SIGNS THE CALLER OUT TOO, deliberately: "sign out everywhere" that
// leaves the device you pressed it on signed in is a control that has not done
// what it says.
func TestRevokingEverySessionReachesTheOtherDeviceAndThisOne(t *testing.T) {
	h := newHarness(t, options{})
	phone := bearer(t, h)
	tablet := signInAs(t, h, "matt@example.com")
	if phone == tablet {
		t.Fatalf("the two sign-ins answered one token, so this leg cannot see the difference")
	}

	if got := h.get(t, "/v1/logbook", tablet, nil); got.status != http.StatusOK {
		t.Fatalf("the premise failed: the tablet cannot read the log: %d", got.status)
	}

	if got := h.do(t, http.MethodDelete, "/v1/auth/session?scope=all", "", phone); got.status != http.StatusNoContent {
		t.Fatalf("DELETE ?scope=all = %d %s, want 204", got.status, got.body)
	}

	if got := h.get(t, "/v1/logbook", tablet, nil); got.status != http.StatusUnauthorized {
		t.Errorf("the OTHER device still reads the log after ?scope=all: %d %s.\n"+
			"    This is the only recovery a user has against a stolen token, and the\n"+
			"    stolen one is precisely the row nobody can name.", got.status, got.body)
	}
	if got := h.get(t, "/v1/logbook", phone, nil); got.status != http.StatusUnauthorized {
		t.Errorf("the CALLER's own token still works after ?scope=all: %d %s — "+
			"'sign out everywhere' that leaves this device signed in has not done "+
			"what it says", got.status, got.body)
	}
}

// AND `?scope=this` LEAVES THE OTHER DEVICE ALONE, which is the other
// direction and the one that makes the parameter mean anything. Without it,
// `all` could be the only behaviour and every assertion above would still
// pass.
func TestTheDefaultScopeLeavesTheOtherDeviceSignedIn(t *testing.T) {
	h := newHarness(t, options{})
	phone := bearer(t, h)
	tablet := signInAs(t, h, "matt@example.com")

	if got := h.do(t, http.MethodDelete, "/v1/auth/session", "", phone); got.status != http.StatusNoContent {
		t.Fatalf("DELETE with no scope = %d %s", got.status, got.body)
	}
	if got := h.get(t, "/v1/logbook", tablet, nil); got.status != http.StatusOK {
		t.Errorf("the other device was signed out by a request that named no scope: %d %s.\n"+
			"    The path is singular and the default is the SMALLER act.",
			got.status, got.body)
	}
}

// A SCOPE NOBODY RECOGNISES IS A 422 NAMING THE FIELD, NOT A FALLBACK.
//
// `?scope=al` — a typo — must not quietly sign one device out while the user
// believes every device is out. That is the one failure mode this parameter
// has, and it is the whole reason an unknown value is refused rather than
// defaulted.
func TestAnUnknownRevocationScopeIsRefusedRatherThanDefaulted(t *testing.T) {
	h := newHarness(t, options{})
	phone := bearer(t, h)
	tablet := signInAs(t, h, "matt@example.com")

	got := h.do(t, http.MethodDelete, "/v1/auth/session?scope=al", "", phone)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE ?scope=al = %d %s, want 422", got.status, got.body)
	}
	if field := got.decode(t)["field"]; field != "scope" {
		t.Errorf("the 422 names %v, want \"scope\"", field)
	}
	// AND IT REVOKED NOTHING. A 422 that had already signed one device out is
	// a refusal in the response and an act in the database.
	for name, token := range map[string]string{"the caller": phone, "the other device": tablet} {
		if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusOK {
			t.Errorf("%s was signed out by a refused request: %d %s", name, got.status, got.body)
		}
	}
}

// THE REVOCATION MOVES NO logbook_version, and that is DEC-50's list rather
// than an accident: a session is not in the emitted document, so revoking one
// must not invalidate the phone's whole cached log.
//
// It is asserted through the TAG, which is the only thing the phone can see.
func TestRevokingASessionDoesNotInvalidateTheCachedLog(t *testing.T) {
	h := newHarness(t, options{})
	phone := bearer(t, h)
	tablet := signInAs(t, h, "matt@example.com")
	h.logbook.version = 7

	before := h.get(t, "/v1/logbook", tablet, nil).header.Get("ETag")
	if before != httpx.FormatETag(logbook.EmitterVersion, 7) {
		t.Fatalf("the fixture is wrong: the tag reads %q", before)
	}

	if got := h.do(t, http.MethodDelete, "/v1/auth/session", "", phone); got.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", got.status, got.body)
	}

	after := h.get(t, "/v1/logbook", tablet, nil).header.Get("ETag")
	if after != before {
		t.Errorf("the tag moved from %q to %q when a session was revoked.\n"+
			"    A session is not in the emitted document; counting one invalidates\n"+
			"    the phone's whole cached log for a write it cannot see.", before, after)
	}
}
