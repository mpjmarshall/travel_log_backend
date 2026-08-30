// Package media is the seam onto S3-compatible object storage: the API mints
// short-lived signed URLs and never sees a photograph's bytes.
package media

import (
	"context"
	"errors"
)

// ErrNoSuchObject is what Stat answers for a key the bucket does not hold.
var ErrNoSuchObject = errors.New("media: no such object")

// Key addresses one object.
type Key struct {
	Traveller string
	Object    string
}

// Upload is everything besides the key that a PUT signature covers.
type Upload struct {
	SHA256 string

	ByteSize int64

	ContentType string
}

// Attributes is what the bucket says about an object that is there.
type Attributes struct {
	Size        int64
	ContentType string

	SHA256 string
}

// Audience picks which of's two presign lifetimes a read URL gets.
type Audience int

const (
	Private Audience = iota

	Public
)

func (a Audience) String() string {
	if a == Public {
		return "public"
	}
	return "private"
}

// Store is what the rest of the application sees.
type Store interface {
	EnsureBucket(ctx context.Context) error

	PresignPut(ctx context.Context, key Key, up Upload) (url string, headers map[string]string, err error)

	PresignGet(ctx context.Context, key Key, aud Audience) (string, error)

	Stat(ctx context.Context, key Key) (Attributes, error)
}
