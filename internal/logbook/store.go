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

// ErrNoPlace is a place write answering about a row nothing holds — the same
// re-read-after-upsert case ErrNoTrip exists for.
//
// THERE IS NO ErrNoCity BESIDE IT AND THAT IS NOT AN OVERSIGHT. Every route
// that names a city either creates it (`PUT /v1/cities/{id}`, whose re-read
// follows its own upsert) or names it as a FIELD of some other write, where an
// unknown one is `InvalidFieldError{Field: "cityId"}` and a 422 rather than a
// 404. A sentinel nothing returns is a branch nothing takes.
var ErrNoPlace = errors.New("logbook: no such place")

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

	// DeleteTrip implements D3's own table and nothing more, and ANSWERS THE
	// WHOLE LOG rather than the trip it removed.
	//
	// THE CACHE CANNOT SPLICE A CASCADE, which is why this is the one write in
	// the plan that does not answer a bare entity. DEC-32's write response
	// exists so the phone can patch one object into its cached document; D3
	// removes rows from FIVE tables — the trip, its photographs, its walks,
	// its visits and its itinerary — and clears a column on rows in a sixth.
	// A client handed a 204 would have to re-derive all of that from a sheet's
	// copy, which is the definition of two implementations of one rule.
	//
	// AN UNKNOWN TRIP IS NOT AN ERROR AND MOVES NO VERSION. The client's own
	// contract is that a delete of something absent has succeeded — "the
	// caller asked for that trip to be absent and it is" — so this answers the
	// log as it stands. Moving the version anyway would be defensible and is
	// wrong in one specific way: a retried delete would invalidate the phone's
	// whole cached document, and DEC-103 exists precisely because deletes get
	// retried against servers that did not have the route.
	DeleteTrip(ctx context.Context, travellerID, tripID string) (Snapshot, error)

	// SetTravellerName is U1's pencil, and it BUMPS logbook_version because
	// the traveller's name is in the emitted document — `traveller: {name}` is
	// the sixth key, and a phone holding a cached log would otherwise never
	// see the change.
	//
	// AN EMPTY NAME IS REFUSED AND IS NOT A WAY TO CLEAR IT, matching the
	// client exactly: `setTravellerName` returns false on a trimmed-empty
	// name, and the reason is stated there — "a log with an owner keeps one,
	// and 'no traveller' is a state a log arrives in and never returns to".
	// The refusal is a named field, so the store answers InvalidFieldError
	// rather than letting travellers_name_present_ck produce a 500.
	SetTravellerName(ctx context.Context, travellerID, name string) (Traveller, int64, error)
}

// CityWritten is what `PUT /v1/cities/{id}` answers, and IT CARRIES BOTH
// SHAPES BECAUSE THE ROUTE HAS BOTH.
//
// With `attachTo` the write appends the new id to that trip's `cityIds` in the
// SAME TRANSACTION, which makes it CASCADING: two entities moved, and the
// phone cannot splice a trip it was not sent. Without it, one city was created
// or renamed and DEC-32's bare entity is the right answer.
//
// `Document` IS NIL WHEN `attachTo` WAS ABSENT, which makes "which shape did
// this write earn" a property of the value rather than a second reading of the
// request the handler has to get right. It is the same device Snapshot uses
// for the 304.
type CityWritten struct {
	City     City
	Document *Document
	Version  int64
}

// CityStore is T5's 'Add a city', declared here and satisfied by
// internal/postgres.
//
// IT IS ITS OWN PORT RATHER THAN TWO MORE METHODS ON Store, on ShareStore's
// precedent and for the reason stated there: the interface a handler is handed
// says what that handler can reach. The city handler cannot delete a trip and
// the trip handler cannot create a city, and neither has to be trusted not to.
type CityStore interface {
	// PutCity is createCity, and `attachTo` is what makes it cascading.
	//
	// AN `attachTo` NAMING A TRIP THIS LOG DOES NOT HOLD IS A 422 AND NOT A
	// 404, and the client's own method is why: `createCity` answers null
	// without writing when `log.trip(attachTo) == null`, so the trip is being
	// treated as a FIELD of the request rather than as the thing the request
	// is about. The thing this request is about is the city, and it is in the
	// path.
	PutCity(ctx context.Context, travellerID string, w CityWrite) (CityWritten, error)
}

// PlaceStore is C1's pin and D2's removal, declared here and satisfied by
// internal/postgres.
type PlaceStore interface {
	// PutPlace writes the place and, when the body carried one, the WHOLE
	// ORDERED visits array — as an UPSERT and never as a delete-then-insert.
	// See internal/postgres/place_store.go for what that distinction costs.
	PutPlace(ctx context.Context, travellerID string, w PlaceWrite) (Place, int64, error)

	// RemovePlace is D2, and it ANSWERS THE WHOLE LOG for the reason
	// DeleteTrip does: the cache cannot splice a cascade. Removing a place
	// takes its visits either way and then either clears two columns on the
	// photographs filed there or deletes them outright — rows in three tables
	// from one request.
	//
	// `deletePhotos` IS A BOOL HERE AND A THREE-VALUED TYPE ONE LAYER UP, and
	// that is deliberate rather than inconsistent. By the time a call reaches
	// this method the question HAS been answered; what must not have a default
	// is the REQUEST, and logbook.PhotoDisposition is where that is enforced —
	// see Service.RemovePlace.
	//
	// AN UNKNOWN PLACE IS A SUCCESS AND MOVES NO VERSION, exactly as an
	// unknown trip is on DeleteTrip: the client's `removePlace` answers true
	// for an id the log does not hold, because the caller asked for that place
	// to be absent and it is.
	RemovePlace(ctx context.Context, travellerID, placeID string, deletePhotos bool) (Snapshot, error)
}

// ShareStore is H1's three writes, declared here and satisfied by
// internal/postgres.
//
// IT IS SEPARATE FROM Store FOR THE REASON MediaStore IS: not because of the
// transaction helper — all three of these DO bump logbook_version, because
// `sharePhotos`, `shareNotes` and `shareCoordinates` are emitted fields and
// DEC-91's `shared` is derived from the row these writes move — but because
// the SHARE LINK IS A CAPABILITY and the logbook is a record. Every method
// here handles a token; no method on Store ever sees one. Keeping them apart
// is what makes "the plaintext exists in exactly two places" checkable by
// reading one file.
//
// ALL THREE ANSWER A WHOLE Trip (DEC-32), which the phone splices into its
// cached log. They are the only writes in the plan that can leave
// `shareLinkId` non-nil, and only one of them does — see NewShareLink.
type ShareStore interface {
	// SetShareOptions writes only the flags that were sent (DEC-89) and
	// touches no share link at all. H1's three switches are about what the
	// link SHOWS, not about whether there is one.
	SetShareOptions(ctx context.Context, travellerID, tripID string, w ShareWrite) (Trip, int64, error)

	// NewShareLink revokes whatever link is live and inserts the client's
	// token, IN ONE TRANSACTION (DEC-67). Two statements and not one: the
	// table revokes and keeps, so `share_links_one_live` — the partial unique
	// index that is the only thing enforcing the 0..1 the class diagram claims
	// — refuses the insert unless the revoke lands first.
	//
	// THE ANSWER CARRIES THE PLAINTEXT TOKEN, and it is ECHOED rather than
	// recovered: the caller sent it in the request body. This is the only
	// response in the whole API that can, and it is what leaves DEC-32's
	// splice a usable `shareLinkId` on the one write that has one.
	NewShareLink(ctx context.Context, travellerID, tripID, token string) (Trip, int64, error)

	// StopSharing revokes the live link AND resets all three flags EXPLICITLY
	// to true/true/false — the client's own defaults, which `stopSharing`
	// writes. The switches belong to the LINK and not to the trip's history:
	// leaving `shareCoordinates` on after a link is killed means the NEXT link
	// hands out exact pins without anybody turning that on.
	StopSharing(ctx context.Context, travellerID, tripID string) (Trip, int64, error)
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
