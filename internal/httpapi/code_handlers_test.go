package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/auth"
	"travellog/internal/mail"
)

type sentMail struct {
	mu   sync.Mutex
	sent []struct {
		To   string
		Text string
	}
}

func (s *sentMail) Send(_ context.Context, to string, m mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, struct {
		To   string
		Text string
	}{to, m.Text})
	return nil
}

func (s *sentMail) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *sentMail) codeFor(t *testing.T, to string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.sent {
		if m.To != to {
			continue
		}
		for _, f := range strings.Fields(m.Text) {
			if len(f) == auth.CodeDigits && strings.Trim(f, "0123456789") == "" {
				return f
			}
		}
	}
	t.Fatalf("no code was mailed to %s", to)
	return ""
}

func TestAskingForACodeAnswers202WhoeverAsks(t *testing.T) {
	h := newHarness(t, options{})
	h.registerTraveller(t, "matt@example.com", "a long enough passphrase")

	known := h.post(t, "/v1/auth/code", `{"email":"matt@example.com"}`)
	unknown := h.post(t, "/v1/auth/code", `{"email":"nobody@example.com"}`)

	if known.status != http.StatusAccepted {
		t.Fatalf("a known address answered %d, want 202", known.status)
	}
	if unknown.status != known.status {
		t.Fatalf("unknown answered %d and known answered %d, which is an oracle",
			unknown.status, known.status)
	}
	if string(unknown.body) != string(known.body) {
		t.Fatalf("the bodies differ:\n  known:   %q\n  unknown: %q", known.body, unknown.body)
	}
}

func TestAMalformedAddressIsAFieldErrorAndNotAnOracle(t *testing.T) {
	h := newHarness(t, options{})
	got := h.post(t, "/v1/auth/code", `{"email":"not an address"}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", got.status)
	}
	var body struct{ Code, Field string }
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Field != "email" {
		t.Fatalf("field %q, want email", body.Field)
	}
}

func TestTheCodeIsMailedToTheAddressAndNotReturned(t *testing.T) {
	sent := &sentMail{}
	h := newHarness(t, options{mailer: sent})
	h.registerTraveller(t, "matt@example.com", "a long enough passphrase")

	got := h.post(t, "/v1/auth/code", `{"email":"matt@example.com"}`)

	if strings.Contains(string(got.body), `"code"`) {
		t.Fatalf("the response carries a code, so the mail is decoration: %s", got.body)
	}
	waitFor(t, func() bool { return sent.count() == 1 })
	if code := sent.codeFor(t, "matt@example.com"); len(code) != auth.CodeDigits {
		t.Fatalf("mailed %q", code)
	}
}

func TestNothingIsMailedToAnAddressNobodyHolds(t *testing.T) {
	sent := &sentMail{}
	h := newHarness(t, options{mailer: sent})
	h.post(t, "/v1/auth/code", `{"email":"nobody@example.com"}`)

	time.Sleep(150 * time.Millisecond)
	if n := sent.count(); n != 0 {
		t.Fatalf("%d mails went to an address with no log, which is a mail cannon", n)
	}
}

func TestTheMailedCodeSignsIn(t *testing.T) {
	sent := &sentMail{}
	h := newHarness(t, options{mailer: sent})
	h.registerTraveller(t, "matt@example.com", "a long enough passphrase")
	h.post(t, "/v1/auth/code", `{"email":"matt@example.com"}`)
	waitFor(t, func() bool { return sent.count() == 1 })
	code := sent.codeFor(t, "matt@example.com")

	got := h.post(t, "/v1/auth/session",
		`{"email":"matt@example.com","code":"`+code+`"}`)

	if got.status != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", got.status, got.body)
	}
	var body struct{ Token string }
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" {
		t.Fatal("no token")
	}
}

func TestAWrongCodeIs401AndSaysNothingMore(t *testing.T) {
	h := newHarness(t, options{})
	h.registerTraveller(t, "matt@example.com", "a long enough passphrase")
	h.post(t, "/v1/auth/code", `{"email":"matt@example.com"}`)

	wrong := h.post(t, "/v1/auth/session", `{"email":"matt@example.com","code":"000000"}`)
	unknown := h.post(t, "/v1/auth/session", `{"email":"nobody@example.com","code":"000000"}`)

	if wrong.status != http.StatusUnauthorized {
		t.Fatalf("a wrong code answered %d, want 401", wrong.status)
	}
	if unknown.status != wrong.status || string(unknown.body) != string(wrong.body) {
		t.Fatalf("a wrong code and an unknown address differ:\n  %d %q\n  %d %q",
			wrong.status, wrong.body, unknown.status, unknown.body)
	}
}

func TestTheCodeNeverReachesTheLog(t *testing.T) {
	sent := &sentMail{}
	h := newHarness(t, options{mailer: sent})
	h.registerTraveller(t, "matt@example.com", "a long enough passphrase")
	h.post(t, "/v1/auth/code", `{"email":"matt@example.com"}`)
	waitFor(t, func() bool { return sent.count() == 1 })
	code := sent.codeFor(t, "matt@example.com")
	h.post(t, "/v1/auth/session", `{"email":"matt@example.com","code":"`+code+`"}`)

	if strings.Contains(h.logs.String(), code) {
		t.Fatalf("the sign-in code is in the log:\n%s", h.logs.String())
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition never became true")
}
