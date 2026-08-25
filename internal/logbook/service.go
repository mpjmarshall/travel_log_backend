// The Service layer, written for EXACTLY THREE OPERATIONS and for nothing else
// (DEC-62, PD-05).
//
// DEC-62 ruled it and named the three — `RefilePhoto`, `RemovePlace` and the
// media commit flow — and ruled in the same breath that most routes are plain
// CRUD where a service method forwards to the repository, and that such a file
// is noise. R3 built the first, R6 builds the second, and RefilePhoto lands in
// R7.
//
// SO A FOURTH METHOD IS A DECISION AND NOT DRIFT. The three land in three
// different steps, so nobody ever sees the pattern in one sitting, and a
// worker looking for symmetry applies "there is a Service" uniformly. A leg
// asserts this type has no methods beyond the three.
//
// WHAT MAKES CommitMedia BELONG HERE RATHER THAN IN A HANDLER. It is the only
// operation in R1-R8 that spans TWO systems: it asks the BUCKET what it holds
// and then writes the DATABASE. A handler doing that is a handler that knows
// what a well-formed upload is, and the next handler that needs the same
// answer either duplicates it or gets it slightly different.
//
// AND IT IS THE ONE NON-ATOMIC SEAM IN THE PLAN (SAF-MIN-12). Every
// destructive path in R5, R6 and R7 is a single transaction and cannot
// half-apply — the safety lens could not construct a partial cascade. This one
// can: the bucket confirms, the database update fails, and the object exists
// with `uploaded_at` NULL, which R7's photo route then rejects. So the user has
// uploaded bytes they cannot attach. What answers that is the retry contract,
// written into this file rather than left to a caller: a SECOND commit of an
// already-uploaded object is a SUCCESS and not a conflict.
package logbook

import (
	"context"
	"errors"
	"fmt"

	"travellog/internal/media"
)

// ErrUploadIncomplete is a commit for an object the bucket does not hold, or
// holds as something other than what was declared.
//
// IT IS 409 AND NOT 422 (httpx's own table says so and this is where the
// reason belongs, since this is the flow that owns it): the referenced row
// EXISTS and the request is well-formed. What is wrong is the object's state,
// which is a conflict — and the client's answer is to upload and ask again,
// which is a different instruction from "fix your field".
var ErrUploadIncomplete = errors.New("logbook: the object has not landed in the bucket")

// Objects is the half of internal/media the commit path needs.
//
// IT IS A ONE-METHOD INTERFACE OVER A PACKAGE THIS FILE ALREADY IMPORTS, and
// both halves of that are deliberate. The import is fine — internal/media
// knows nothing about a trip or a traveller and is a leaf — and re-declaring
// `media.Key` and `media.Attributes` here would be a second place for the same
// two facts, which is the defect this project has recorded four times. The
// narrowing is what stops a Service from ever presigning: minting a capability
// is the handler's, and an interface carrying PresignGet would make reaching
// for it at the wrong moment a plausible-looking line.
type Objects interface {
	Stat(ctx context.Context, key media.Key) (media.Attributes, error)
}

// Service is the three operations. It is a struct of ports rather than a
// constructor, for the reason auth.Service is one: the wiring is visible at
// cmd/api and a missing port is a nil-pointer panic at boot rather than a
// zero-value that half works.
type Service struct {
	Media   MediaStore
	Objects Objects

	// Places is R6's port, and it is the WHOLE PlaceStore rather than a
	// one-method narrowing like Objects above. The narrowing on Objects exists
	// to stop a Service ever presigning — minting a capability is the
	// handler's — and there is no equivalent thing to keep away from here: the
	// other method on this port is `PutPlace`, which is the same traveller's
	// own log and is plain CRUD the handler reaches directly. A second
	// interface declaring one of two methods would be ceremony.
	Places PlaceStore
}

// RemovePlace is D2, and it is the SECOND of the three operations DEC-62 named
// (PD-05). CommitMedia is R3's and RefilePhoto is R7's.
//
// WHAT IT OWNS IS THE QUESTION, NOT THE STATEMENTS. The statement ORDER that
// makes D2's delete branch mean what the sheet says has to live inside one
// transaction and is therefore internal/postgres's — the sheet's promise is
// written out beside the two statements there. What is here is the thing no
// layer below can hold: `?photos=keep|delete` IS REQUIRED, and a
// `PhotoDisposition` with no usable zero value is how "there is no default"
// stops being a rule a handler remembers and becomes a fact about the call.
//
// SO THIS IS DELIBERATELY THIN, AND THE THINNESS IS ARGUED RATHER THAN
// APOLOGISED FOR. DEC-62 warns in terms against "empty forwarding methods for
// symmetry", and the test of a forwarding method is whether deleting it
// changes anything. Delete this one and `photosUnspecified` reaches the store
// as `deletePhotos == false`, which is D2's KEEP branch: a caller that never
// answered the question gets one of the two answers, silently, and the sheet's
// own reason for asking — that the two branches destroy different amounts — is
// gone. That is the same defect class as `[]Visit` making absent and empty one
// value, one route over.
//
// THE REFUSAL IS AN InvalidFieldError NAMING `photos`, so it reaches the
// client as the 422 that says which field, through the same mapping every
// other refusal in this API goes through.
func (s Service) RemovePlace(ctx context.Context, travellerID, placeID string, photos PhotoDisposition) (Snapshot, error) {
	if s.Places == nil {
		return Snapshot{}, errors.New("logbook: the place service has no store")
	}
	switch photos {
	case KeepPhotos, DeletePhotos:
	default:
		// The same sentence ParsePhotoDisposition gives, because to a caller
		// they are one condition: this route will not guess how far a deletion
		// reaches.
		_, err := ParsePhotoDisposition(photos.String())
		return Snapshot{}, err
	}
	return s.Places.RemovePlace(ctx, travellerID, placeID, photos == DeletePhotos)
}

// CommitMedia is `POST /v1/media/{id}/commit`: HEAD the bucket, verify what
// came back against what was declared, and set uploaded_at.
//
// FOUR THINGS ARE CHECKED AND EACH REFUSES A DIFFERENT LIE.
//
//   - THE OBJECT IS THERE. `media.ErrNoSuchObject` is the ordinary case — a
//     client that began and has not finished uploading — and it is the 409
//     the route exists to answer.
//   - THE SIZE MATCHES WHAT WAS DECLARED. The signature already pins the exact
//     length, so a mismatch here means the row was rewritten or the object was
//     replaced; either way the row and the bytes disagree and the row is what
//     every later reader trusts.
//   - THE STORED DIGEST MATCHES THE ADDRESS. This is free — `StatObject` with
//     `Checksum: true` returns it in the same call (DEC-88) — and it is worth
//     more than it costs: an object uploaded through either of the two BANNED
//     presign calls carries NO checksum at all, so requiring a non-empty
//     matching value turns the ban from an AST walk into a RUNTIME guard.
//     Note the flag: without `Checksum: true` the field comes back empty and
//     this check passes nothing, which is why internal/media's Stat sets it
//     and says so.
//   - THE TYPE MATCHES. DEC-87 signs Content-Type into the PUT, so a
//     disagreement here means the object in the bucket is not the thing the
//     row claims it is — and the row is what the allowlist constrains.
//
// A SECOND COMMIT IS A 200 AND NOT A 409, AND IT IS TAKEN BEFORE THE BUCKET IS
// ASKED. That ordering is the retry contract doing real work rather than being
// polite: a client that lost the first response asks again and is told yes,
// with no round trip to the bucket at all, and the row it is handed is the row
// that is there.
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
		// NOT ErrUploadIncomplete. A bucket that cannot be reached is an
		// outage and answers 503; telling the client its upload is incomplete
		// would have it re-upload bytes that are already there, against a
		// server that is the thing at fault.
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
