package media_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"travellog/internal/media"
)

const traveller = "0f9c1f36-2e5d-4a1c-9a3f-6d2b1c7e4a80"

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// ONE INPUT, TWO OUTPUTS, AND THEY CANNOT DIVERGE (DEC-88). The checksum binds
// the header to the BODY; this is the only thing binding it to the KEY.
func TestAddressDerivesTheKeyAndTheHeaderFromOneDigest(t *testing.T) {
	for _, body := range []string{
		"",
		"the bytes the client promised",
		strings.Repeat("a photograph", 4096),
	} {
		digest := digestOf(body)

		path, checksum, err := media.Address(traveller, digest)
		if err != nil {
			t.Fatalf("Address(%s): %v", digest, err)
		}

		if want := traveller + "/" + digest; path != want {
			t.Errorf("path %q, want %q — the traveller prefix IS the boundary; "+
				"the day a second traveller registers, a sweep that got this "+
				"wrong deletes somebody else's photographs", path, want)
		}

		// The header is base64 of the same bytes the hex names, and the two
		// encodings are not a style choice: MinIO answers 400 InvalidArgument
		// to a hex digest in this header, which is a different sentence from a
		// genuine mismatch.
		raw, err := base64.StdEncoding.DecodeString(checksum)
		if err != nil {
			t.Fatalf("the checksum header %q is not base64: %v", checksum, err)
		}
		if got := hex.EncodeToString(raw); got != digest {
			t.Errorf("the header decodes to %s and the key names %s — computed "+
				"from two variables, the address stops holding its own contents", got, digest)
		}
	}
}

// The path is the only thing keeping one traveller out of another's objects,
// so two travellers holding the SAME object must still get two paths.
func TestTwoTravellersNeverShareAPath(t *testing.T) {
	const other = "11111111-2222-3333-4444-555555555555"
	digest := digestOf("the same photograph, uploaded twice")

	mine, _, err := media.Address(traveller, digest)
	if err != nil {
		t.Fatalf("Address: %v", err)
	}
	theirs, _, err := media.Address(other, digest)
	if err != nil {
		t.Fatalf("Address: %v", err)
	}
	if mine == theirs {
		t.Fatalf("both travellers address %s", mine)
	}
	if !strings.HasPrefix(mine, traveller+"/") || !strings.HasPrefix(theirs, other+"/") {
		t.Fatalf("the prefix is not the traveller: %q / %q", mine, theirs)
	}
}

// WHAT Address REFUSES, and every row is a value that would otherwise become a
// path segment. The digest rows are migration 0001's own constraint in Go —
// `media_objects_id_sha256_ck CHECK (id ~ '^[0-9a-f]{64}$')` — and the two
// must agree or the bucket and the table disagree about what an id is.
func TestAddressRefusesAnythingThatIsNotAnIdOrATraveller(t *testing.T) {
	good := digestOf("fine")

	for _, c := range []struct {
		name      string
		traveller string
		digest    string
	}{
		{"uppercase hex", traveller, strings.ToUpper(good)},
		{"63 characters", traveller, good[:63]},
		{"65 characters", traveller, good + "0"},
		{"not hex at all", traveller, strings.Repeat("g", 64)},
		{"empty digest", traveller, ""},
		{"a path in the digest", traveller, "../" + good[3:]},
		{"empty traveller", "", good},
		{"a slug rather than a uuid", "matt", good},
		{"a path in the traveller", "../" + traveller, good},
		{"a traveller with a slash", traveller + "/x", good},
	} {
		t.Run(c.name, func(t *testing.T) {
			if path, checksum, err := media.Address(c.traveller, c.digest); err == nil {
				t.Fatalf("accepted, and answered %q / %q", path, checksum)
			}
		})
	}
}
