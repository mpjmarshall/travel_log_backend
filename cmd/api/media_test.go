// The bucket, at boot.
package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"travellog/internal/config"
	"travellog/internal/media"
)

func bucketConfig() config.Config {
	return config.Config{
		Development:         true,
		S3InternalEndpoint:  "http://minio:9000",
		S3PublicBaseURL:     "https://media.example.test",
		S3Region:            "eu-west-2",
		S3Bucket:            "travellog-media",
		S3AccessKey:         "an-access-key",
		S3SecretKey:         "a-secret-key",
		S3PresignTTLPrivate: 2 * time.Minute,
		S3PresignTTLPublic:  15 * time.Minute,
	}
}

func TestTheTwoBucketAddressesGoToTheirOwnHalves(t *testing.T) {
	got := mediaConfig(bucketConfig())

	for _, c := range []struct {
		what        string
		got, want   string
		ifItIsWrong string
	}{
		{"the dialled endpoint", got.InternalEndpoint, "http://minio:9000",
			"the API would try to reach the bucket at an address only a phone can resolve"},
		{"the signed base", got.PublicBaseURL, "https://media.example.test",
			"every presigned URL would be minted for a host no client outside the compose network can resolve, and a signature covers the host"},
		{"the region", got.Region, "eu-west-2",
			"a wrong region is a signature scope no server accepts — and an EMPTY one puts a network round trip in front of every cold presign"},
		{"the bucket", got.Bucket, "travellog-media", "there would be nowhere to put anything"},
		{"the access key", got.AccessKey, "an-access-key", "every request would be refused"},
		{"the secret key", got.SecretKey, "a-secret-key", "every signature would be wrong"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q — %s", c.what, c.got, c.want, c.ifItIsWrong)
		}
	}
}

// The two lifetimes, at the wiring site.
func TestTheTwoLifetimesAreNotSwappedOnTheWayIn(t *testing.T) {
	got := mediaConfig(bucketConfig())

	if got.TTLPrivate != 2*time.Minute {
		t.Errorf("the private lifetime is %s, want 2m — it is the revocation knob", got.TTLPrivate)
	}
	if got.TTLPublic != 15*time.Minute {
		t.Errorf("the public lifetime is %s, want 15m — four sentences of client "+
			"copy are written against it", got.TTLPublic)
	}
}

// boot talks to the bucket, and this is the leg that says so without a
// daemon.
func TestBootRefusesToComeUpWhenTheBucketCannotBeReached(t *testing.T) {
	cfg := bucketConfig()
	closed := closedPort(t)
	cfg.S3InternalEndpoint = "http://" + closed
	cfg.S3PublicBaseURL = "https://media.example.test"

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	store, err := mediaStore(ctx, cfg, quiet())
	if err == nil {
		t.Fatalf("boot succeeded against a port nothing is listening on, and "+
			"answered %T — so nothing at boot reaches the bucket, and a wrong "+
			"S3_SECRET_KEY or a missing bucket would first be seen on a phone", store)
	}
	if !strings.Contains(err.Error(), cfg.S3Bucket) {
		t.Errorf("the error does not name the bucket:\n%s", err)
	}
	if !strings.Contains(err.Error(), closed) {
		t.Errorf("the error does not name the address that was dialled:\n%s", err)
	}
	if strings.Contains(err.Error(), "media.example.test") {
		t.Errorf("boot dialled the SIGNED address rather than the dialled one:\n%s", err)
	}
}

// A store is still a store: what boot builds is what R3 will presign with.
func TestBootHandsBackSomethingThatSatisfiesTheInterface(t *testing.T) {
	var _ media.Store = (*media.MinIO)(nil)
}

// closedPort answers a host:port that was listening a moment ago and is not
// any more.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("closing the reserved port: %v", err)
	}
	return addr
}
