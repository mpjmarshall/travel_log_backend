// Putting the seeded bytes in the bucket, through the same signing a phone's
// upload capability is minted by.
package seed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"travellog/internal/media"
)

// Uploaded is what one asset's upload did. AlreadyThere is the bucket
// answering that the bytes are their own address, which is a success.
type Uploaded struct {
	Digest       string
	Size         int64
	AlreadyThere bool
}

// Upload puts every asset in the bucket under its own digest. It reports what
// it did rather than printing, so the command owns what an operator reads.
func Upload(ctx context.Context, store media.Store, client *http.Client,
	travellerID string, assets []Asset) ([]Uploaded, error) {

	if client == nil {
		client = http.DefaultClient
	}

	done := make([]Uploaded, 0, len(assets))
	for _, a := range assets {
		already, err := putOne(ctx, store, client, travellerID, a)
		if err != nil {
			return nil, err
		}
		done = append(done, Uploaded{Digest: a.Digest, Size: a.Size, AlreadyThere: already})
	}
	return done, nil
}

func putOne(ctx context.Context, store media.Store, client *http.Client,
	travellerID string, a Asset) (bool, error) {

	key := media.Key{Traveller: travellerID, Object: a.Digest}
	signed, headers, err := store.PresignPut(ctx, key,
		media.Upload{SHA256: a.Digest, ByteSize: a.Size, ContentType: a.ContentType})
	if err != nil {
		return false, fmt.Errorf("signing the upload for %s: %w", a.Locator, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed, bytes.NewReader(a.Bytes))
	if err != nil {
		return false, fmt.Errorf("building the upload for %s: %w", a.Locator, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.ContentLength = a.Size

	res, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("uploading %s: %w", a.Locator, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))

	switch {
	case res.StatusCode == http.StatusPreconditionFailed:
		return true, nil
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return false, nil
	default:
		return false, fmt.Errorf("uploading %s answered %d: %s",
			a.Locator, res.StatusCode, bytes.TrimSpace(body))
	}
}
