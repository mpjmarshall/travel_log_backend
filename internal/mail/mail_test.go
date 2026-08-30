package mail_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/logging"
	"travellog/internal/mail"
)

func TestTheSignInMessageCarriesTheCodeAndNothingElseSecret(t *testing.T) {
	m := mail.SignInCode("123456", 10*time.Minute)

	if !strings.Contains(m.Text, "123456") {
		t.Fatalf("the code is not in the body:\n%s", m.Text)
	}
	if m.Subject == "" {
		t.Fatal("no subject")
	}
	if strings.Contains(m.Subject, "123456") {
		t.Fatal("the code is in the SUBJECT, which shows on a lock screen")
	}
}

func TestTheMessageSaysHowLongTheCodeLasts(t *testing.T) {
	m := mail.SignInCode("123456", 10*time.Minute)
	if !strings.Contains(m.Text, "10 minutes") {
		t.Fatalf("a code with no stated lifetime is one nobody knows to hurry for:\n%s", m.Text)
	}
}

func TestTheMessageTellsAnUnexpectedRecipientToIgnoreIt(t *testing.T) {
	m := mail.SignInCode("123456", 10*time.Minute)
	if !strings.Contains(strings.ToLower(m.Text), "ignore") {
		t.Fatalf("somebody who did not ask for this needs to be told to do nothing:\n%s", m.Text)
	}
}

func TestTheLogSenderRefusesUnlessDevelopmentIsAskedForByName(t *testing.T) {
	if _, err := mail.NewLogSender(slog.Default(), false); err == nil {
		t.Fatal("a sender that writes credentials to the log was allowed by default")
	}
	if _, err := mail.NewLogSender(slog.Default(), true); err != nil {
		t.Fatalf("development was asked for by name and refused: %v", err)
	}
}

func TestTheLogSenderWritesTheCodeSoADeveloperCanSignIn(t *testing.T) {
	var buf bytes.Buffer
	sender, err := mail.NewLogSender(logging.New(&buf, slog.LevelInfo), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), "matt@example.com", mail.SignInCode("424242", time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "424242") {
		t.Fatalf("the whole point of this sender is that the code is readable:\n%s", buf.String())
	}
}

func TestSendingIsBoundedSoAWedgedProviderCannotHoldARequest(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	slow := mail.SenderFunc(func(ctx context.Context, _ string, _ mail.Message) error {
		select {
		case <-blocked:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	bounded := mail.WithTimeout(slow, 50*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		done <- bounded.Send(context.Background(), "matt@example.com", mail.SignInCode("1", time.Minute))
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a sender that never answers reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the bound did not fire: the send is still waiting")
	}
}

func TestDetachedSendingDoesNotDependOnWhetherTheAddressExists(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	slow := mail.SenderFunc(func(context.Context, string, mail.Message) error {
		defer wg.Done()
		time.Sleep(120 * time.Millisecond)
		return nil
	})

	detached := mail.Detached(slow, slog.New(slog.DiscardHandler))

	start := time.Now()
	err := detached.Send(context.Background(), "matt@example.com", mail.SignInCode("1", time.Minute))
	took := time.Since(start)

	if err != nil {
		t.Fatalf("a detached send reported a failure the caller cannot act on: %v", err)
	}
	if took > 50*time.Millisecond {
		t.Fatalf("the caller waited %v for the provider, which is the oracle", took)
	}
	wg.Wait()
}

func TestADetachedFailureIsLoggedRatherThanLost(t *testing.T) {
	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	failing := mail.SenderFunc(func(context.Context, string, mail.Message) error {
		defer wg.Done()
		return errors.New("the provider is down")
	})

	detached := mail.Detached(failing, logging.New(&buf, slog.LevelInfo))
	if err := detached.Send(context.Background(), "matt@example.com", mail.SignInCode("1", time.Minute)); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	time.Sleep(20 * time.Millisecond)

	if !strings.Contains(buf.String(), "the provider is down") {
		t.Fatalf("a send nobody is waiting on failed silently:\n%s", buf.String())
	}
}

func TestNoSenderEverLogsTheCode(t *testing.T) {
	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	failing := mail.SenderFunc(func(context.Context, string, mail.Message) error {
		defer wg.Done()
		return errors.New("boom")
	})
	detached := mail.Detached(failing, logging.New(&buf, slog.LevelInfo))
	_ = detached.Send(context.Background(), "matt@example.com", mail.SignInCode("998877", time.Minute))
	wg.Wait()
	time.Sleep(20 * time.Millisecond)

	if strings.Contains(buf.String(), "998877") {
		t.Fatalf("the code reached the log:\n%s", buf.String())
	}
}

func TestADetachedSendSurvivesTheCallersContextBeingCancelled(t *testing.T) {
	got := make(chan context.Context, 1)
	recorder := mail.SenderFunc(func(ctx context.Context, _ string, _ mail.Message) error {
		got <- ctx
		return nil
	})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	detached := mail.Detached(recorder, slog.New(slog.DiscardHandler))
	if err := detached.Send(cancelled, "matt@example.com", mail.SignInCode("1", time.Minute)); err != nil {
		t.Fatal(err)
	}

	select {
	case ctx := <-got:
		if err := ctx.Err(); err != nil {
			t.Fatalf("the send inherited a cancelled context: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the send never reached the provider at all")
	}
}
