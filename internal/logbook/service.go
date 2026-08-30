// The Service layer, written for exactly three operations and for nothing
// else.
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

// Service is's three operations.
type Service struct {
	Media   MediaStore
	Objects Objects

	Places PlaceStore

	Photos PhotoStore
}

// RemovePlace is D2, and it is the second of's three operations named.
func (s Service) RemovePlace(ctx context.Context, travellerID, placeID string, photos PhotoDisposition) (Snapshot, error) {
	if s.Places == nil {
		return Snapshot{}, errors.New("logbook: the place service has no store")
	}
	switch photos {
	case KeepPhotos, DeletePhotos:
	default:
		_, err := ParsePhotoDisposition(photos.String())
		return Snapshot{}, err
	}
	return s.Places.RemovePlace(ctx, travellerID, placeID, photos == DeletePhotos)
}

// RefilePhoto is M2.2's 'Change', and it is the third and last of the
// operations named.
func (s Service) RefilePhoto(ctx context.Context, travellerID, photoID string, w RefileWrite) (PhotoRefiled, error) {
	if s.Photos == nil {
		return PhotoRefiled{}, errors.New("logbook: the photo service has no store")
	}
	if w.PlaceID == nil {
		return PhotoRefiled{}, InvalidFieldError{Field: "placeId",
			Why: "a re-file names the pin it is filing to; M2.2 lists the pins in the " +
				"photograph's own city and there is no 'nowhere' among them"}
	}
	if w.VisitID == nil {
		return PhotoRefiled{}, InvalidFieldError{Field: "visitId",
			Why: "a re-file names the OCCASION as well as the pin, and the client is what " +
				"chooses it — a place can be visited more than once on one trip (the " +
				"fixture visits Nishiki four times in one day), so a server picking for " +
				"itself would file the photograph to whichever row the planner returned. " +
				"Send the id of an existing occasion, or a fresh id and `visitAt` to " +
				"open a new one"}
	}
	return s.Photos.RefilePhoto(ctx, travellerID, photoID, w)
}

// CommitMedia is `POST /v1/media/{id}/commit`: head the bucket, verify what
// came back against what was declared, and set uploaded_at.
func (s Service) CommitMedia(ctx context.Context, travellerID, id string) (MediaObject, error) {
	if s.Media == nil || s.Objects == nil {
		return MediaObject{}, errors.New("logbook: the media service has no store")
	}

	rows, err := s.Media.MediaObjects(ctx, travellerID, []string{id})
	if err != nil {
		return MediaObject{}, err
	}
	if len(rows) == 0 {
		return MediaObject{}, fmt.Errorf("%w: %s", ErrNoMediaObject, id)
	}
	declared := rows[0]

	if declared.Committed() {
		return declared, nil
	}

	got, err := s.Objects.Stat(ctx, media.Key{Traveller: travellerID, Object: id})
	switch {
	case errors.Is(err, media.ErrNoSuchObject):
		return MediaObject{}, fmt.Errorf("%w: %s: %w", ErrUploadIncomplete, id, err)
	case err != nil:
		return MediaObject{}, fmt.Errorf("logbook: stat %s: %w", id, err)
	}

	if got.Size != declared.ByteSize {
		return MediaObject{}, fmt.Errorf("%w: %s holds %d bytes and the row declares %d",
			ErrUploadIncomplete, id, got.Size, declared.ByteSize)
	}
	if got.SHA256 != id {
		return MediaObject{}, fmt.Errorf("%w: %s carries the stored digest %q, and the "+
			"address IS the digest — an EMPTY one is what an upload through a presign "+
			"call that signs only `host` leaves behind",
			ErrUploadIncomplete, id, got.SHA256)
	}
	if got.ContentType != declared.ContentType {
		return MediaObject{}, fmt.Errorf("%w: %s is stored as %q and the row declares %q",
			ErrUploadIncomplete, id, got.ContentType, declared.ContentType)
	}

	return s.Media.MarkMediaUploaded(ctx, travellerID, id)
}
