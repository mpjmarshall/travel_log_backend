package auth

import (
	"regexp"
	"strings"
	"testing"
)

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

const travellerA = "da5317f0-0661-4ab4-8920-204e069d385d"
const travellerB = "56499165-1058-4fa7-a13c-18350bbad89b"

func TestNewCodeIsAlwaysSixDigits(t *testing.T) {
	// THE LEADING ZERO IS THE BUG THIS CATCHES. A code drawn as an integer
	// and formatted with %d is five characters one time in ten, and the
	// traveller types what they were sent and is refused. Nothing downstream
	// can tell that from a wrong code.
	for i := 0; i < 2000; i++ {
		code, _, err := NewCode(travellerA)
		if err != nil {
			t.Fatalf("drawing a code: %v", err)
		}
		if !sixDigits.MatchString(code) {
			t.Fatalf("code %q is not six digits", code)
		}
	}
}

func TestNewCodeReachesTheLeadingZeroes(t *testing.T) {
	// Not the same assertion as the one above, which passes on an
	// implementation that can only ever draw 100000-999999. This one fails on
	// exactly that implementation. Two thousand draws with none starting at
	// zero is a probability of 0.9^2000, which is not a flake.
	for i := 0; i < 2000; i++ {
		code, _, err := NewCode(travellerA)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(code, "0") {
			return
		}
	}
	t.Fatal("2000 codes and not one below 100000: the low sixth of the range is unreachable")
}

func TestNewCodeDoesNotRepeatItself(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code, _, err := NewCode(travellerA)
		if err != nil {
			t.Fatal(err)
		}
		seen[code] = true
	}
	// 500 draws from a million with no duplicates is unlikely but legal; a
	// constant is what this rejects, and anything above a handful does it.
	if len(seen) < 400 {
		t.Fatalf("500 draws produced only %d distinct codes", len(seen))
	}
}

func TestTheDigestIsSaltedByTheTraveller(t *testing.T) {
	// A six-digit code is a million possibilities, so a global digest is a
	// rainbow table somebody builds in a second. Salting by the traveller
	// means the table has to be rebuilt per account. It does not make the
	// digest strong — see code.go — it makes it not free.
	a, err := HashCode(travellerA, "123456")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashCode(travellerB, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if SameHash(a, b) {
		t.Fatal("the same code digests identically for two travellers")
	}
}

func TestTheDigestIsStable(t *testing.T) {
	first, err := HashCode(travellerA, "004321")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashCode(travellerA, "004321")
	if err != nil {
		t.Fatal(err)
	}
	if !SameHash(first, second) {
		t.Fatal("the same traveller and code digested differently twice")
	}
}

func TestNewCodeAnswersTheDigestOfWhatItAnswered(t *testing.T) {
	// The pair has to agree or nothing can ever verify: this is the leg that
	// fails if NewCode hashes the raw draw and returns the formatted string.
	code, hash, err := NewCode(travellerA)
	if err != nil {
		t.Fatal(err)
	}
	again, err := HashCode(travellerA, code)
	if err != nil {
		t.Fatal(err)
	}
	if !SameHash(hash, again) {
		t.Fatalf("NewCode returned a digest that HashCode does not reproduce for %q", code)
	}
}

func TestAMalformedCodeIsRefused(t *testing.T) {
	// The shape is checked here or nowhere, for token.go's reason: any string
	// hashes to a perfectly good digest and compares without complaint, so
	// nothing downstream can tell a malformed code from a wrong one.
	for _, bad := range []string{"", "12345", "1234567", "12345a", "12 456", "abcdef", "１２３４５６"} {
		if _, err := HashCode(travellerA, bad); err == nil {
			t.Fatalf("%q was accepted as a code", bad)
		}
	}
}

func TestTheTravellerMustBeAUUID(t *testing.T) {
	// media/keys.go takes the same care with the same value and for the same
	// reason: it is a key, and a caller passing an empty string would salt
	// every traveller's code identically without any failure to say so.
	if _, err := HashCode("", "123456"); err == nil {
		t.Fatal("an empty traveller was accepted as a salt")
	}
	if _, err := HashCode("not-a-uuid", "123456"); err == nil {
		t.Fatal("a non-uuid traveller was accepted as a salt")
	}
}
