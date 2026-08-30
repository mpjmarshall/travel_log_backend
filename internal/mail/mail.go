// Sending a sign-in code. Nothing above this package knows which provider.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Message is one mail. Text only: a sign-in code needs no layout.
type Message struct {
	Subject string
	Text    string
}

// Sender delivers a Message, safely from several goroutines.
type Sender interface {
	Send(ctx context.Context, to string, m Message) error
}

// SenderFunc adapts a function to Sender.
type SenderFunc func(ctx context.Context, to string, m Message) error

func (f SenderFunc) Send(ctx context.Context, to string, m Message) error {
	return f(ctx, to, m)
}

// SignInCode is the message a traveller receives. The code is in the body and
// never the subject, because a subject shows on a lock screen.
func SignInCode(code string, ttl time.Duration) Message {
	return Message{
		Subject: "Your Travel Log sign-in code",
		Text: fmt.Sprintf(
			"Your sign-in code is %s\n\n"+
				"It works once and lasts %s.\n\n"+
				"If you did not ask to sign in, ignore this message. "+
				"Nobody can use the code without it.\n",
			code, humanise(ttl),
		),
	}
}

// humanise writes a duration the way a sentence would.
func humanise(d time.Duration) string {
	if m := int(d.Minutes()); m >= 1 {
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	}
	return d.String()
}

// ErrDevelopmentOnly refuses a sender that writes credentials to the log.
var ErrDevelopmentOnly = errors.New("mail: the log sender writes sign-in codes to the log and must be asked for by name")

// NewLogSender writes the code to the log so a developer can sign in with no
// provider. It refuses unless development is asked for by name.
func NewLogSender(log *slog.Logger, development bool) (Sender, error) {
	if !development {
		return nil, ErrDevelopmentOnly
	}
	return SenderFunc(func(_ context.Context, to string, m Message) error {
		log.Warn("mail: not sent, printed",
			slog.String("to", to),
			slog.String("body", m.Text),
		)
		return nil
	}), nil
}

// WithTimeout bounds a send, so a provider that never answers cannot hold the
// caller open. Here rather than at the call site, which is what forgets.
func WithTimeout(next Sender, d time.Duration) Sender {
	return SenderFunc(func(ctx context.Context, to string, m Message) error {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return next.Send(ctx, to, m)
	})
}

// Detached sends without making the caller wait, so delivery time cannot say
// whether an address exists. It does not inherit the request's context.
func Detached(next Sender, log *slog.Logger) Sender {
	return SenderFunc(func(_ context.Context, to string, m Message) error {
		go func() {
			if err := next.Send(context.WithoutCancel(context.Background()), to, m); err != nil {
				log.Error("mail: a detached send failed",
					slog.String("to", to),
					slog.String("err", err.Error()),
				)
			}
		}()
		return nil
	})
}
