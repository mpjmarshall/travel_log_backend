// Package media is the seam onto S3-compatible object storage: the API mints
// short-lived signed URLs and never sees a photograph's bytes.
//
// WHY THE BYTES NEVER COME THROUGH HERE (DEC-36). A proxied upload occupies an
// HTTP handler for megabytes the API has no reason to touch, and that is the
// contention `DB_MAX_OPEN_CONNS` is sized against in the first place. A
// presigned PUT means the API handles two small JSON requests per upload.
//
// WHAT A SIGNATURE HAS TO COVER, AND WHY EACH ONE IS THERE. The URL this
// package mints for an upload is a capability, and everything the capability
// is allowed to do has to be inside the signature or it is not bounded at all:
//
//   - THE DIGEST (`x-amz-checksum-sha256`, DEC-38). The object's key IS its
//     sha256, so a client that could write arbitrary bytes at that address
//     would poison it for every later reader. Measured against real MinIO:
//     with the digest signed, mismatched bytes answer
//     `400 XAmzContentChecksumMismatch` and NOTHING is written.
//   - THE LENGTH (`content-length`, DEC-51). What SigV4 can express is an
//     EXACT value, never a ceiling — see the comment on Upload.ByteSize, which
//     is the one place this distinction is written out.
//   - THE TYPE (`content-type`, DEC-87). The Go 422 and the schema CHECK both
//     constrain the DATABASE ROW; without this header the bucket keeps
//     whatever the uploader sent, so a row reading `image/png` could address
//     an object served as `text/html`.
//   - THE WRITE-ONCE (`if-none-match: *`, DEC-88). The digest binds the header
//     to the BODY and nothing bound it to the KEY. With versioning off a
//     second PUT at a committed key silently REPLACES the bytes and the
//     address stops holding its own contents.
//
// WHAT THIS PACKAGE DELIBERATELY CANNOT DO. There is no method that deletes an
// object (OE-12). The sweep that would call one is out of scope, and its shape
// is not knowable until the liveness query exists — an object is live iff some
// photograph's `asset` equals its id, and no step before R7 can ask that. The
// consequence is written down rather than left implicit: nothing in R1-R8
// reclaims an object, so a successful upload that is never committed stays in
// the bucket for ever. docs/BEFORE-A-PUBLIC-DEPLOY.md carries the arithmetic
// and the deferred answer.
package media

import (
	"context"
	"errors"
)

// ErrNoSuchObject is what Stat answers for a key the bucket does not hold. It
// is a sentinel rather than a bare error because the commit path in R3 has to
// tell "the upload has not landed" (a 409 the client can act on) from "the
// bucket is unreachable" (a 503), and the two arrive through the same call.
var ErrNoSuchObject = errors.New("media: no such object")

// Key addresses one object. It is TWO fields and not one string, so the
// traveller prefix cannot be forgotten at a call site: the bucket is keyed the
// same way the table is, and the day a second traveller registers, a sweep
// that got this wrong deletes somebody else's photographs.
type Key struct {
	// Traveller is the owning traveller's uuid.
	Traveller string
	// Object is the object's 64-character lowercase hex sha256, which is also
	// its id on the wire and in media_objects.
	Object string
}

// Upload is everything besides the key that a PUT signature covers.
type Upload struct {
	// SHA256 is the same hex digest as Key.Object, and PresignPut REFUSES a
	// disagreement rather than picking one (DEC-88). Both fields exist so that
	// "the key and the header were computed from two variables" is a state
	// this API can express and a leg can redden — which is what the ruling
	// asks for. What makes them agree is Address, one function, one input.
	SHA256 string

	// ByteSize is the exact length of the body, and EXACT is the word.
	//
	// DEC-51 asked for `content-length` SIGNED INTO the presigned PUT "so the
	// BUCKET enforces it rather than the API hoping". What SigV4 can express
	// is a header VALUE, not a range — so the bucket enforces `== ByteSize`
	// and can never enforce `<= MEDIA_MAX_BYTES`. BOTH SENTENCES ARE NEEDED
	// AND NEITHER IS TRUE ALONE: MEDIA_MAX_BYTES is an API-side refusal to
	// MINT, taken before the capability exists, and this is what stops the
	// minted capability being an unbounded write. Measured: signed 29, PUT
	// 29,000 -> 403 SignatureDoesNotMatch with nothing stored; PUT exactly 29
	// -> 200; chunked, with no length at all -> 411 MissingContentLength.
	//
	// A real range needs a presigned POST policy, which is a different client
	// contract and a different step; PostPolicy.SetContentLengthRange is where
	// that conversation would start.
	ByteSize int64

	// ContentType is the media type the object will be stored and served as,
	// and it MUST be the value that already passed the API's own allowlist —
	// signing an unvalidated one would make the signature the allowlist's
	// third disagreeing copy rather than its reach into the bucket (DEC-87).
	ContentType string
}

// Attributes is what the bucket says about an object that is there.
type Attributes struct {
	Size        int64
	ContentType string

	// SHA256 is the digest the BUCKET stored, in hex, and it is empty when the
	// object carries none. That emptiness is load-bearing: an object uploaded
	// through either of the two BANNED presign calls (ban_test.go names them,
	// and says why they are not written out in this directory) carries no
	// checksum at all — so a commit that requires a non-empty matching value
	// turns the ban from a grep into a runtime guard. It costs no extra call
	// — StatObject returns it — but it does need `Checksum: true`, without
	// which the field comes back empty and the check silently passes nothing.
	SHA256 string
}

// Audience picks which of DEC-47's two presign lifetimes a read URL gets.
//
// IT IS AN ENUM AND NOT A time.Duration PARAMETER, and that is the whole of
// DEC-84's mandated leg. A duration parameter makes "the handler reached for
// the private lifetime where the public one belongs" a plausible-looking
// expression at the call site that no leg can see; a named audience makes it
// one wrong word, greppable, and assertable by reading X-Amz-Expires back off
// the URL the signer produced. v7.1's leg compared the two configured values
// to each other, which could not fail in the way that matters.
type Audience int

const (
	// Private is the phone's own read, through POST /v1/media/mint. It is the
	// revocation knob (DEC-44) and it is short — roughly two minutes — because
	// the client re-fetches constantly and caches bytes by object id, so a
	// short window costs it nothing.
	Private Audience = iota

	// Public is what GET /l/{token} embeds, at fifteen minutes (DEC-84). The
	// envelope has nothing to re-mint it with, so the same two minutes would
	// make a shared page unreadable mid-scroll.
	//
	// THE HONEST SENTENCE THE CLIENT COPY CARRIES: stopping a share stops new
	// links at once, and a photograph already loaded may keep working for up
	// to fifteen minutes.
	Public
)

func (a Audience) String() string {
	if a == Public {
		return "public"
	}
	return "private"
}

// Store is what the rest of the application sees. Two implementations: MinIO,
// which is the only thing in this repository that talks to a bucket, and
// Memory, which is what handler-level legs run against.
//
// ctx is first on every method because spec L22 asks for it and because every
// one of them can block on a network — including PresignPut, which looks like
// pure arithmetic and is not until the client's region is pinned. See
// minio.go's New.
type Store interface {
	// EnsureBucket creates the bucket if it is not there and is a no-op if it
	// is. It is called AT BOOT, and the reason is DEC-98: nothing else creates
	// it, the official image auto-creates nothing, and both healthchecks
	// report a perfectly healthy stack against a MinIO that cannot store
	// anything.
	EnsureBucket(ctx context.Context) error

	// PresignPut mints an upload capability and answers the URL together with
	// EXACTLY the headers the signature covers, already encoded.
	//
	// THE HEADER MAP IS NOT A CONVENIENCE (DEC-88). A presigned URL whose
	// signature covers extra headers is unusable unless the caller replays
	// each one byte-for-byte, and the digest is base64 there while the id is
	// hex everywhere else. A client handed only the URL gets 400 on every
	// upload for ever, with no way to derive the encoding. The map's key set
	// equals the URL's X-Amz-SignedHeaders minus `host`, and a leg holds the
	// two together.
	PresignPut(ctx context.Context, key Key, up Upload) (url string, headers map[string]string, err error)

	// PresignGet mints a read capability for the given audience.
	PresignGet(ctx context.Context, key Key, aud Audience) (string, error)

	// Stat answers what the bucket holds at a key, or ErrNoSuchObject.
	Stat(ctx context.Context, key Key) (Attributes, error)
}
