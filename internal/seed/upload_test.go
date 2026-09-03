// The upload, against a bucket that answers the way MinIO does.
package seed_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/media"
	"travellog/internal/seed"
)

// signingStore presigns to a bucket the test is running, so the leg exercises
// the replay rather than the signature.
type signingStore struct {
	media.Store
	base    string
	headers map[string]string
}

func (s signingStore) PresignPut(_ context.Context, key media.Key, up media.Upload) (string, map[string]string, error) {
	return s.base + "/" + key.Traveller + "/" + key.Object, s.headers, nil
}

func anAsset(t *testing.T) seed.Asset {
	t.Helper()
	assets, err := seed.ReadImagery(imageryDir)
	if err != nil {
		t.Fatalf("ReadImagery: %v", err)
	}
	return assets[0]
}

func TestAnUploadReplaysEverySignedHeader(t *testing.T) {
	signed := map[string]string{
		"content-type":          "image/png",
		"content-length":        "529392",
		"if-none-match":         "*",
		"x-amz-checksum-sha256": "jfsgO8D4kGVadUUARGbaE0gq940htcbet70UJZKl080=",
	}
	var got http.Header
	var body int
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		n, _ := io.Copy(io.Discard, r.Body)
		body = int(n)
		w.WriteHeader(http.StatusOK)
	}))
	defer bucket.Close()

	asset := anAsset(t)
	done, err := seed.Upload(context.Background(),
		signingStore{base: bucket.URL, headers: signed}, bucket.Client(), "t-1", []seed.Asset{asset})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	for name, want := range signed {
		if got.Get(name) != want {
			t.Errorf("the bucket saw %s = %q, want %q — a header the signature covers and "+
				"the request does not replay is refused with AccessDenied on every upload",
				name, got.Get(name), want)
		}
	}
	if int64(body) != asset.Size {
		t.Errorf("the bucket received %d bytes, want %d", body, asset.Size)
	}
	if len(done) != 1 || done[0].AlreadyThere {
		t.Errorf("Upload reported %+v, want one upload that was not already there", done)
	}
}

func TestAnObjectAlreadyInTheBucketIsSuccess(t *testing.T) {
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer bucket.Close()

	done, err := seed.Upload(context.Background(),
		signingStore{base: bucket.URL, headers: map[string]string{}}, bucket.Client(),
		"t-1", []seed.Asset{anAsset(t)})
	if err != nil {
		t.Fatalf("a 412 is write-once answering that the bytes are their own address, "+
			"and a seed that treats it as a failure cannot be run twice: %v", err)
	}
	if len(done) != 1 || !done[0].AlreadyThere {
		t.Errorf("Upload reported %+v, want one object already in the bucket", done)
	}
}

func TestARefusedUploadNamesWhatItWasPutting(t *testing.T) {
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("InternalError"))
	}))
	defer bucket.Close()

	asset := anAsset(t)
	_, err := seed.Upload(context.Background(),
		signingStore{base: bucket.URL, headers: map[string]string{}}, bucket.Client(),
		"t-1", []seed.Asset{asset})
	if err == nil {
		t.Fatal("Upload answered no error for a bucket that refused, so the seed would " +
			"write rows addressing objects that are not there")
	}
	if !strings.Contains(err.Error(), asset.Locator) {
		t.Errorf("the refusal is %q and does not name %s", err, asset.Locator)
	}
}
