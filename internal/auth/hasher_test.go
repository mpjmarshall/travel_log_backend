// Argon2id (DEC-08), test-first.
//
// MOST LEGS RUN AT CHEAP PARAMETERS AND THAT IS A DECISION, NOT A SHORTCUT.
// The shipped parameters are 64 MiB per call; a file that used them
// everywhere would allocate a gigabyte to assert things about string parsing.
// The legs that are ABOUT the parameters — that they are DEC-21's untuned
// numbers, and that a hash carries its own — use the real ones, and they are
// the only ones that could notice a change to them.
package auth

import (
	"strings"
	"testing"
)

// cheap is Argon2id at parameters chosen to be fast, and DELIBERATELY unlike
// the defaults in all four fields: a leg that passes because the hasher
// quietly used its own settings instead of the encoded ones must not be able
// to pass by coincidence.
var cheap = Argon2id{Params: Params{Memory: 8 << 10, Time: 2, Threads: 1, KeyLen: 16, SaltLen: 8}}

func TestTheDefaultParametersAreDEC21sUntunedOnes(t *testing.T) {
	p := DefaultParams
	if p.Memory != 64*1024 {
		t.Errorf("Memory = %d KiB, want 65536 (64 MiB) — DEC-08", p.Memory)
	}
	if p.Time != 1 {
		t.Errorf("Time = %d, want 1 — DEC-08", p.Time)
	}
	if p.Threads != 4 {
		t.Errorf("Threads = %d, want 4 — DEC-08", p.Threads)
	}
	if p.SaltLen != 16 {
		t.Errorf("SaltLen = %d, want 16 — DEC-08 says a per-user 16-byte crypto/rand salt", p.SaltLen)
	}
	if p.KeyLen != 32 {
		t.Errorf("KeyLen = %d, want 32", p.KeyLen)
	}
}

func TestHashEncodesTheParametersBesideTheDigest(t *testing.T) {
	h := Argon2id{Params: DefaultParams}
	encoded, err := h.Hash("a passphrase nobody guesses")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		t.Fatalf("the encoding is %q, want six $-separated fields — DEC-08 asks for\n"+
			"    $argon2id$v=19$m=,t=,p=$salt$hash so the parameters travel with the hash", encoded)
	}
	if parts[1] != "argon2id" {
		t.Errorf("algorithm field = %q, want argon2id", parts[1])
	}
	if parts[2] != "v=19" {
		t.Errorf("version field = %q, want v=19", parts[2])
	}
	if parts[3] != "m=65536,t=1,p=4" {
		t.Errorf("parameter field = %q, want m=65536,t=1,p=4", parts[3])
	}
	if parts[4] == "" || parts[5] == "" {
		t.Errorf("salt or hash field is empty: %q", encoded)
	}
	if strings.ContainsAny(parts[4]+parts[5], "=") {
		t.Errorf("the salt or hash is padded base64; PHC uses the unpadded form: %q", encoded)
	}
}

func TestTheSamePassphraseHashesDifferentlyEveryTime(t *testing.T) {
	seen := map[string]bool{}
	for range 8 {
		encoded, err := cheap.Hash("one passphrase")
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if seen[encoded] {
			t.Fatalf("two hashes of one passphrase came out identical — the salt is not per-user:\n  %s", encoded)
		}
		seen[encoded] = true
	}
}

func TestTheSaltIsTheLengthTheParametersAsked(t *testing.T) {
	h := Argon2id{Params: DefaultParams}
	encoded, err := h.Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	salt, hash, err := saltAndKeyOf(encoded)
	if err != nil {
		t.Fatalf("re-reading the encoding: %v", err)
	}
	if len(salt) != int(DefaultParams.SaltLen) {
		t.Errorf("salt is %d bytes, want %d", len(salt), DefaultParams.SaltLen)
	}
	if len(hash) != int(DefaultParams.KeyLen) {
		t.Errorf("key is %d bytes, want %d", len(hash), DefaultParams.KeyLen)
	}
}

func TestVerifyAcceptsTheRightPassphraseAndRefusesEveryOther(t *testing.T) {
	encoded, err := cheap.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := cheap.Verify(encoded, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Verify on the right passphrase: %v", err)
	}
	if !ok {
		t.Errorf("Verify refused the passphrase it was given")
	}
	for _, wrong := range []string{
		"", "correct horse battery stapl", "correct horse battery staple ",
		"Correct horse battery staple", "wrong",
	} {
		ok, err := cheap.Verify(encoded, wrong)
		if err != nil {
			t.Fatalf("Verify(%q): %v", wrong, err)
		}
		if ok {
			t.Errorf("Verify accepted %q", wrong)
		}
	}
}

// THE LEG DEC-08's ENCODING EXISTS FOR. A hash written under one set of
// parameters must still verify under a hasher configured with another — that
// is the whole reason the parameters are in the string. `cheap` and the
// default differ in all four fields, so a Verify that used its OWN settings
// computes a different key and refuses a correct passphrase. Which is what
// DEC-21's deferred tuning would do to every existing traveller on the day
// somebody raises the memory cost.
func TestVerifyReadsTheParametersOutOfTheEncodingRatherThanUsingItsOwn(t *testing.T) {
	written := Argon2id{Params: Params{Memory: 16 << 10, Time: 3, Threads: 2, KeyLen: 24, SaltLen: 12}}
	encoded, err := written.Hash("shared secret")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	for name, reader := range map[string]Argon2id{
		"the shipped hasher": {Params: DefaultParams},
		"the cheap hasher":   cheap,
	} {
		ok, err := reader.Verify(encoded, "shared secret")
		if err != nil {
			t.Fatalf("%s, Verify: %v", name, err)
		}
		if !ok {
			t.Errorf("%s refused a correct passphrase whose hash carries m=%d,t=%d,p=%d — "+
				"it used its own parameters instead of the encoded ones",
				name, written.Params.Memory, written.Params.Time, written.Params.Threads)
		}
	}
}

func TestVerifyRefusesAnEncodingItCannotRead(t *testing.T) {
	good, err := cheap.Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	fields := strings.Split(good, "$")

	bad := map[string]string{
		"empty":                              "",
		"not PHC at all":                     "just a string",
		"five fields":                        "$argon2id$v=19$m=8192,t=2,p=1$" + fields[4],
		"seven fields":                       good + "$extra",
		"argon2i, a different KDF":           "$argon2i$" + strings.Join(fields[2:], "$"),
		"argon2d, a different KDF":           "$argon2d$" + strings.Join(fields[2:], "$"),
		"bcrypt wearing the shape":           "$2b$12$" + fields[4] + "$" + fields[5],
		"a version this build does not know": "$argon2id$v=16$" + strings.Join(fields[3:], "$"),
		"no version":                         "$argon2id$m=8192,t=2,p=1$" + fields[4] + "$" + fields[5],
		"parameters out of order":            "$argon2id$v=19$t=2,m=8192,p=1$" + fields[4] + "$" + fields[5],
		"a parameter that is not a number":   "$argon2id$v=19$m=lots,t=2,p=1$" + fields[4] + "$" + fields[5],
		"salt that is not base64":            "$argon2id$v=19$m=8192,t=2,p=1$!!!!$" + fields[5],
		"key that is not base64":             "$argon2id$v=19$m=8192,t=2,p=1$" + fields[4] + "$!!!!",
		"an empty salt":                      "$argon2id$v=19$m=8192,t=2,p=1$$" + fields[5],
		"an empty key":                       "$argon2id$v=19$m=8192,t=2,p=1$" + fields[4] + "$",
	}
	for name, encoded := range bad {
		ok, err := cheap.Verify(encoded, "x")
		if err == nil {
			t.Errorf("Verify accepted %s (%q) with no error", name, encoded)
		}
		if ok {
			t.Errorf("Verify said %s (%q) MATCHES", name, encoded)
		}
	}
}

// A row in the database is not trusted input in the way a request body is, but
// it is input all the same: a bad migration, a restore from the wrong dump, or
// a hand-edited row reaches Verify. argon2.IDKey PANICS on t=0 and on p=0 —
// measured, and the panic is `argon2: number of rounds too small` — so a
// parameter check here is what stands between one malformed row and a 500 that
// takes the goroutine's stack with it.
func TestVerifyRefusesParametersThatWouldPanicTheKDF(t *testing.T) {
	good, err := cheap.Hash("x")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	fields := strings.Split(good, "$")
	tail := "$" + fields[4] + "$" + fields[5]

	for _, params := range []string{
		"m=8192,t=0,p=1",
		"m=8192,t=2,p=0",
		"m=0,t=2,p=1",
		"m=1,t=2,p=1",
	} {
		encoded := "$argon2id$v=19$" + params + tail
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Verify PANICKED on %q: %v", params, r)
				}
			}()
			if ok, err := cheap.Verify(encoded, "x"); err == nil || ok {
				t.Errorf("Verify(%q) = (%v, %v), want (false, an error)", params, ok, err)
			}
		}()
	}
}

func TestHashRefusesParametersThatWouldPanicTheKDF(t *testing.T) {
	for _, p := range []Params{
		{Memory: 8 << 10, Time: 0, Threads: 1, KeyLen: 16, SaltLen: 8},
		{Memory: 8 << 10, Time: 1, Threads: 0, KeyLen: 16, SaltLen: 8},
		{Memory: 0, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 8},
		{Memory: 8 << 10, Time: 1, Threads: 1, KeyLen: 0, SaltLen: 8},
		{Memory: 8 << 10, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 0},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Hash PANICKED on %+v: %v", p, r)
				}
			}()
			if _, err := (Argon2id{Params: p}).Hash("x"); err == nil {
				t.Errorf("Hash(%+v) = nil error, want one", p)
			}
		}()
	}
}

// Argon2id satisfies Hasher. Stated as a leg rather than as a blank assignment
// in the source, because DEC-08 asks for "an interface with ONE
// implementation" and the interface is the half that is easy to let rot.
//
// ITS FAILURE MODE IS A BUILD ERROR, NOT A RED ASSERTION, and that is recorded
// rather than glossed. Renaming Argon2id.Verify does not make this leg report
// a wrong answer — it makes the package stop compiling, with
// `Argon2id does not implement Hasher (missing method Verify)` among six
// errors. That is still a reddening and still exit 1, and it is the right one:
// an interface and its only implementation drifting apart is a fact about
// types, so the type checker is the thing that should say so.
func TestArgon2idIsTheHasher(t *testing.T) {
	var h Hasher = Argon2id{Params: cheap.Params}
	encoded, err := h.Hash("x")
	if err != nil {
		t.Fatalf("Hash through the interface: %v", err)
	}
	ok, err := h.Verify(encoded, "x")
	if err != nil || !ok {
		t.Errorf("Verify through the interface = (%v, %v), want (true, nil)", ok, err)
	}
}
