// The media half of a seed: the address is the content, and the row says the
// bytes are already in the bucket.
package seed_test

import (
	"path/filepath"
	"testing"
	"time"

	"travellog/internal/seed"
)

// imageryDir is the client's own bundle, which the captured log addresses.
const imageryDir = "../logbook/testdata/imagery"

// The digests are shasum -a 256's answer, not this package's, so the leg can
// disagree with the code that computes them.
var wantAssets = map[string]struct {
	digest string
	size   int64
}{
	"card-ireland.png":  {"8dfb203bc0f890655a7545004866da13482af78d21b5c6deb7bd142592a5d3cd", 529392},
	"hero-mountain.png": {"e66b552e6043510bb5cd474096d18208b1c975556ef1a8cfc565dd63a02835c1", 555376},
}

func TestReadImageryAddressesEachFileByItsOwnBytes(t *testing.T) {
	assets, err := seed.ReadImagery(filepath.Clean(imageryDir))
	if err != nil {
		t.Fatalf("ReadImagery: %v", err)
	}
	if len(assets) != len(wantAssets) {
		t.Fatalf("ReadImagery answered %d assets, want %d", len(assets), len(wantAssets))
	}

	for _, a := range assets {
		name := filepath.Base(a.Locator)
		want, known := wantAssets[name]
		if !known {
			t.Errorf("ReadImagery answered an asset nobody asked for: %s", a.Locator)
			continue
		}
		if a.Digest != want.digest {
			t.Errorf("%s addresses %s, want %s — the digest is not the bytes, so every "+
				"row that names it points at an object the bucket does not hold",
				name, a.Digest, want.digest)
		}
		if a.Size != want.size {
			t.Errorf("%s is %d bytes, want %d", name, a.Size, want.size)
		}
		if a.ContentType != "image/png" {
			t.Errorf("%s is %q, want image/png", name, a.ContentType)
		}
		if a.Locator != "assets/imagery/"+name {
			t.Errorf("locator is %q, and the captured log addresses assets/imagery/%s",
				a.Locator, name)
		}
	}
}

func TestReadImagerySaysWhichDirectoryItCouldNotRead(t *testing.T) {
	_, err := seed.ReadImagery("nowhere-in-particular")
	if err == nil {
		t.Fatal("ReadImagery answered no error for a directory that does not exist, so a " +
			"seed would carry on and write rows addressing objects nobody uploaded")
	}
}

// BACKFILL: Mapping and MediaObjects were written before these legs existed.
// Each is mutation-proven below rather than reported as red-first.
func TestMappingAnswersTheLocatorTheCapturedLogAddresses(t *testing.T) {
	assets, err := seed.ReadImagery(imageryDir)
	if err != nil {
		t.Fatalf("ReadImagery: %v", err)
	}
	mapping := seed.Mapping(assets)

	if len(mapping) != len(wantAssets) {
		t.Fatalf("the mapping holds %d entries, want %d — two locators sharing a digest "+
			"would give one object every reference in the log", len(mapping), len(wantAssets))
	}
	for name, want := range wantAssets {
		got, held := mapping["assets/imagery/"+name]
		if !held {
			t.Errorf("the mapping does not answer assets/imagery/%s", name)
			continue
		}
		if got != want.digest {
			t.Errorf("assets/imagery/%s maps to %s, want %s", name, got, want.digest)
		}
	}
}

func TestMediaObjectsAreIndistinguishableFromAnUploadedOne(t *testing.T) {
	assets, err := seed.ReadImagery(imageryDir)
	if err != nil {
		t.Fatalf("ReadImagery: %v", err)
	}
	at := time.Date(2027, 5, 12, 9, 0, 0, 0, time.UTC)
	rows := seed.MediaObjects("00000000-0000-4000-8000-000000000001", assets, at)

	if len(rows) != len(assets) {
		t.Fatalf("MediaObjects answered %d rows for %d assets", len(rows), len(assets))
	}
	for _, row := range rows {
		if row.UploadedAt == nil {
			t.Errorf("%s has a nil uploaded_at, and R3's photo route refuses an asset "+
				"that was begun and never uploaded — so every reference to it is a 422", row.ID)
			continue
		}
		if !row.UploadedAt.Equal(at) {
			t.Errorf("%s uploaded_at = %s, want %s", row.ID, row.UploadedAt, at)
		}
		if row.ID != row.ID || row.ByteSize == 0 || row.ContentType != "image/png" {
			t.Errorf("row %s carries %d bytes of %q", row.ID, row.ByteSize, row.ContentType)
		}
	}
}
