// The media half of a seed: reading the bundle the captured log addresses,
// and putting those bytes in the bucket under their own digest.
package seed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// imageryNames is the bundle the captured log addresses, and the client ships
// exactly these two.
var imageryNames = []string{"card-ireland.png", "hero-mountain.png"}

// Asset is one file, addressed by the sha256 of its own bytes.
type Asset struct {
	Locator     string
	Path        string
	Digest      string
	Size        int64
	ContentType string
	Bytes       []byte
}

// ReadImagery turns the directory beside the fixture into the asset table.
func ReadImagery(dir string) ([]Asset, error) {
	out := make([]Asset, 0, len(imageryNames))
	for _, name := range imageryNames {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading the imagery the captured log addresses: %w", err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Asset{
			Locator:     "assets/imagery/" + name,
			Path:        path,
			Digest:      hex.EncodeToString(sum[:]),
			Size:        int64(len(body)),
			ContentType: "image/png",
			Bytes:       body,
		})
	}
	return out, nil
}

// Mapping answers the locator-to-digest table the captured document is
// rewritten against.
func Mapping(assets []Asset) map[string]string {
	out := make(map[string]string, len(assets))
	for _, a := range assets {
		out[a.Locator] = a.Digest
	}
	return out
}

// MediaObjects is the media_objects half, with uploaded_at set — which is what
// makes a seeded object indistinguishable from an uploaded one.
func MediaObjects(travellerID string, assets []Asset, at time.Time) []MediaObject {
	out := make([]MediaObject, 0, len(assets))
	for _, a := range assets {
		uploaded := at
		out = append(out, MediaObject{
			TravellerID: travellerID, ID: a.Digest, ByteSize: a.Size,
			ContentType: a.ContentType, CreatedAt: at, UploadedAt: &uploaded,
		})
	}
	return out
}
