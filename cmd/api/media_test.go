// The bucket, at boot.
//
// TWO THINGS ARE INVISIBLE FROM OUTSIDE THE PROCESS and both are guarded here,
// for the reason routes_test.go's limiter leg exists: a swapped pair gives a
// perfectly healthy server that is wrong in a way no status code reports.
//
//  1. WHICH ADDRESS FEEDS WHICH HALF (DEC-42). S3_INTERNAL_ENDPOINT is what
//     the API dials and S3_PUBLIC_BASE_URL is what a signature covers. Swap
//     them and every presigned URL is minted for `minio:9000`, which a phone
//     cannot resolve — and nothing on the server ever notices.
//  2. THAT BOOT ACTUALLY REACHES THE BUCKET (DEC-98). The mutation the plan
//     names is "delete EnsureBucket from main.go", and its stated guard is the
//     arc. This is the same mutation caught in `make check` with no daemon:
//     against a port nothing is listening on, a boot that only CONSTRUCTS a
//     client succeeds, and a boot that creates the bucket cannot.
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

// THE TWO LIFETIMES, AT THE WIRING SITE (DEC-47, DEC-84). internal/media's own
// legs assert that each audience gets its own; this asserts that the two
// numbers arrive the right way round from config, which is the other end of
// the same wire and is not implied by either half.
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

// BOOT TALKS TO THE BUCKET, and this is the leg that says so without a daemon.
//
// Against a port nothing is listening on, media.New succeeds — building a
// client is offline, and so is presigning once the region is pinned — so a
// boot that stopped there would come up perfectly healthy and 500 on every
// media route. Only EnsureBucket touches the network. Delete that call and
// this leg goes green with nothing having been checked, which is why the
// assertion is on the ERROR and not on the absence of one.
//
// AND IT IS THE INTERNAL ENDPOINT THAT IS DIALLED, not the signed one: the
// error names the closed port and never the public base. The reverse would
// mean the API is reaching for the bucket by the address the phone uses,
// which works on a laptop and not inside compose.
//
// THE BUDGET IS 1.5s AND NOT bucketTimeout's 10s, because minio-go RETRIES a
// refused connection with backoff rather than giving up on it — measured, a
// closed port consumed the whole context both times — so this leg pays its
// budget in full on every run and the gate is four commands and stays fast.
// The elapsed-versus-budget half of the message is there for exactly that
// reason: at boot the same shape is a container that waited ten seconds, and
// "waited 250ms of a 10s budget" and "waited 10s" are different problems.
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
// any more, which is the only reliable way to name a port nothing will answer.
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
