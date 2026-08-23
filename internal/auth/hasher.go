// Argon2id, behind an interface with one implementation (DEC-08).
//
// THE PARAMETERS ARE UNTUNED — 64 MiB, t=1, p=4 — AND EVERY OCCURRENCE SAYS
// SO. DEC-21 defers tuning to a real box, because the only honest way to pick
// them is to measure the machine that will run them against the memory it has
// and the login rate it must sustain. The numbers below are OWASP's published
// starting point and nothing more; they are not a measurement of anything in
// this project. What sizes the risk in the meantime is not the parameters, it
// is the concurrency cap in gate.go: 64 MiB per call is only a ceiling if
// something counts the calls.
//
// THE ENCODING IS PHC — `$argon2id$v=19$m=,t=,p=$salt$hash` — SO THE
// PARAMETERS TRAVEL WITH THE HASH. Without that, DEC-21's deferred tuning is
// not deferrable: raising the memory cost would recompute a different key for
// every traveller already registered and lock all of them out at once, with
// nothing in any log to say why. Verify reads its cost from the string it is
// checking and never from the struct it is a method on, and
// hasher_test.go's `written`/`cheap`/`DefaultParams` triangle is what proves
// it rather than asserts it.
//
// AND VERIFY VALIDATES BEFORE IT COMPUTES, because argon2.IDKey PANICS on
// parameters no caller would choose but a corrupt row can hold. Measured, on
// x/crypto v0.55.0: t=0 panics `argon2: number of rounds too small`, p=0
// panics `argon2: parallelism degree too low`, and a ZERO-LENGTH key does
// something worse than either — `blake2b.New(0, nil)` fails, argon2 does not
// check it, and the call dies on a nil pointer dereference inside
// blake2b.(*digest).Write. One hand-edited row would otherwise take the
// goroutine's stack out through a 500.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params is one Argon2id cost setting.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32 // passes
	Threads uint8  // lanes
	KeyLen  uint32 // bytes of output
	SaltLen uint32 // bytes of per-user salt
}

// DefaultParams is DEC-08's 64 MiB / t=1 / p=4, with a 16-byte per-user salt.
// UNTUNED (DEC-21): nothing in this project has measured them against
// anything. Change them and every existing hash keeps verifying, because the
// old cost is in the old string.
var DefaultParams = Params{Memory: 64 * 1024, Time: 1, Threads: 4, KeyLen: 32, SaltLen: 16}

// The floors below are what keeps a bad row out of the KDF. They are lower
// than anything this build would ever choose on purpose: they exist to refuse
// a panic, not to enforce a policy.
const (
	minSaltLen = 8
	minKeyLen  = 16
)

// ErrHashEncoding is a stored hash this build cannot read. It is separated
// from a wrong passphrase because the answers differ: a wrong passphrase is a
// 401 the traveller can act on, and this is a 500 an operator must.
var ErrHashEncoding = errors.New("auth: that is not an argon2id hash this build can read")

// Hasher is DEC-08's seam. One implementation, and gate.go's cap wraps it
// rather than reaching past it.
type Hasher interface {
	Hash(passphrase string) (string, error)
	Verify(encoded, passphrase string) (bool, error)
}

// Argon2id is that implementation.
type Argon2id struct{ Params Params }

// Hash draws a fresh salt and answers the PHC encoding.
func (h Argon2id) Hash(passphrase string) (string, error) {
	if err := h.Params.check(); err != nil {
		return "", err
	}
	salt := make([]byte, h.Params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: drawing a %d-byte salt: %w", h.Params.SaltLen, err)
	}
	key := argon2.IDKey([]byte(passphrase), salt,
		h.Params.Time, h.Params.Memory, h.Params.Threads, h.Params.KeyLen)

	return "$argon2id$v=" + fmt.Sprint(argon2.Version) + "$" + h.Params.encode() +
		"$" + base64.RawStdEncoding.EncodeToString(salt) +
		"$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// Verify recomputes at the cost the ENCODING names, never at this hasher's own.
func (h Argon2id) Verify(encoded, passphrase string) (bool, error) {
	p, salt, key, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

func (p Params) encode() string {
	return fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads)
}

func (p Params) check() error {
	switch {
	case p.Time < 1:
		return fmt.Errorf("%w: t=%d, and argon2.IDKey panics below 1", ErrHashEncoding, p.Time)
	case p.Threads < 1:
		return fmt.Errorf("%w: p=%d, and argon2.IDKey panics below 1", ErrHashEncoding, p.Threads)
	case p.Memory < 8*uint32(p.Threads):
		return fmt.Errorf("%w: m=%d is below the 8*p=%d floor argon2 needs to lay out its blocks",
			ErrHashEncoding, p.Memory, 8*uint32(p.Threads))
	case p.KeyLen < minKeyLen:
		return fmt.Errorf("%w: a %d-byte key; below %d, and a zero-length one dies inside blake2b",
			ErrHashEncoding, p.KeyLen, minKeyLen)
	case p.SaltLen < minSaltLen:
		return fmt.Errorf("%w: a %d-byte salt, below the %d-byte floor", ErrHashEncoding, p.SaltLen, minSaltLen)
	}
	return nil
}

// parseHash reads the whole encoding, strictly.
//
// EVERY FIELD IS RE-RENDERED AND COMPARED RATHER THAN SCANNED AND TRUSTED.
// fmt.Sscanf("m=8192,t=2,p=1,evil=1", "m=%d,t=%d,p=%d", …) answers n=3 and no
// error, silently discarding the tail — so a string that is not exactly what
// Hash writes would otherwise be accepted as though it were.
func parseHash(encoded string) (Params, []byte, []byte, error) {
	f := strings.Split(encoded, "$")
	if len(f) != 6 || f[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: %d fields, want 6", ErrHashEncoding, len(f))
	}
	if f[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: the algorithm is %q, and argon2i and argon2d "+
			"are different KDFs that would verify to different keys", ErrHashEncoding, f[1])
	}

	var version int
	if _, err := fmt.Sscanf(f[2], "v=%d", &version); err != nil || fmt.Sprintf("v=%d", version) != f[2] {
		return Params{}, nil, nil, fmt.Errorf("%w: the version field is %q", ErrHashEncoding, f[2])
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("%w: version %d, and this build computes %d",
			ErrHashEncoding, version, argon2.Version)
	}

	var p Params
	var threads uint32
	if _, err := fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: the parameter field is %q", ErrHashEncoding, f[3])
	}
	if threads > 255 {
		return Params{}, nil, nil, fmt.Errorf("%w: p=%d does not fit a lane count", ErrHashEncoding, threads)
	}
	p.Threads = uint8(threads)
	if p.encode() != f[3] {
		return Params{}, nil, nil, fmt.Errorf("%w: the parameter field is %q, and this build writes %q",
			ErrHashEncoding, f[3], p.encode())
	}

	salt, key, err := saltAndKeyOf(encoded)
	if err != nil {
		return Params{}, nil, nil, err
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	if err := p.check(); err != nil {
		return Params{}, nil, nil, err
	}
	return p, salt, key, nil
}

// saltAndKeyOf decodes the last two fields. It is separate so a test can read
// back what Hash wrote without re-deriving the encoding.
func saltAndKeyOf(encoded string) (salt, key []byte, err error) {
	f := strings.Split(encoded, "$")
	if len(f) != 6 {
		return nil, nil, fmt.Errorf("%w: %d fields, want 6", ErrHashEncoding, len(f))
	}
	if salt, err = base64.RawStdEncoding.DecodeString(f[4]); err != nil {
		return nil, nil, fmt.Errorf("%w: the salt is not unpadded base64: %v", ErrHashEncoding, err)
	}
	if key, err = base64.RawStdEncoding.DecodeString(f[5]); err != nil {
		return nil, nil, fmt.Errorf("%w: the key is not unpadded base64: %v", ErrHashEncoding, err)
	}
	return salt, key, nil
}
