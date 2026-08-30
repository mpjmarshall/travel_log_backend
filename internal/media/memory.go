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

// Memory is the in-process twin the handler legs run against: no daemon, no
// network, no build tag.
type Memory struct {
	mu      sync.Mutex
	made    bool
	objects map[string]Attributes

	TTL map[Audience]time.Duration
}

var _ Store = (*Memory)(nil)

// NewMemory answers an empty twin with no bucket, which is the state is
// about.
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
// applies the same four refusals the signature makes real MinIO apply.
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

// fake builds an unroutable URL carrying's two query parameters the legs
// read.
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

// PutWithoutChecksum is what an upload through one of's two banned presign
// calls leaves behind.
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
