// Opaque session tokens (spec L24): 32 bytes from crypto/rand, base64url on
// the wire, SHA-256 of the RAW bytes stored, compared with
// crypto/subtle.ConstantTimeCompare.
//
// THE PLAINTEXT IS RETURNED ONCE AND IS NEVER STORED, LOGGED OR RECOVERABLE.
// NewToken is the only place it exists; what leaves this package for the
// database is the digest. internal/logging's redactor is the second half of
// that promise — it replaces any attribute whose key contains "token" — and
// the two are independent, so neither is load-bearing alone.
//
// WHY THE HASH IS OF THE RAW BYTES AND NOT OF THE BASE64 TEXT. Both are 32
// bytes, both round-trip, both are unique per token, and sign-in works either
// way: there is no observable difference inside this system at all. The
// difference is that only one of them is what the spec says, and the day
// somebody re-implements either half against L24 the two stop agreeing with
// no failure anywhere to explain it. token_test.go asserts the negative as
// well as the positive for that reason.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// TokenBytes is spec L24's 32. It is the entropy, not the wire length: the
// wire is 43 characters of unpadded base64url.
const TokenBytes = 32

// ErrMalformedToken is a presented credential that is not the shape this
// package mints. It is deliberately indistinguishable, to a caller, from a
// well-formed token nobody holds — see Service.Authenticate.
var ErrMalformedToken = errors.New("auth: that is not the shape of a session token")

// NewToken mints one. It answers the plaintext for the response body and the
// digest for the `sessions.token_hash` column, and never both again.
func NewToken() (plaintext string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: drawing %d bytes for a session token: %w", TokenBytes, err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// HashToken turns a presented plaintext back into the digest a row is found
// by.
//
// THE LENGTH IS CHECKED HERE OR IT IS CHECKED NOWHERE. A one-byte token
// base64-decodes cleanly, hashes to a perfectly good 32-byte digest, satisfies
// sessions_token_hash_sha256_ck and compares without complaint — so nothing
// downstream can tell a truncated credential from a whole one. Refusing the
// wrong length is not defence in depth; it is the only place the wire shape
// exists as a rule.
func HashToken(plaintext string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil {
		return nil, ErrMalformedToken
	}
	if len(raw) != TokenBytes {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrMalformedToken, len(raw), TokenBytes)
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// SameHash compares two digests in constant time.
//
// THE LENGTH GUARD IS NOT REDUNDANT WITH ConstantTimeCompare'S OWN. That
// function answers 1 for two EMPTY slices — measured, and it is the documented
// behaviour of an equal-length comparison of nothing — so without this, a nil
// digest from a caller that forgot to check its error would authenticate
// against a nil column read.
func SameHash(a, b []byte) bool {
	if len(a) != sha256.Size || len(b) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
