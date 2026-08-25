// The share token's two halves: the digest that replaces the plaintext
// (DEC-85), and the shape a client-minted capability has to have.
//
// NONE OF THIS NEEDS A DATABASE, and that is the point of it being here rather
// than only in internal/postgres: the schema legs prove the COLUMN is a bytea
// and the backfill hashed what was there, and only these can say WHICH digest
// — that it is of the token's own bytes, with no decode step, which is the one
// way this function differs from `auth.HashToken` and the one way it could be
// written wrongly with every other leg still green.
package logbook_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// THE DIGEST IS OF THE BYTES AS TYPED, AND THE NEGATIVE IS WHAT MAKES THIS A
// MEASUREMENT. `auth.HashToken` base64-DECODES first and hashes the raw bytes;
// hashing a share token that way would still produce 32 bytes, still satisfy
// share_links_token_hash_sha256_ck, still compare without complaint, and
// resolve a DIFFERENT token — so the positive assertion alone cannot see the
// mistake. token_test.go asserts its own negative for the same reason.
func TestAShareTokenIsHashedAsItsOwnBytesAndNotAsBase64(t *testing.T) {
	// Twelve characters of the client's own alphabet, which is what
	// `newShareLinkId()` actually mints.
	const token = "kyotomay9f2a"

	want := sha256.Sum256([]byte(token))
	if got := logbook.HashShareToken(token); !bytes.Equal(got, want[:]) {
		t.Errorf("logbook.HashShareToken(%q) = %x, want the sha256 of its own bytes, %x",
			token, got, want)
	}

	// The other spelling, and it must NOT be what this produces. base64 will
	// decode this string happily — every character is in the alphabet — and
	// the digest of those decoded bytes is a perfectly good 32 bytes that
	// names a different capability.
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("the premise of this leg failed: %q does not base64-decode, so "+
			"the wrong implementation would have errored rather than silently "+
			"hashing something else: %v", token, err)
	}
	wrong := sha256.Sum256(decoded)
	if bytes.Equal(logbook.HashShareToken(token), wrong[:]) {
		t.Errorf("HashShareToken hashed the BASE64-DECODED bytes of %q. A share "+
			"token is minted by the client and is arbitrary text; decoding it "+
			"first resolves a different row and refuses most real tokens outright.",
			token)
	}
}

func TestTheDigestIsThirtyTwoBytesForAnyInput(t *testing.T) {
	for _, token := range []string{"", "a", "kyotomay9f2a", strings.Repeat("z", 4096)} {
		if got := len(logbook.HashShareToken(token)); got != sha256.Size {
			t.Errorf("logbook.HashShareToken(%d characters) is %d bytes, want %d — "+
				"share_links_token_hash_sha256_ck refuses anything else",
				len(token), got, sha256.Size)
		}
	}
}

func TestValidateShareMintRefusesAGuessableCapability(t *testing.T) {
	cases := []struct {
		name  string
		token *string
		want  string // "" means accepted
	}{
		{"the client's own shape", ptr("kyotomay9f2a"), ""},
		{"the fixture's captured token", ptr("kyoto9f2ammm"), ""},
		{"sixty-four characters", ptr(strings.Repeat("a", 64)), ""},
		{"absent", nil, "token"},
		{"empty", ptr(""), "token"},
		{"eleven characters", ptr("kyotomay9f2"), "token"},
		{"sixty-five characters", ptr(strings.Repeat("a", 65)), "token"},
		{"upper case", ptr("KYOTOMAY9F2A"), "token"},
		{"a hyphen, which an id may hold and a token may not", ptr("kyoto-9f2ab"), "token"},
		{"a slash, which would end up in a URL path", ptr("kyoto/may9f2a"), "token"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := logbook.ValidateShareMint(logbook.ShareMint{Token: c.token})
			if c.want == "" {
				if err != nil {
					t.Fatalf("ValidateShareMint = %v, want it accepted", err)
				}
				return
			}
			var invalid logbook.InvalidFieldError
			if !errors.As(err, &invalid) {
				t.Fatalf("ValidateShareMint = %v, want an InvalidFieldError naming %q", err, c.want)
			}
			if invalid.Field != c.want {
				t.Errorf("the refusal names %q, want %q", invalid.Field, c.want)
			}
		})
	}
}
