// The share link: what a write to it may contain, and the one place a share
// token becomes a digest.
package logbook

import (
	"crypto/sha256"
	"fmt"
	"regexp"
)

// MinShareTokenBytes is the floor on a client-minted token, and it is an
// ENTROPY argument rather than a formatting one.
const MinShareTokenBytes = 12

// MaxShareTokenBytes is a DoS bound and this build's own policy, in the sense
// MaxNameBytes is.
const MaxShareTokenBytes = 64

// shareTokenPattern is the compiled regexp for a client-minted token.
var shareTokenPattern = regexp.MustCompile(
	fmt.Sprintf(`^[a-z0-9]{%d,%d}$`, MinShareTokenBytes, MaxShareTokenBytes))

// HashShareToken is sha256 of the token's own bytes.
func HashShareToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ShareWrite is the body of `PUT /v1/trips/{id}/share`: H1's three switches,
// Every field is a pointer because absent means leave alone.
type ShareWrite struct {
	SharePhotos      *bool `json:"sharePhotos"`
	ShareNotes       *bool `json:"shareNotes"`
	ShareCoordinates *bool `json:"shareCoordinates"`
}

// ShareMint is the body of `POST /v1/trips/{id}/share`: the token the CLIENT
// minted.
type ShareMint struct {
	Token *string `json:"token"`
}

// ValidateShareMint refuses a token that is not the shape of a capability.
func ValidateShareMint(m ShareMint) error {
	if m.Token == nil {
		return InvalidFieldError{Field: "token",
			Why: "a new link carries the token the client minted"}
	}
	if !shareTokenPattern.MatchString(*m.Token) {
		return InvalidFieldError{Field: "token",
			Why: "a share token is 12 to 64 characters of a-z and 0-9 — " +
				"anything shorter is a capability somebody can guess"}
	}
	return nil
}
