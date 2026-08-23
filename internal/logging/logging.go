// Package logging builds the application's logger.
//
// go_backend.md L25: "Use the standard `log/slog` package for structured JSON
// logging." The whole package is that one sentence plus a redactor, and the
// redactor is what makes it safe to point a structured logger at a credential.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted is exported so a test asserts on the same constant the handler
// writes. A test carrying its own copy of the string passes when the handler's
// changes.
const Redacted = "[redacted]"

// secretKeys is the closed list VS2's step text names: token, passphrase,
// authorization.
//
// "password" is deliberately NOT here, and the absence is a decision rather
// than an oversight: DEC-61 settles the field's name as `passphrase`, and this
// project has no `password` anywhere. If one ever appears, it is one entry and
// one row in TestRedactsTheThreeNamedKeys — the cost of adding a fourth is
// exactly the cost of noticing you need it.
var secretKeys = []string{"token", "passphrase", "authorization"}

// New returns a JSON logger at `level` with the redactor installed.
//
// The writer is a parameter rather than a hardwired os.Stdout because a test
// that cannot read what was written cannot assert a secret is absent from it,
// and "the secret is absent" is the only claim this package makes.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redact,
	}))
}

// redact replaces the value of any attribute whose key names a credential.
//
// THREE PROPERTIES, EACH WITH ITS OWN LEG, AND EACH ONE A WAY A NAIVE REDACTOR
// LEAKS:
//
//   - It matches a SUBSTRING of the key, lowercased. `access_token`,
//     `sessionToken` and `authorization_header` are the spellings a call site
//     actually produces; a redactor that only knows the exact word `token`
//     fires on the one name nobody types. The cost of the wider net is a
//     redacted `tokenizer_config`, which is nothing.
//
//   - It replaces the WHOLE VALUE rather than rewriting a string, so
//     slog.Any("token", someStruct) is replaced entire. A redactor that only
//     handled slog.KindString would have written that struct out in full —
//     measured, and the JSON handler even base64s a []byte field on the way,
//     so the secret leaves in two encodings.
//
//   - It is installed as ReplaceAttr, which is the only hook slog applies to
//     ALL THREE paths an attribute can take: a plain call argument, a member of
//     a Group, and an attribute pre-formatted by With. The third is the one
//     that matters in practice, because a request-scoped logger is built with
//     With, and it is where a token would actually travel.
//
// The decision is made on the KEY and never on the value. A redactor that
// sniffed values would pass every leak test by replacing everything, which logs
// nothing and looks safe.
func redact(_ []string, a slog.Attr) slog.Attr {
	// slog does not call ReplaceAttr on a group attribute itself, only on its
	// contents. Returning early is defensive, and it keeps a group named
	// "authorization" from collapsing into a string and taking its members
	// with it.
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
