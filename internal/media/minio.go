package media

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is what MinIO needs, and every field of it comes from
// internal/config — this package reads no environment of its own.
type Config struct {
	InternalEndpoint string
	PublicBaseURL    string

	Region    string
	Bucket    string
	AccessKey string
	SecretKey string

	TTLPrivate time.Duration
	TTLPublic  time.Duration
}

// MinIO is the one implementation that talks to a bucket.
type MinIO struct {
	api    *minio.Client
	signer *minio.Client
	bucket string
	ttl    map[Audience]time.Duration
}

var _ Store = (*MinIO)(nil)

// New builds's two clients this package needs.
func New(cfg Config) (*MinIO, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("media: no bucket name")
	}
	if cfg.TTLPrivate <= 0 || cfg.TTLPublic <= 0 {
		return nil, fmt.Errorf("media: both presign lifetimes must be positive, "+
			"got private=%s public=%s", cfg.TTLPrivate, cfg.TTLPublic)
	}

	api, err := client(cfg.InternalEndpoint, cfg)
	if err != nil {
		return nil, fmt.Errorf("media: the internal endpoint: %w", err)
	}
	signer, err := client(cfg.PublicBaseURL, cfg)
	if err != nil {
		return nil, fmt.Errorf("media: the public base URL: %w", err)
	}

	return &MinIO{
		api:    api,
		signer: signer,
		bucket: cfg.Bucket,
		ttl: map[Audience]time.Duration{
			Private: cfg.TTLPrivate,
			Public:  cfg.TTLPublic,
		},
	}, nil
}

// client parses one of's two addresses into what minio.New wants: a host,
// Whether to speak TLS.
func client(address string, cfg Config) (*minio.Client, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return nil, fmt.Errorf("parsing %q: %w", address, err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%q must be an http:// or https:// address with a host", address)
	}
	return minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: u.Scheme == "https",
		Region: cfg.Region,
	})
}

// EnsureBucket is, and it runs at boot.
func (m *MinIO) EnsureBucket(ctx context.Context) error {
	exists, err := m.api.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("media: asking whether the bucket %q exists: %w", m.bucket, err)
	}
	if exists {
		return nil
	}
	if err := m.api.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
		if minio.ToErrorResponse(err).Code == "BucketAlreadyOwnedByYou" {
			return nil
		}
		return fmt.Errorf("media: creating the bucket %q: %w", m.bucket, err)
	}
	return nil
}

// PresignPut mints the upload capability.
func (m *MinIO) PresignPut(ctx context.Context, key Key, up Upload) (string, map[string]string, error) {
	if err := checkUpload(key, up); err != nil {
		return "", nil, err
	}
	path, checksum, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", nil, err
	}

	headers := uploadHeaders(up, checksum)

	extra := http.Header{}
	for name, value := range headers {
		extra.Set(name, value)
	}

	u, err := m.signer.PresignHeader(ctx, http.MethodPut, m.bucket, path, m.ttl[Private], nil, extra)
	if err != nil {
		return "", nil, fmt.Errorf("media: signing an upload for %s: %w", path, err)
	}
	return u.String(), headers, nil
}

// SignedHeaders reads the header names a presigned URL's signature covers
// back off the URL itself, lowercase and sorted, `host` included.
func SignedHeaders(presigned string) ([]string, error) {
	u, err := url.Parse(presigned)
	if err != nil {
		return nil, fmt.Errorf("media: parsing the presigned URL: %w", err)
	}
	raw := u.Query().Get("X-Amz-SignedHeaders")
	if raw == "" {
		return nil, errors.New("media: the URL carries no X-Amz-SignedHeaders, " +
			"which is what a URL signed by one of the banned calls looks like")
	}
	return strings.Split(raw, ";"), nil
}

// PresignGet mints a read capability for one audience.
func (m *MinIO) PresignGet(ctx context.Context, key Key, aud Audience) (string, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return "", err
	}
	ttl, ok := m.ttl[aud]
	if !ok {
		return "", fmt.Errorf("media: %d is not an audience", aud)
	}

	params := url.Values{}
	params.Set("response-content-disposition", "attachment")

	u, err := m.signer.PresignedGetObject(ctx, m.bucket, path, ttl, params)
	if err != nil {
		return "", fmt.Errorf("media: signing a %s read for %s: %w", aud, path, err)
	}
	return u.String(), nil
}

// Stat answers what the bucket holds, or ErrNoSuchObject.
func (m *MinIO) Stat(ctx context.Context, key Key) (Attributes, error) {
	path, _, err := Address(key.Traveller, key.Object)
	if err != nil {
		return Attributes{}, err
	}

	info, err := m.api.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{Checksum: true})
	if err != nil {
		if code := minio.ToErrorResponse(err).Code; code == "NoSuchKey" || code == "NoSuchBucket" {
			return Attributes{}, fmt.Errorf("%w: %s: %s", ErrNoSuchObject, path, code)
		}
		return Attributes{}, fmt.Errorf("media: stat %s: %w", path, err)
	}

	return Attributes{
		Size:        info.Size,
		ContentType: info.ContentType,
		SHA256:      hexOfBase64(info.ChecksumSHA256),
	}, nil
}

// hexOfBase64 turns the bucket the base64 checksum back into the hex the rest
// of this system speaks.
func hexOfBase64(b64 string) string {
	if b64 == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

// ExpiresIn reads `X-Amz-Expires` back off a presigned URL, as a duration.
func ExpiresIn(presigned string) (time.Duration, error) {
	u, err := url.Parse(presigned)
	if err != nil {
		return 0, fmt.Errorf("media: parsing the presigned URL: %w", err)
	}
	raw := u.Query().Get("X-Amz-Expires")
	if raw == "" {
		return 0, errors.New("media: the URL carries no X-Amz-Expires, so there is " +
			"nothing to tell the client about how long it has")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("media: X-Amz-Expires is %q, which is not a positive "+
			"number of seconds", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}
