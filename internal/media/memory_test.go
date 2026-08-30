package media_test

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"travellog/internal/media"
)

// upload is the honest triple for a body, built the way a handler will build
// it: one digest, and everything else derived from it.
func upload(body string, contentType string) (media.Key, media.Upload, []byte) {
	digest := digestOf(body)
	return media.Key{Traveller: traveller, Object: digest},
		media.Upload{SHA256: digest, ByteSize: int64(len(body)), ContentType: contentType},
		[]byte(body)
}

// The two-variable mistake is a state this api can express, and this is where
// it reddens.
func TestPresignPutRefusesADigestThatDisagreesWithTheKey(t *testing.T) {
	store := media.NewMemory()
	key, up, _ := upload("the bytes the client promised", "image/png")

	up.SHA256 = digestOf("entirely different bytes")
	if url, headers, err := store.PresignPut(t.Context(), key, up); err == nil {
		t.Fatalf("a URL was minted for a key and a digest that disagree: %q / %v", url, headers)
	}
}

// The same three refusals a signature makes real, at the seam that has no
// signature.
func TestPresignPutRefusesWhatCannotBeSigned(t *testing.T) {
	store := media.NewMemory()

	for _, c := range []struct {
		name string
		edit func(*media.Upload)
	}{
		{"a zero length", func(u *media.Upload) { u.ByteSize = 0 }},
		{"a negative length", func(u *media.Upload) { u.ByteSize = -1 }},
		{"no content type", func(u *media.Upload) { u.ContentType = "  " }},
	} {
		t.Run(c.name, func(t *testing.T) {
			key, up, _ := upload("a photograph", "image/png")
			c.edit(&up)
			if _, _, err := store.PresignPut(t.Context(), key, up); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// The header map is the same four names at both seams, so a handler leg
// written against Memory is a leg about the real set.
func TestTheTwinHandsBackTheSameFourHeaders(t *testing.T) {
	store := media.NewMemory()
	key, up, _ := upload("a photograph", "image/png")

	url, headers, err := store.PresignPut(t.Context(), key, up)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	var got []string
	for name := range headers {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"content-length", "content-type", "if-none-match", "x-amz-checksum-sha256"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("uploadHeaders has %v, want %v", got, want)
	}

	if headers["content-length"] != "12" {
		t.Errorf("content-length is %q for a 12-byte body", headers["content-length"])
	}
	if headers["if-none-match"] != "*" {
		t.Errorf("if-none-match is %q; `*` is what makes the address write-once",
			headers["if-none-match"])
	}
	if headers["content-type"] != "image/png" {
		t.Errorf("content-type is %q, and it must be the value the allowlist "+
			"already passed", headers["content-type"])
	}

	signed, err := media.SignedHeaders(url)
	if err != nil {
		t.Fatalf("SignedHeaders: %v", err)
	}
	var minusHost []string
	for _, name := range signed {
		if name != "host" {
			minusHost = append(minusHost, name)
		}
	}
	sort.Strings(minusHost)
	if strings.Join(minusHost, ";") != strings.Join(got, ";") {
		t.Fatalf("the twin's URL covers %v and its map has %v", minusHost, got)
	}
}

// Lifetime each audience gets, at the seam with no daemon.
func TestEachAudienceGetsItsOwnLifetimeInTheTwin(t *testing.T) {
	store := media.NewMemory()
	store.TTL[media.Private] = 2 * time.Minute
	store.TTL[media.Public] = 15 * time.Minute
	key, _, _ := upload("a photograph", "image/png")

	for _, c := range []struct {
		aud  media.Audience
		want string
	}{
		{media.Private, "X-Amz-Expires=120"},
		{media.Public, "X-Amz-Expires=900"},
	} {
		url, err := store.PresignGet(t.Context(), key, c.aud)
		if err != nil {
			t.Fatalf("PresignGet(%s): %v", c.aud, err)
		}
		if !strings.Contains(url, c.want) {
			t.Errorf("the %s audience minted %s, want %s", c.aud, url, c.want)
		}
	}
}

// The twin refuses what minio refuses, or a handler leg proves nothing.
func TestTheTwinEnforcesTheFourRefusals(t *testing.T) {
	key, up, body := upload("the bytes the client promised", "image/png")

	t.Run("NoSuchBucket before EnsureBucket", func(t *testing.T) {
		store := media.NewMemory()
		if err := store.Put(key, up, body); err == nil ||
			!strings.Contains(err.Error(), "NoSuchBucket") {
			t.Fatalf("a Put into a bucket nobody made answered %v — DEC-98's whole "+
				"point is that this is the state a fresh MinIO is in", err)
		}
	})

	t.Run("XAmzContentChecksumMismatch", func(t *testing.T) {
		store := media.NewMemory()
		if err := store.EnsureBucket(t.Context()); err != nil {
			t.Fatal(err)
		}
		lying := []byte(strings.Repeat("x", len(body)))
		if err := store.Put(key, up, lying); err == nil ||
			!strings.Contains(err.Error(), "XAmzContentChecksumMismatch") {
			t.Fatalf("bytes that are not the address answered %v", err)
		}
	})

	t.Run("SignatureDoesNotMatch on the length", func(t *testing.T) {
		store := media.NewMemory()
		if err := store.EnsureBucket(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(key, up, append(body, 'x')); err == nil ||
			!strings.Contains(err.Error(), "SignatureDoesNotMatch") {
			t.Fatalf("one byte over the signed length answered %v", err)
		}
	})

	t.Run("PreconditionFailed on the second write", func(t *testing.T) {
		store := media.NewMemory()
		if err := store.EnsureBucket(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(key, up, body); err != nil {
			t.Fatalf("the first Put: %v", err)
		}
		if err := store.Put(key, up, body); err == nil ||
			!strings.Contains(err.Error(), "PreconditionFailed") {
			t.Fatalf("the second Put at a committed address answered %v — and the "+
				"client's retry story reads this as SUCCESS, not failure", err)
		}
		got, err := store.Stat(t.Context(), key)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got.Size != int64(len(body)) || got.SHA256 != key.Object {
			t.Fatalf("Stat answered %+v", got)
		}
	})
}

// ErrNoSuchObject is a sentinel because R3's commit has to tell "not landed"
// (a 409) from "unreachable" (a 503), and both arrive through Stat.
func TestStatAnswersTheSentinelForAKeyThatIsNotThere(t *testing.T) {
	store := media.NewMemory()
	if err := store.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	key, _, _ := upload("never uploaded", "image/png")

	_, err := store.Stat(t.Context(), key)
	if !errors.Is(err, media.ErrNoSuchObject) {
		t.Fatalf("Stat answered %v, which errors.Is(media.ErrNoSuchObject) does not match", err)
	}
}

// The twin's read URLs carry the disposition, because the real one's do.
func TestTheTwinsReadURLsAreMarkedAsDownloadsForBothAudiences(t *testing.T) {
	m := media.NewMemory()
	key := media.Key{Traveller: traveller, Object: digestOf("a photograph")}

	for _, aud := range []media.Audience{media.Private, media.Public} {
		raw, err := m.PresignGet(context.Background(), key, aud)
		if err != nil {
			t.Fatalf("PresignGet(%s): %v", aud, err)
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parsing %s: %v", raw, err)
		}
		if got := parsed.Query().Get("response-content-disposition"); got != "attachment" {
			t.Errorf("the %s URL carries response-content-disposition=%q, want attachment",
				aud, got)
		}
	}
}

// ExpiresIn reads the window back off the url, which is what gives the begin
// response's `expiresAt` one source.
func TestExpiresInReadsTheWindowTheURLCarries(t *testing.T) {
	m := media.NewMemory()
	key := media.Key{Traveller: traveller, Object: digestOf("a photograph")}

	raw, err := m.PresignGet(context.Background(), key, media.Public)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	got, err := media.ExpiresIn(raw)
	if err != nil {
		t.Fatalf("ExpiresIn: %v", err)
	}
	if want := m.TTL[media.Public]; got != want {
		t.Errorf("ExpiresIn = %s, want %s", got, want)
	}

	for _, bad := range []string{
		"https://memory.invalid/x",
		"https://memory.invalid/x?X-Amz-Expires=",
		"https://memory.invalid/x?X-Amz-Expires=soon",
		"https://memory.invalid/x?X-Amz-Expires=0",
		"https://memory.invalid/x?X-Amz-Expires=-5",
	} {
		if _, err := media.ExpiresIn(bad); err == nil {
			t.Errorf("ExpiresIn(%q) answered no error", bad)
		}
	}
}
