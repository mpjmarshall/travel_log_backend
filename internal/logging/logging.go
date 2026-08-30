// Package logging builds the application's logger.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted is exported so a test asserts on the same constant the handler
// writes.
const Redacted = "[redacted]"

// secretKeys is a closed list: token, passphrase, authorization.
var secretKeys = []string{"token", "passphrase", "authorization"}

// New returns a JSON logger at `level` with the redactor installed.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redact,
	}))
}

// redact replaces the value of any attribute whose key names a credential.
func redact(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		return a
	}

	key := strings.ToLower(a.Key)
	for _, secret := range secretKeys {
		if strings.Contains(key, secret) {
			return slog.String(a.Key, Redacted)
		}
	}
	return a
}
