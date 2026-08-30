// The AUTHENTICATED budget, which is a different thing from the credential
// budget and is keyed on a different fact about the request.
package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The leg the defect had no answer for: an authenticated route ran with no
// limiter whatsoever, so this loop never saw a 429 however long it ran.
func TestAnAuthenticatedRouteRunsOutOfItsOwnAllowance(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 3})
	token := bearer(t, h)

	for i := range 3 {
		if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusOK {
			t.Fatalf("request %d of an allowance of 3 = %d %s", i+1, got.status, got.body)
		}
	}

	refused := h.get(t, "/v1/logbook", token, nil)
	if refused.status != http.StatusTooManyRequests {
		t.Errorf("the 4th authenticated request at a limit of 3 a minute = %d %s, want 429.\n"+
			"    An authenticated route with no limiter is an unlimited whole-log read\n"+
			"    for anybody holding a token, against a 30-day TTL and no revocation.",
			refused.status, refused.body)
	}
	if got := string(refused.body); got != `{"code":"rate_limited"}` {
		t.Errorf("body = %s, want {\"code\":\"rate_limited\"}", got)
	}
}

// two travellers, one address.
func TestOneTravellerRunningOutDoesNotRefuseAnotherAtTheSameAddress(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 3})
	first := bearerFor(t, h, "matt@example.com")
	second := aSecondTravellerBehindTheClosedRoute(t, h, "kit@example.com")

	spent := 0
	for range 4 {
		if h.get(t, "/v1/logbook", first, nil).status == http.StatusOK {
			spent++
		}
	}
	if spent != 3 {
		t.Fatalf("the first traveller was served %d of an allowance of 3", spent)
	}
	if got := h.get(t, "/v1/logbook", first, nil); got.status != http.StatusTooManyRequests {
		t.Fatalf("the first traveller is not out of allowance: %d %s", got.status, got.body)
	}

	if got := h.get(t, "/v1/logbook", second, nil); got.status != http.StatusOK {
		t.Errorf("a second traveller at the same address = %d %s, want 200.\n"+
			"    The budget is keyed on the traveller, so one traveller spending theirs\n"+
			"    cannot refuse another — which is what a limiter keyed on the client\n"+
			"    address would do to every traveller behind one NAT.",
			got.status, got.body)
	}

	if seen := h.addrs.distinct(); len(seen) != 1 {
		t.Errorf("the two travellers reached the server from %d addresses (%v);\n"+
			"    this leg only distinguishes per-traveller keying from per-address\n"+
			"    keying while there is exactly one address in play", len(seen), seen)
	}
}

// The other half of the keying claim, and the mutation it kills is a
// plausible one.
func TestOneTravellersTwoSessionsShareOneAllowance(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 3})
	phone := bearerFor(t, h, "matt@example.com")
	tablet := signInAs(t, h, "matt@example.com")

	if phone == tablet {
		t.Fatalf("the two sign-ins answered the same token, so this leg cannot see the difference")
	}

	for i := range 3 {
		if got := h.get(t, "/v1/logbook", phone, nil); got.status != http.StatusOK {
			t.Fatalf("request %d on the first session = %d %s", i+1, got.status, got.body)
		}
	}

	if got := h.get(t, "/v1/logbook", tablet, nil); got.status != http.StatusTooManyRequests {
		t.Errorf("a second session for the SAME traveller = %d %s, want 429.\n"+
			"    A budget keyed on the token rather than on the traveller is bought off\n"+
			"    with a second sign-in, which anybody holding the passphrase can do.",
			got.status, got.body)
	}
}

// A refused request must not reach the handler — the whole point of a ceiling
// on a stolen token is the work that does not happen.
func TestARefusedAuthenticatedRequestNeverReachesTheHandler(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 2})
	token := bearer(t, h)

	for range 2 {
		h.get(t, "/v1/logbook", token, nil)
	}
	served := h.logbook.assembleCount()

	for range 5 {
		if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusTooManyRequests {
			t.Fatalf("a request past the allowance = %d %s, want 429", got.status, got.body)
		}
	}

	if after := h.logbook.assembleCount(); after != served {
		t.Errorf("the store assembled %d documents for 5 refused requests, want 0.\n"+
			"    A limiter that refuses AFTER the work is a limiter that bounds nothing.",
			after-served)
	}
}

// the limiter sits inside the authentication, and this is the leg that says
// so.
func TestAnUnauthenticatedFloodDoesNotSpendTheTravellersAllowance(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 3})
	token := bearer(t, h)

	for range 20 {
		if got := h.get(t, "/v1/logbook", "", nil); got.status != http.StatusUnauthorized {
			t.Fatalf("a request with no credential = %d %s, want 401", got.status, got.body)
		}
	}

	served := 0
	for range 3 {
		if h.get(t, "/v1/logbook", token, nil).status == http.StatusOK {
			served++
		}
	}
	if served != 3 {
		t.Errorf("after 20 unauthenticated requests the traveller was served %d of an\n"+
			"    allowance of 3. A flood nobody had a credential for spent somebody\n"+
			"    else's budget, which is a denial of service handed out for free.", served)
	}
}

// The refusal goes to the log with the traveller on it.
func TestTheTravellerLimitLogsTheTravellerAndNeverTheToken(t *testing.T) {
	h := newHarness(t, options{travellerPerMin: 1})
	token := bearerFor(t, h, "matt@example.com")

	h.get(t, "/v1/logbook", token, nil)
	if got := h.get(t, "/v1/logbook", token, nil); got.status != http.StatusTooManyRequests {
		t.Fatalf("the 2nd request at a limit of 1 = %d %s, want 429", got.status, got.body)
	}

	logs := h.logs.String()
	if !strings.Contains(logs, h.travellerID(t, token)) {
		t.Errorf("no log line names the traveller whose allowance ran out:\n%s", logs)
	}
	if raw := strings.TrimPrefix(token, "Bearer "); strings.Contains(logs, raw) {
		t.Errorf("the session token reached the log")
	}
}

// aSecondTravellerBehindTheClosedRoute puts a traveller into the store
// DIRECTLY and signs them in through the real routes.
func aSecondTravellerBehindTheClosedRoute(t *testing.T, h *harness, email string) string {
	t.Helper()
	hash, err := cheapArgon.Hash("a long enough passphrase")
	if err != nil {
		t.Fatalf("hashing the second traveller's passphrase: %v", err)
	}
	if _, err := h.store.CreateTraveller(context.Background(), email, hash); err != nil {
		t.Fatalf("putting %s into the store directly: %v", email, err)
	}
	if got := h.post(t, "/v1/auth/register",
		credentialsFor("a-third@example.com")); got.status != http.StatusConflict {
		t.Fatalf("the premise of this helper failed: registration answered %d rather "+
			"than 409, so the route is not closed and the helper is unnecessary",
			got.status)
	}
	return signInAs(t, h, email)
}
