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
	if len(seen) < 400 {
		t.Fatalf("500 draws produced only %d distinct codes", len(seen))
	}
}

func TestTheDigestIsSaltedByTheTraveller(t *testing.T) {
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
	for _, bad := range []string{"", "12345", "1234567", "12345a", "12 456", "abcdef", "１２３４５６"} {
		if _, err := HashCode(travellerA, bad); err == nil {
			t.Fatalf("%q was accepted as a code", bad)
		}
	}
}

func TestTheTravellerMustBeAUUID(t *testing.T) {
	if _, err := HashCode("", "123456"); err == nil {
		t.Fatal("an empty traveller was accepted as a salt")
	}
	if _, err := HashCode("not-a-uuid", "123456"); err == nil {
		t.Fatal("a non-uuid traveller was accepted as a salt")
	}
}
