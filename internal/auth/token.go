// Opaque session tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// TokenBytes is the 32.
const TokenBytes = 32

// ErrMalformedToken is a presented credential that is not the shape this
// package mints.
var ErrMalformedToken = errors.New("auth: that is not the shape of a session token")

// NewToken mints one.
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
func SameHash(a, b []byte) bool {
	if len(a) != sha256.Size || len(b) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
