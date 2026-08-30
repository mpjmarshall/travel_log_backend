// What a trip write may contain, and the compiled regexps that decide it (.
package logbook

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxNameBytes and MaxSummaryBytes are policy, not schema.
const (
	MaxNameBytes    = 200
	MaxSummaryBytes = 4096
)

// idPattern is the rule, verified against all 43 ids in the client's own log:
// permissive, because the ids are the client's and must round-trip.
var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// assetPattern is the content address: lowercase hex, sixty-four characters.
var assetPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// InvalidFieldError is what the one additive key, `field`, is built from.
type InvalidFieldError struct{ Field, Why string }

func (e InvalidFieldError) Error() string { return "logbook: " + e.Field + ": " + e.Why }

// TripWrite is the body of PUT /v1/trips/{id}, and every field is a pointer
// Absent means leave alone.
type TripWrite struct {
	ID         *string   `json:"id"`
	Name       *string   `json:"name"`
	CityIDs    *[]string `json:"cityIds"`
	Start      **Instant `json:"start"`
	End        **Instant `json:"end"`
	Summary    **string  `json:"summary"`
	CoverAsset **string  `json:"coverAsset"`
}

// Sent answers whether a nullable field was in the body at all, which is the
// question every case when in the upsert asks.
func Sent[T any](p **T) bool { return p != nil }

// Value flattens a sent nullable field to the value the statement writes: nil
// for a sent null, the pointer otherwise.
func Value[T any](p **T) *T {
	if p == nil {
		return nil
	}
	return *p
}

// ValidateTrip answers's first field that is wrong, and nothing about
// whether the ids it names exist.
func ValidateTrip(t TripWrite) error {
	if t.ID == nil || !idPattern.MatchString(*t.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if t.Name != nil {
		if strings.TrimSpace(*t.Name) == "" {
			return InvalidFieldError{Field: "name", Why: "a trip needs a name"}
		}
		if len(*t.Name) > MaxNameBytes {
			return InvalidFieldError{Field: "name",
				Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*t.Name), MaxNameBytes)}
		}
	}
	if summary := Value(t.Summary); summary != nil && len(*summary) > MaxSummaryBytes {
		return InvalidFieldError{Field: "summary",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*summary), MaxSummaryBytes)}
	}
	if t.CityIDs != nil {
		if err := checkCityIDs(*t.CityIDs); err != nil {
			return err
		}
	}
	if cover := Value(t.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	start, end := Value(t.Start), Value(t.End)
	if start != nil && end != nil && end.Time().Before(start.Time()) {
		return InvalidFieldError{Field: "end", Why: "a trip cannot end before it starts"}
	}
	return nil
}

func checkCityIDs(ids []string) error {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return InvalidFieldError{Field: "cityIds", Why: fmt.Sprintf("%q is not an id", id)}
		}
		if seen[id] {
			return InvalidFieldError{Field: "cityIds",
				Why: fmt.Sprintf("%q appears twice, and a trip visits a city once in its ordered list", id)}
		}
		seen[id] = true
	}
	return nil
}

// allowedContentTypes is the one runtime list, and everything else about the
// allowlist is derived from it ( as narrowed by, ).
var allowedContentTypes = []string{"image/jpeg", "image/png"}

// contentTypePattern is `^image/(jpeg|png)$`.
var contentTypePattern = regexp.MustCompile(contentTypeExpression(allowedContentTypes))

// contentTypeExpression turns the allowlist into the anchored alternation.
func contentTypeExpression(allowed []string) string {
	const family = "image/"
	subtypes := make([]string, len(allowed))
	for i, mediaType := range allowed {
		subtypes[i] = strings.TrimPrefix(mediaType, family)
	}
	return "^" + family + "(" + strings.Join(subtypes, "|") + ")$"
}

// AllowedContentTypes is the allowlist, as a fresh slice each call — a caller
// holding this package's own backing array could reorder or rewrite it.
func AllowedContentTypes() []string {
	out := make([]string, len(allowedContentTypes))
	copy(out, allowedContentTypes)
	return out
}

// ContentTypeAllowed is the one question every caller asks.
func ContentTypeAllowed(mediaType string) bool {
	return contentTypePattern.MatchString(mediaType)
}

// MediaBegin is the body of `POST /v1/media`, and every field is a pointer
// Absent means leave alone.
type MediaBegin struct {
	SHA256      *string `json:"sha256"`
	ByteSize    *int64  `json:"byteSize"`
	ContentType *string `json:"contentType"`
}

// ValidateMediaBegin answers's first field that is wrong.
func ValidateMediaBegin(b MediaBegin, maxBytes int64) error {
	if b.SHA256 == nil || !assetPattern.MatchString(*b.SHA256) {
		return InvalidFieldError{Field: "sha256",
			Why: "a content address is 64 lowercase hex characters"}
	}
	if b.ContentType == nil || !ContentTypeAllowed(*b.ContentType) {
		return InvalidFieldError{Field: "contentType",
			Why: fmt.Sprintf("this build stores %s and nothing else",
				strings.Join(allowedContentTypes, " and "))}
	}
	if b.ByteSize == nil || *b.ByteSize <= 0 {
		return InvalidFieldError{Field: "byteSize",
			Why: "a photograph has a positive size, and SigV4 signs an exact length"}
	}
	if *b.ByteSize > maxBytes {
		return InvalidFieldError{Field: "byteSize",
			Why: fmt.Sprintf("%d bytes, and this build mints an upload for at most %d",
				*b.ByteSize, maxBytes)}
	}
	return nil
}

// MediaMint is the body of `POST /v1/media/mint`: a list of ids, so a
// twelve-photograph grid is one round trip rather than twelve.
type MediaMint struct {
	IDs *[]string `json:"ids"`
}

// MaxMintIDs bounds one mint request, and it is this build's policy rather
// than schema — the same sense MaxNameBytes is.
const MaxMintIDs = 100

// ValidateMediaMint answers's first thing that is wrong with a mint
// request.
func ValidateMediaMint(m MediaMint) error {
	if m.IDs == nil {
		return InvalidFieldError{Field: "ids", Why: "a mint takes a list of media object ids"}
	}
	if len(*m.IDs) == 0 {
		return InvalidFieldError{Field: "ids", Why: "a mint of nothing has no answer to give"}
	}
	if len(*m.IDs) > MaxMintIDs {
		return InvalidFieldError{Field: "ids",
			Why: fmt.Sprintf("%d ids, and this build mints at most %d in one request",
				len(*m.IDs), MaxMintIDs)}
	}
	for _, id := range *m.IDs {
		if !assetPattern.MatchString(id) {
			return InvalidFieldError{Field: "ids",
				Why: fmt.Sprintf("%q is not a media object id: 64 lowercase hex characters", id)}
		}
	}
	return nil
}

// ValidateMediaID is the guard on an id arriving through route arguments
// Than through a body.
func ValidateMediaID(id string) error {
	if !assetPattern.MatchString(id) {
		return InvalidFieldError{Field: "id",
			Why: "a media object id is 64 lowercase hex characters"}
	}
	return nil
}
