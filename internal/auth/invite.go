package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
)

const InviteBytes = 10

// ErrInviteSpent is an invite that is used, unknown or misspelt: one sentinel,
// because telling them apart says which codes exist.
var ErrInviteSpent = errors.New("auth: that invite cannot be used")

var inviteAlphabet = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewInvite mints one, answering the plaintext to hand out and the digest for
// the column.
func NewInvite() (plaintext string, hash []byte, err error) {
	raw := make([]byte, InviteBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: drawing an invite: %w", err)
	}
	plaintext = inviteAlphabet.EncodeToString(raw)
	return plaintext, HashInvite(plaintext), nil
}

// HashInvite turns a presented invite into the digest a row is found by, case
// and hyphens normalised because it is typed by hand.
func HashInvite(plaintext string) []byte {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(plaintext), "-", ""))
	sum := sha256.Sum256([]byte(clean))
	return sum[:]
}
