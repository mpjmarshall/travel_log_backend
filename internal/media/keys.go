package media

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// digestRe is the schema's own constraint, in Go.
var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// travellerRe is deliberately narrow: a uuid and nothing else.
var travellerRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Address turns one hex digest into's two things a signature needs: the
// object the path in the bucket and the base64 value of the checksum header.
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
		return "", "", fmt.Errorf("media: decoding %q: %w", hexDigest, err)
	}
	return traveller + "/" + hexDigest, base64.StdEncoding.EncodeToString(raw), nil
}

// The four header names, written once.
const (
	headerChecksum    = "x-amz-checksum-sha256"
	headerContentType = "content-type"
	headerLength      = "content-length"
	headerWriteOnce   = "if-none-match"

	writeOnce = "*"
)

// uploadHeaders is the map PresignPut hands back and the map the signature
// covers — one construction, so's two cannot drift.
func uploadHeaders(up Upload, checksum string) map[string]string {
	return map[string]string{
		headerChecksum:    checksum,
		headerContentType: up.ContentType,
		headerLength:      strconv.FormatInt(up.ByteSize, 10),
		headerWriteOnce:   writeOnce,
	}
}

// checkUpload refuses an Upload that disagrees with the key it is being
// signed against, or one that could not be uploaded at all.
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
