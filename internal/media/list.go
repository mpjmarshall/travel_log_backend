package media

import (
	"context"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// Listing is one object as the bucket reports it, split back into the two
// halves its key is made of.
type Listing struct {
	Traveller    string
	Object       string
	Size         int64
	LastModified time.Time
}

// List walks the whole bucket. A key that is not traveller/object is ignored,
// because nothing this application wrote looks like that.
func (m *MinIO) List(ctx context.Context) ([]Listing, error) {
	out := []Listing{}
	for info := range m.api.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if info.Err != nil {
			return nil, info.Err
		}
		traveller, object, ok := splitKey(info.Key)
		if !ok {
			continue
		}
		out = append(out, Listing{
			Traveller:    traveller,
			Object:       object,
			Size:         info.Size,
			LastModified: info.LastModified,
		})
	}
	return out, nil
}

// List answers what this twin holds, in no particular order.
func (m *Memory) List(context.Context) ([]Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []Listing{}
	for path, attrs := range m.objects {
		traveller, object, ok := splitKey(path)
		if !ok {
			continue
		}
		out = append(out, Listing{
			Traveller:    traveller,
			Object:       object,
			Size:         attrs.Size,
			LastModified: time.Time{},
		})
	}
	return out, nil
}

func splitKey(key string) (traveller, object string, ok bool) {
	traveller, object, ok = strings.Cut(key, "/")
	if !ok || traveller == "" || object == "" {
		return "", "", false
	}
	if strings.Contains(object, "/") {
		return "", "", false
	}
	return traveller, object, true
}
