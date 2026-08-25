package media

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// digestRe is the schema's own constraint, in Go. migration 0001 carries
// `media_objects_id_sha256_ck CHECK (id ~ '^[0-9a-f]{64}$')`, and the two must
// agree or the bucket and the table disagree about what an id is.
var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// travellerRe is deliberately narrow: a uuid and nothing else. The traveller
// segment is a PATH segment, so anything that could carry a `/`, a `.` or a
// `%` would let one traveller's key reach outside their own prefix.
var travellerRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Address turns ONE hex digest into the two things a signature needs: the
// object's path in the bucket and the base64 value of the checksum header.
//
// IT IS ONE FUNCTION BECAUSE THEY MUST NOT BE ABLE TO DIVERGE (DEC-88). The
// checksum binds the header to the BODY; nothing binds the header to the KEY,
// and that is this function's whole job. Measured, before it existed: a PUT to
// key <traveller>/<digestA> carrying a self-consistent checksum for entirely
// different bytes B succeeds with 200, and the address then holds bytes whose
// digest is not the address. Compute the two from two variables and the guard
// is gone with the suite still green.
//
// THE TWO ENCODINGS ARE NOT A STYLE CHOICE. `x-amz-checksum-sha256` is base64
// by the S3 protocol and the id is hex everywhere else in this system —
// measured: an honest body with the HEX digest in the header answers
// `400 InvalidArgument` ("Invalid checksum provided."), which is a different
// sentence from a genuine mismatch and is why the legs assert the code. So
// base64 exists in exactly two places: inside this function, and inside the
// uploadHeaders map the client replays verbatim.
func Address(traveller, hexDigest string) (path, checksum string, err error) {
	if !travellerRe.MatchString(traveller) {
		return "", "", fmt.Errorf("media: %q is not a traveller uuid, and the "+
			"traveller is a path segment", traveller)
	}
	if !digestRe.MatchString(hexDigest) {
		return "", "", fmt.Errorf("media: %q is not 64 lowercase hex characters, "+
			"which is what media_objects.id is constrained to", hexDigest)
	}
	raw, err := hex.DecodeString(hexDigest)
	if err != nil {
		// Unreachable while digestRe holds; kept because a regexp and a
		// decoder disagreeing is exactly the class this function exists for.
		return "", "", fmt.Errorf("media: decoding %q: %w", hexDigest, err)
	}
	return traveller + "/" + hexDigest, base64.StdEncoding.EncodeToString(raw), nil
}

// The four header names, written once. They are LOWERCASE on purpose: the
// URL's X-Amz-SignedHeaders is lowercase, the leg that keeps the map honest
// compares the two key sets directly, and HTTP header names are
// case-insensitive on the wire so a client replaying them verbatim is
// correct.
const (
	headerChecksum    = "x-amz-checksum-sha256"
	headerContentType = "content-type"
	headerLength      = "content-length"
	headerWriteOnce   = "if-none-match"

	// writeOnce is `*`, which reads as "only if nothing is there". It is the
	// whole of DEC-88's second half: first PUT 200, any second 412
	// PreconditionFailed with the original bytes intact.
	writeOnce = "*"
)

// uploadHeaders is the map PresignPut hands back and the map the signature
// covers — ONE construction, so the two cannot drift. Both Store
// implementations call it, which is what lets the handler legs in R3 run
// against Memory and still be about the real header set.
func uploadHeaders(up Upload, checksum string) map[string]string {
	return map[string]string{
		headerChecksum:    checksum,
		headerContentType: up.ContentType,
		headerLength:      strconv.FormatInt(up.ByteSize, 10),
		headerWriteOnce:   writeOnce,
	}
}

// checkUpload refuses an Upload that disagrees with the key it is being signed
// against, or one that could not be uploaded at all.
//
// THE FIRST CHECK IS THE ONE DEC-88 ASKS FOR. Upload.SHA256 and Key.Object are
// two variables holding one fact, and the ruling wants the two-variable
// mistake to be a state a leg can redden rather than one the types forbid.
// This is where it reddens.
func checkUpload(key Key, up Upload) error {
	if up.SHA256 != key.Object {
		return fmt.Errorf("media: the upload declares sha256 %q and the key "+
			"addresses %q; a content address whose signature covers a different "+
			"digest is exactly the poisoning If-None-Match and the checksum "+
			"exist to stop", up.SHA256, key.Object)
	}
	if up.ByteSize <= 0 {
		return fmt.Errorf("media: byteSize is %d; SigV4 signs an exact length "+
			"and a non-positive one cannot be uploaded", up.ByteSize)
	}
	if strings.TrimSpace(up.ContentType) == "" {
		return fmt.Errorf("media: contentType is empty; DEC-87 signs it, so an " +
			"empty one is a signature the client cannot satisfy")
	}
	return nil
}
