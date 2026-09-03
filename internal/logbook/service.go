// The one operation that is more than a store call: committing an upload,
// which reconciles what the bucket holds against what the row declares.
package logbook

import (
	"context"
	"errors"
	"fmt"

	"travellog/internal/media"
)

// ErrUploadIncomplete is a commit for an object the bucket does not hold, or
// holds as something other than what was declared.
var ErrUploadIncomplete = errors.New("logbook: the object has not landed in the bucket")

// Objects is the half of internal/media the commit path needs.
type Objects interface {
	Stat(ctx context.Context, key media.Key) (media.Attributes, error)
}

// CommitMedia is `POST /v1/media/{id}/commit`: head the bucket, verify what
// came back against what was declared, and set uploaded_at.
func CommitMedia(ctx context.Context, rows MediaStore, objects Objects, travellerID, id string) (MediaObject, error) {
	got, err := rows.MediaObjects(ctx, travellerID, []string{id})
	if err != nil {
		return MediaObject{}, err
	}
	if len(got) == 0 {
		return MediaObject{}, fmt.Errorf("%w: %s", ErrNoMediaObject, id)
	}
	declared := got[0]

	if declared.Committed() {
		return declared, nil
	}

	stored, err := objects.Stat(ctx, media.Key{Traveller: travellerID, Object: id})
	switch {
	case errors.Is(err, media.ErrNoSuchObject):
		return MediaObject{}, fmt.Errorf("%w: %s: %w", ErrUploadIncomplete, id, err)
	case err != nil:
		return MediaObject{}, fmt.Errorf("logbook: stat %s: %w", id, err)
	}

	if stored.Size != declared.ByteSize {
		return MediaObject{}, fmt.Errorf("%w: %s holds %d bytes and the row declares %d",
			ErrUploadIncomplete, id, stored.Size, declared.ByteSize)
	}
	if stored.SHA256 != id {
		return MediaObject{}, fmt.Errorf("%w: %s carries the stored digest %q, and the "+
			"address IS the digest — an EMPTY one is what an upload through a presign "+
			"call that signs only `host` leaves behind",
			ErrUploadIncomplete, id, stored.SHA256)
	}
	if stored.ContentType != declared.ContentType {
		return MediaObject{}, fmt.Errorf("%w: %s is stored as %q and the row declares %q",
			ErrUploadIncomplete, id, stored.ContentType, declared.ContentType)
	}

	return rows.MarkMediaUploaded(ctx, travellerID, id)
}
