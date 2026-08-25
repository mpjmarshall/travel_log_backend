package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Memory is the twin the handler legs in R3 run against: no daemon, no
// network, no build tag.
//
// IT IS NOT A STUB THAT SAYS YES. Every rule the real bucket enforces is
// enforced here too — the digest, the exact length, the content type, the
// write-once, and the bucket having to exist before anything can be stored —
// because a twin that accepts what MinIO refuses turns a handler leg into
// evidence about nothing. What it deliberately does NOT do is sign: its URLs
// are unroutable by construction, and the leg that a signature is right is the
// integration one and can only be the integration one (DEC-16 draws exactly
// this line).
//
// The URLs it hands back still carry X-Amz-SignedHeaders and X-Amz-Expires,
// because the two legs that read those — the header-map honesty leg and the
// audience-lifetime leg — are about this package's own bookkeeping rather than
// about SigV4, and they are worth having at both seams.
type Memory struct {
	mu      sync.Mutex
	made    bool
	objects map[string]Attributes

	// TTL is exported so a test can set the two lifetimes to the values the
	// deployment uses. It defaults to DEC-44's two minutes and DEC-84's
	// fifteen, which are the numbers deploy/.env.example carries.
	TTL map[Audience]time.Duration
}

var _ Store = (*Memory)(nil)

// NewMemory answers an empty twin with NO bucket, which is the state DEC-98 is
// about: a fresh MinIO has none either, and a handler test that never calls
// EnsureBucket should fail the way production would.
func NewMemory() *Memory {
	return &Memory{
		objects: map[string]Attributes{},
		TTL: map[Audience]time.Duration{
			Private: 2 * time.Minute,
			Public:  15 * time.Minute,
		},
	}
}

func (m *Memory) EnsureBucket(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.made = true
	return nil
}

func (m *Memory) PresignPut(_ context.Context, key Key, up Upload) (string, map[string]string, error) {
	if err := checkUpload(key, up); err != nil {
		return "", nil, err
	}
	path, checksum, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", nil, err
	}
	headers := uploadHeaders(up, checksum)
	return m.fake("PUT", path, m.lifetime(Private), headers), headers, nil
}

func (m *Memory) PresignGet(_ context.Context, key Key, aud Audience) (string, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", err
	}
	if aud != Private && aud != Public {
		return "", fmt.Errorf("media: %d is not an audience", aud)
	}
	// THE DISPOSITION IS ON THE TWIN'S URLs TOO (DEC-51). It is inside the
	// real signature, so a holder cannot strip it — measured: deleting the
	// query parameter answers 403 SignatureDoesNotMatch. A twin whose read
	// URLs did not carry it would let a handler leg about "every presigned
	// GET is marked as a download" pass against a handler that stopped asking
	// for one, which is the vacuous shape this package's whole design refuses.
	return m.fake("GET", path, m.lifetime(aud), nil,
		"response-content-disposition", "attachment"), nil
}

func (m *Memory) Stat(_ context.Context, key Key) (Attributes, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return Attributes{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.made {
		return Attributes{}, fmt.Errorf("%w: %s: NoSuchBucket", ErrNoSuchObject, path)
	}
	got, ok := m.objects[path]
	if !ok {
		return Attributes{}, fmt.Errorf("%w: %s: NoSuchKey", ErrNoSuchObject, path)
	}
	return got, nil
}

// Put is what an upload through one of this twin's URLs would do, and it
// applies the same four refusals the signature makes real MinIO apply. It is
// how a handler leg says "the client's upload landed" without a bucket.
//
// The error strings carry the S3 CODE the real server answers, because that is
// what the integration legs assert on and a twin that invented its own
// vocabulary would make the two tiers untranslatable.
func (m *Memory) Put(key Key, up Upload, body []byte) error {
	if err := checkUpload(key, up); err != nil {
		return err
	}
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.made {
		return fmt.Errorf("media: NoSuchBucket: %s", path)
	}
	if _, taken := m.objects[path]; taken {
		return fmt.Errorf("media: PreconditionFailed: %s already holds bytes, and "+
			"If-None-Match makes a content address write-once", path)
	}
	if int64(len(body)) != up.ByteSize {
		return fmt.Errorf("media: SignatureDoesNotMatch: %d bytes against a "+
			"signature for %d", len(body), up.ByteSize)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != key.Object {
		return fmt.Errorf("media: XAmzContentChecksumMismatch: the bytes hash to "+
			"%s and the address is %s", got, key.Object)
	}

	m.objects[path] = Attributes{
		Size:        up.ByteSize,
		ContentType: up.ContentType,
		SHA256:      key.Object,
	}
	return nil
}

func (m *Memory) lifetime(aud Audience) time.Duration {
	if d, ok := m.TTL[aud]; ok {
		return d
	}
	return 0
}

// fake builds an unroutable URL carrying the two query parameters the legs
// read. `memory.invalid` is a reserved TLD (RFC 2606), so a test that
// accidentally fetches one gets a DNS failure rather than somebody's server.
func (m *Memory) fake(method, path string, ttl time.Duration, headers map[string]string, extra ...string) string {
	q := url.Values{}
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	q.Set("X-Amz-Method", method)
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}

	names := []string{"host"}
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	q.Set("X-Amz-SignedHeaders", strings.Join(names, ";"))

	return "https://memory.invalid/" + path + "?" + q.Encode()
}

// PutWithoutChecksum is what an upload through one of the two BANNED presign
// calls leaves behind: the right bytes at the right address, with NO checksum
// recorded against them.
//
// IT IS A TEST SEAM AND IT IS THE ONLY REASON THE BAN IS A RUNTIME GUARD RATHER
// THAN AN AST WALK (DEC-88). `StatObject` with `Checksum: true` answers the
// digest the BUCKET stored, and both banned calls sign `host` and nothing else,
// so an object uploaded through either carries an EMPTY checksum — measured
// against MinIO. Without a way to produce that state, the commit path's
// non-empty-and-matching check is a branch no leg can reach, and the emptiness
// this package's Attributes.SHA256 comment calls "load-bearing" is a claim
// nothing checks.
//
// It deliberately skips the write-once and the digest refusals as well, because
// a `host`-only signature enforces neither — that IS the finding.
func (m *Memory) PutWithoutChecksum(key Key, up Upload, body []byte) error {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.made {
		return fmt.Errorf("media: NoSuchBucket: %s", path)
	}
	m.objects[path] = Attributes{
		Size:        int64(len(body)),
		ContentType: up.ContentType,
		SHA256:      "",
	}
	return nil
}
