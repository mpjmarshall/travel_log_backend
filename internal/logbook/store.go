// The storage contract the domain declares and internal/postgres satisfies
// (DEC-62: the business rules own the contract and the storage implementation
// meets it).
//
// READ TAKES A CALLBACK RATHER THAN ANSWERING A DOCUMENT, AND THAT IS THE
// WHOLE OF THE 304. DEC-31 says the version lookup happens FIRST — one indexed
// SELECT before any other query — so a conditional request that has not
// changed never assembles the five lists. Two facts make a callback the only
// honest shape for that:
//
//   - the version and the document must come out of ONE repeatable-read
//     snapshot, or the phone stores a torn body under a number describing a
//     different moment and serves it for ever;
//   - the decision to assemble belongs to the HANDLER, because it is the
//     handler that holds If-None-Match and DEC-49's emitter version.
//
// A two-call interface — ReadVersion then ReadDocument — cannot keep both: the
// second call is a second snapshot. Answering (version, document) always
// assembles. So Read runs the caller's `assemble` inside the snapshot with the
// version in hand, and builds the document only if it says so.
package logbook

import (
	"context"
	"errors"
	"time"
)

// ErrNoTraveller is a read or a write for a traveller row that is not there.
// It is reachable even behind an authenticated route: the row can be deleted
// between the credential being accepted and the query running.
var ErrNoTraveller = errors.New("logbook: no such traveller")

// ErrNoTrip is a write answering about a trip nothing holds. It exists for the
// re-read after an upsert, which must never invent a body.
var ErrNoTrip = errors.New("logbook: no such trip")

// Snapshot is what one read saw. Document is nil when `assemble` said no,
// which is exactly the 304 path and is what makes "the 304 does not assemble
// the document" a fact about the type rather than a claim.
type Snapshot struct {
	Version  int64
	Document *Document
}

type Store interface {
	Read(ctx context.Context, travellerID string, assemble func(version int64) bool) (Snapshot, error)
	PutTrip(ctx context.Context, travellerID string, w TripWrite) (Trip, int64, error)
}

// ErrNoMediaObject is a media route asking about a digest this traveller has
// never begun. It is a sentinel rather than a bare error because the handler
// has to tell "no such object" (a 404) from "the bucket is unreachable" (a
// 503), and both arrive through the same call.
var ErrNoMediaObject = errors.New("logbook: no such media object")

// MediaObject is one row of media_objects, as the domain sees it.
//
// `UploadedAt` IS A POINTER AND `alreadyExists` IS DERIVED FROM IT, NEVER FROM
// ROW PRESENCE. A begin that never uploaded would otherwise report true, the
// client would skip an upload that never happened, and the commit would 409
// with no way forward. That is v6's own finding and it is the one thing about
// this type worth writing down.
type MediaObject struct {
	ID          string
	ByteSize    int64
	ContentType string
	CreatedAt   time.Time
	UploadedAt  *time.Time
}

// Committed is `uploaded_at IS NOT NULL`, spelled once.
func (m MediaObject) Committed() bool { return m.UploadedAt != nil }

// MediaStore is the media half of the storage contract (DEC-62: the domain
// declares it, internal/postgres satisfies it).
//
// IT IS SEPARATE FROM Store, AND THE SPLIT IS DEC-50's. media_objects is not
// in the emitted logbook document, so a media write takes the traveller's
// advisory lock and does NOT bump logbook_version — a different helper, a
// different membership, and the two lists in internal/postgres/tx.go are the
// spec rather than a description. Folding these three methods into Store would
// put a non-bumping write behind an interface every other method of which
// bumps.
type MediaStore interface {
	// BeginMedia upserts the declared object and answers the row as it stands
	// AFTERWARDS — which is not always the row that was proposed.
	BeginMedia(ctx context.Context, travellerID string, b MediaBegin) (MediaObject, error)

	// MediaObjects answers the rows for these ids, in no particular order,
	// and silently omits ones that are not there. The caller decides what a
	// miss means, because it differs by route: a commit 404s and a mint
	// refuses the whole request.
	MediaObjects(ctx context.Context, travellerID string, ids []string) ([]MediaObject, error)

	// MarkMediaUploaded sets uploaded_at if it is not already set, and answers
	// the row either way. ErrNoMediaObject for a digest nothing holds.
	//
	// IT IS IDEMPOTENT BY CONSTRUCTION, WHICH IS THE RETRY CONTRACT (SAF-MIN-12).
	// The bucket-versus-database seam is the only non-atomic one in the plan:
	// the bucket confirms, this update fails, and the object exists with
	// uploaded_at NULL — bytes the user has uploaded and cannot attach. A
	// SECOND COMMIT OF AN ALREADY-UPLOADED OBJECT IS A 200 AND NOT A 409, so a
	// client that lost the response can simply ask again.
	MarkMediaUploaded(ctx context.Context, travellerID, id string) (MediaObject, error)
}
