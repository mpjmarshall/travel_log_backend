// Opaque session tokens, spec L24, test-first.
//
// L24: "Session tokens are opaque: 32 bytes from crypto/rand, base64url on the
// wire, SHA-256 of the RAW bytes stored, compared with
// crypto/subtle.ConstantTimeCompare."
//
// THE WORD "RAW" IS THE WHOLE OF THIS FILE. Hashing the base64 TEXT instead of
// the bytes it encodes produces a hash that is stable, 32 bytes long, unique
// per token and wrong — every leg but one below stays green under that
// mutation, the column CHECK still passes, and sign-in still works. It is a
// defect nothing observable would ever reveal, and it only matters the day
// somebody re-implements the client half against the spec's sentence.
package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestNewTokenIsThirtyTwoBytesOnTheWireAsUnpaddedBase64URL(t *testing.T) {
	plaintext, _, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil {
		t.Fatalf("the plaintext %q does not decode as unpadded base64url: %v", plaintext, err)
	}
	if len(raw) != TokenBytes {
		t.Errorf("the plaintext decodes to %d bytes, want %d", len(raw), TokenBytes)
	}
	if len(plaintext) != 43 {
		t.Errorf("the plaintext is %d characters, want 43 (32 bytes, unpadded base64url)", len(plaintext))
	}
	if strings.ContainsAny(plaintext, "+/=") {
		t.Errorf("the plaintext %q carries a character base64url does not use", plaintext)
	}
}

func TestTheStoredHashIsSHA256OfTheRAWBytesAndNotOfTheBase64Text(t *testing.T) {
	plaintext, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil {
		t.Fatalf("decoding the plaintext: %v", err)
	}

	ofRaw := sha256.Sum256(raw)
	if !bytes.Equal(hash, ofRaw[:]) {
		t.Errorf("the hash is not SHA-256 of the raw bytes\n got %x\nwant %x", hash, ofRaw)
	}

	ofText := sha256.Sum256([]byte(plaintext))
	if bytes.Equal(hash, ofText[:]) {
		t.Errorf("the hash is SHA-256 of the base64 TEXT, which spec L24 does not say:\n"+
			"    it says the raw bytes. Both are 32 bytes and both round-trip, so nothing\n"+
			"    else in this system can tell them apart.\n hash %x", hash)
	}
}

func TestTheHashIsThirtyTwoBytes(t *testing.T) {
	_, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if len(hash) != sha256.Size {
		t.Errorf("hash is %d bytes, want %d — sessions_token_hash_sha256_ck rejects anything else",
			len(hash), sha256.Size)
	}
}

func TestTwoTokensAreNeverTheSame(t *testing.T) {
	const n = 2000
	seen := make(map[string]bool, n)
	for range n {
		plaintext, _, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[plaintext] {
			t.Fatalf("NewToken returned %q twice in %d draws", plaintext, n)
		}
		seen[plaintext] = true
	}
}

func TestHashTokenReproducesWhatNewTokenReturned(t *testing.T) {
	plaintext, minted, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	presented, err := HashToken(plaintext)
	if err != nil {
		t.Fatalf("HashToken(%q): %v", plaintext, err)
	}
	if !bytes.Equal(minted, presented) {
		t.Errorf("a token presented back does not hash to what was stored\n stored %x\n  given %x",
			minted, presented)
	}
}

func TestHashTokenRefusesWhatIsNotBase64URL(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a token",
		"AAAA+AAA/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		strings.Repeat("A", 43) + "=",
	} {
		if _, err := HashToken(bad); err == nil {
			t.Errorf("HashToken(%q) = nil error, want one", bad)
		}
	}
}

func TestHashTokenRefusesAPlaintextOfTheWrongLength(t *testing.T) {
	for _, n := range []int{1, 16, 31, 33, 64} {
		short := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, n))
		if _, err := HashToken(short); err == nil {
			t.Errorf("HashToken accepted a %d-byte token; spec L24 says 32.\n"+
				"    A short token hashes to a perfectly good 32-byte digest and is\n"+
				"    stored and compared without complaint, so the wire shape has to be\n"+
				"    refused here or it is refused nowhere.", n)
		}
	}
}

func TestSameHashAnswersTrueOnlyForTheSameBytes(t *testing.T) {
	_, a, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	_, b, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !SameHash(a, append([]byte(nil), a...)) {
		t.Errorf("SameHash said two equal hashes differ")
	}
	if SameHash(a, b) {
		t.Errorf("SameHash said two different hashes match")
	}
	if SameHash(a, a[:16]) {
		t.Errorf("SameHash matched a truncated hash against the whole one")
	}
	if SameHash(nil, nil) {
		t.Errorf("SameHash matched two empty hashes — an absent token must never authenticate")
	}
}

// AN ARTEFACT CHECK, labelled as one: it walks token.go's source and can only
// fail when somebody edits that file, never because a token behaves wrongly.
//
// It is here because spec L24 names three packages BY NAME — crypto/rand,
// crypto/sha256 and crypto/subtle — and every one of them has a substitute
// that passes every behavioural leg above. math/rand/v2 mints unique tokens.
// bytes.Equal compares them correctly. Neither is what the spec asked for, and
// no test of behaviour can tell.
func TestTokenGoUsesThePackagesTheSpecNamesByName(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "token.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing token.go: %v", err)
	}

	imported := map[string]bool{}
	for _, spec := range parsed.Imports {
		imported[strings.Trim(spec.Path.Value, `"`)] = true
	}
	for _, want := range []string{"crypto/rand", "crypto/sha256", "crypto/subtle"} {
		if !imported[want] {
			t.Errorf("token.go does not import %s, which spec L24 names", want)
		}
	}
	for _, banned := range []string{"math/rand", "math/rand/v2"} {
		if imported[banned] {
			t.Errorf("token.go imports %s — a token is a credential", banned)
		}
	}

	var calls []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				calls = append(calls, pkg.Name+"."+sel.Sel.Name)
			}
		}
		return true
	})
	joined := strings.Join(calls, " ")
	for _, want := range []string{"rand.Read", "sha256.Sum256", "subtle.ConstantTimeCompare"} {
		if !strings.Contains(joined, want) {
			t.Errorf("token.go never calls %s; spec L24 names it", want)
		}
	}
	if strings.Contains(joined, "bytes.Equal") {
		t.Errorf("token.go compares with bytes.Equal, which returns early on the first\n" +
			"    differing byte. Spec L24 says subtle.ConstantTimeCompare.")
	}
}
