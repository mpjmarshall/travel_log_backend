// The storage contract: declared by the domain, satisfied by internal/postgres.
package logbook

import (
	"context"
	"errors"
	"time"
)

// ErrNoTraveller is a read or a write for a traveller row that is not there.
var ErrNoTraveller = errors.New("logbook: no such traveller")

// ErrNoTrip is a write answering about a trip nothing holds.
var ErrNoTrip = errors.New("logbook: no such trip")

// ErrNoPlace is a place write answering about a row nothing holds.
var ErrNoPlace = errors.New("logbook: no such place")

// ErrNoPhoto is a route naming a photograph this log does not hold.
var ErrNoPhoto = errors.New("logbook: no such photograph")

// ErrNoWalk is the same for a walk, reachable only through a route's re-read.
var ErrNoWalk = errors.New("logbook: no such walk")

// Snapshot is what one read saw.
type Snapshot struct {
	Version  int64
	Document *Document
}

type Store interface {
	Read(ctx context.Context, travellerID string, assemble func(version int64) bool) (Snapshot, error)
	PutTrip(ctx context.Context, travellerID string, w TripWrite) (Trip, int64, error)

	DeleteTrip(ctx context.Context, travellerID, tripID string) (Snapshot, error)

	SetTravellerName(ctx context.Context, travellerID, name string) (Traveller, int64, error)
}

// CityWritten is what PUT /v1/cities/{id} answers, in either of its two shapes.
type CityWritten struct {
	City     City
	Document *Document
	Version  int64
}

// CityStore is T5's 'Add a city'.
type CityStore interface {
	PutCity(ctx context.Context, travellerID string, w CityWrite) (CityWritten, error)
}

// PlaceStore is C1's pin and D2's removal.
type PlaceStore interface {
	PutPlace(ctx context.Context, travellerID string, w PlaceWrite) (Place, int64, error)

	RemovePlace(ctx context.Context, travellerID, placeID string, deletePhotos bool) (Snapshot, error)
}

// PhotoStore is the four photograph routes.
type PhotoStore interface {
	PutPhoto(ctx context.Context, travellerID string, w PhotoWrite) (Photo, int64, error)

	DeletePhoto(ctx context.Context, travellerID, photoID string) (int64, error)

	SnoozePhotos(ctx context.Context, travellerID string, w SnoozeWrite) ([]Photo, int64, error)

	RefilePhoto(ctx context.Context, travellerID, photoID string, w RefileWrite) (PhotoRefiled, error)
}

// WalkStore is N1's two walk writes, one method because they are one route.
type WalkStore interface {
	PutWalk(ctx context.Context, travellerID string, w WalkWrite) (Walk, int64, error)
}

// ShareStore is H1's three writes.
type ShareStore interface {
	SetShareOptions(ctx context.Context, travellerID, tripID string, w ShareWrite) (Trip, int64, error)

	NewShareLink(ctx context.Context, travellerID, tripID, token string) (Trip, int64, error)

	StopSharing(ctx context.Context, travellerID, tripID string) (Trip, int64, error)
}

// ErrNoShare is `GET /l/{token}` asking about a token nothing holds.
var ErrNoShare = errors.New("logbook: no such share link")

// ShareLink is what a token resolves to, revocation included.
type ShareLink struct {
	TravellerID string
	TripID      string

	Revoked bool
}

// PublicStore is the read behind GET /l/{token}.
type PublicStore interface {
	ShareLink(ctx context.Context, tokenHash []byte) (ShareLink, error)

	PublicLog(ctx context.Context, travellerID, tripID string) (PublicSource, error)
}

// ErrNoMediaObject is a digest this traveller has never begun.
var ErrNoMediaObject = errors.New("logbook: no such media object")

// MediaObject is one row of media_objects, as the domain sees it.
type MediaObject struct {
	ID          string
	ByteSize    int64
	ContentType string
	CreatedAt   time.Time
	UploadedAt  *time.Time
}

// Committed is `uploaded_at is not null`, spelled once.
func (m MediaObject) Committed() bool { return m.UploadedAt != nil }

// MediaStore is the media half of the storage contract.
type MediaStore interface {
	BeginMedia(ctx context.Context, travellerID string, b MediaBegin) (MediaObject, error)

	MediaObjects(ctx context.Context, travellerID string, ids []string) ([]MediaObject, error)

	MarkMediaUploaded(ctx context.Context, travellerID, id string) (MediaObject, error)
}
