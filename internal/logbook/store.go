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
