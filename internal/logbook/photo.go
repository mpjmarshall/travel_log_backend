// What a photograph write may contain, and the TWO COLUMNS THAT ARE NOT ON IT.
//
// ------------------------------------------------------------------------
// `PhotoWrite` HAS NO `placeId` AND NO `visitId`, AND THAT ABSENCE IS THE
// WHOLE OF THE WORST DEFECT IN THIS AREA MADE UNREACHABLE (DEC-89,
// SAF-MAJ-5).
//
// The defect, measured by the safety lens against the client's own fixture on
// postgres:17.11: `ph-0` carried `place_id=bukchon, visit_id=v-bukchon-0` and
// a caption. Under the whole-state convention a body of `{caption}` — M2's
// 'Write a note', which owns the note and nothing else — writes
// `place_id=NULL, visit_id=NULL` alongside it, and the log has no record that
// the photograph was ever at Bukchon. ALL THREE STANDING GUARDS PASS ON IT:
// the dangling check sees no dangling reference (the reference is GONE), the
// place-without-occasion query sees no place, and the pair-agreement assertion
// sees two NULLs, which agree. The client's own
// sentence about the half-state is "a photograph naming a place but no
// occasion is half a record… and it is the half a count cannot see"; both
// halves gone is a state no count can see at all.
//
// DEC-89's POINTER CONTRACT ALONE WOULD CLOSE IT, and this goes further on
// purpose, because there are TWO WAYS to get the contract wrong and only one
// of them is a pointer. `TripWrite` sets the precedent: the four sharing
// fields are not on it at all (SF6), because `PUT /v1/trips/{id}` does not own
// them — H1's three switches do, through their own route. THE FILING IS THE
// SAME KIND OF THING. Exactly three things in this system write
// `photos.place_id`:
//
//	POST /v1/photos/{id}/refile   M2.2's 'Change' — sets BOTH, together
//	DELETE /v1/places/{id}        D2 — clears BOTH, through two foreign keys
//	`make seed`                   the load, which writes whole rows
//
// and no control anywhere in the client sets a photograph's pin any other way.
// So a slot for it on this type would be a FOURTH writer of a field three
// things already own, and DEC-83 makes the cost of a fourth writer concrete:
// the (place_id, visit_id) pair is coherent by a GO RULE and not by the
// schema — the paired CHECK was executed and aborts D2's keep branch — so
// every additional place the pair can be written is another place it can be
// written incoherently.
//
// WHAT THAT COSTS IN EVIDENCE IS SAID PLAINLY RATHER THAN CLAIMED AS A WIN.
// The plan predicts a mutation for this — "make one write field a value rather
// than a pointer in `PhotoWrite`" — and against this type that mutation cannot
// touch the filing at all, because there is no field to demote. The leg is
// guarded instead by a STORE mutation, which is strictly stronger and is run:
// add `place_id = NULL, visit_id = NULL` to the upsert's SET clause and watch
// the caption-only leg redden on the count that must not fall. The pointer
// mutation still has work to do on the fields that ARE here, and it is run on
// `Caption`.
// ------------------------------------------------------------------------
package logbook

import (
	"fmt"
	"strings"
)

// MaxCaptionBytes bounds `photos.caption`, and it is THIS BUILD'S POLICY
// rather than schema, in exactly the sense MaxNoteBytes and MaxSummaryBytes
// are. The column is `text` and 0003 bounds only its emptiness, so without
// this a one-megabyte note is storable and then re-emitted on every read of
// the whole log, for ever.
//
// It is the same number as MaxNoteBytes because it is the same kind of field —
// a line somebody typed under a photograph — and it is a separate constant so
// that moving one does not move the other by accident.
const MaxCaptionBytes = 4096

// PhotoWrite is the body of `PUT /v1/photos/{id}`: M2's 'Write a note', and
// the create a client-minted key makes possible (DEC-33).
//
// IT IS AN UPSERT AND NOT AN UPDATE, on `PUT /v1/places/{id}`'s precedent and
// for the reason DEC-33 gives: the key is the CLIENT's, so the request is
// idempotent by construction and a retry after a lost response cannot double
// anything. It also has to be, because nothing else in this API creates a
// photograph — the media routes create an OBJECT, and the row that references
// it has to arrive somehow.
//
// `asset` IS VALIDATED AS A COMMITTED OBJECT AND NOT MERELY AS AN EXISTING
// ROW, and that check is the store's because it is a fact about a row (see
// internal/postgres/photo_store.go). What is here is the SHAPE: DEC-38's
// content address, sixty-four lowercase hex characters, which is the same
// expression `photos_asset_sha256_ck` carries.
type PhotoWrite struct {
	ID             *string   `json:"id"`
	TripID         *string   `json:"tripId"`
	CityID         *string   `json:"cityId"`
	TakenAt        *Instant  `json:"takenAt"`
	Asset          *string   `json:"asset"`
	Caption        **string  `json:"caption"`
	Coordinates    **LatLng  `json:"coordinates"`
	AccuracyMetres **int     `json:"accuracyMetres"`
	FiledLater     **Instant `json:"filedLater"`
}

// ValidatePhoto answers the first field that is wrong, and nothing about
// whether the ids it names exist. Existence is the store's, under the
// traveller's advisory lock.
func ValidatePhoto(p PhotoWrite) error {
	if p.ID == nil || !idPattern.MatchString(*p.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if p.TripID != nil && !idPattern.MatchString(*p.TripID) {
		return InvalidFieldError{Field: "tripId",
			Why: fmt.Sprintf("%q is not an id", *p.TripID)}
	}
	if p.CityID != nil && !idPattern.MatchString(*p.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *p.CityID)}
	}
	if p.Asset != nil && !assetPattern.MatchString(*p.Asset) {
		return InvalidFieldError{Field: "asset",
			Why: "a photograph's asset is a media object id: 64 lowercase hex characters"}
	}
	if caption := Value(p.Caption); caption != nil && len(*caption) > MaxCaptionBytes {
		return InvalidFieldError{Field: "caption",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d",
				len(*caption), MaxCaptionBytes)}
	}
	if at := Value(p.Coordinates); at != nil {
		if err := checkLatLng(*at, "coordinates"); err != nil {
			return err
		}
	}
	if metres := Value(p.AccuracyMetres); metres != nil && *metres < 0 {
		// The Go half of `photos_accuracy_metres_ck`.
		return InvalidFieldError{Field: "accuracyMetres",
			Why: fmt.Sprintf("an accuracy is not negative, and %d is", *metres)}
	}
	return nil
}

// StoredCaption is the caption as it will be written: trimmed, and NULL rather
// than the empty string.
//
// THIS IS THE CLIENT'S OWN RULE AND IT IS COPIED RATHER THAN INVENTED.
// `setPhotoCaption` computes `(caption ?? ”).trim()` and stores `null` when
// that is empty, and the reason is on screen: M2's two note blocks are both
// guarded by `caption != null`, so `caption = ”` is an empty box with no way
// back out of it. Migration 0003's `photos_caption_present_ck` is the
// guarantee; this is what stops the guarantee reaching the client as a 500
// with no field on it.
//
// IT IS THE ONLY WAY TO CLEAR A CAPTION OVER THE WIRE, AND THAT IS A MEASURED
// CONSEQUENCE OF `**T` RATHER THAN A DESIGN. Probed on go1.26 against this
// module's own directive: `{"caption":null}` sets the `**string` field itself
// to nil and is INDISTINGUISHABLE from omitting the key, because
// encoding/json's `indirect` breaks at the outermost settable pointer when the
// literal is null. So `{"caption":""}` is what M2's cleared note has to send.
// docs/CLIENT-PREREQUISITES.md §R7.2 says so; nothing in this repository can
// check what the client sends.
func StoredCaption(sent **string) *string {
	value := Value(sent)
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// ---------------------------------------------------------------- THE SNOOZE

// SnoozeWrite is the body of `POST /v1/photos/snooze`: N1's 'Later', and the
// FIRST route in this API that takes a COLLECTION.
//
// ONE ROW ON N1 STANDS FOR EVERY UNFILED PHOTOGRAPH FROM ONE CITY ON ONE TRIP,
// which is why the client's own method takes a group and why this is not a
// loop over `PUT /v1/photos/{id}`. Thirty photographs snoozed one at a time is
// thirty version bumps, thirty round trips, and thirty chances to stop half
// way — which is a partial failure the client has no state for.
//
// `PhotoIDs` IS A POINTER TO A SLICE FOR THE REASON `Points` IS. An absent key
// and an empty array are different requests: absent is a body that never named
// a group, and `[]` is a group that turned out to be empty. Both write
// nothing, and they are told apart so that the first is a 422 naming the field
// and the second is the 200 the client's own "returns false without writing
// when the group is empty" describes.
type SnoozeWrite struct {
	PhotoIDs *[]string `json:"photoIds"`
	Until    *Instant  `json:"until"`
}

// ValidateSnooze answers the first field that is wrong.
//
// `until` IS REQUIRED AND THERE IS NO UN-SNOOZE. N1's 'Later' always names a
// date — `filedLaterSnooze` is seven days in the client — and no control
// anywhere takes a snooze back off. A nullable `until` would offer a
// destruction no sheet authorises, which is the same error `visits: []` was.
func ValidateSnooze(s SnoozeWrite) error {
	if s.PhotoIDs == nil {
		return InvalidFieldError{Field: "photoIds",
			Why: "a snooze names the group it is snoozing; one row on N1 stands for " +
				"every unfiled photograph from one city on one trip"}
	}
	if s.Until == nil {
		return InvalidFieldError{Field: "until",
			Why: "'Later' is a date, and there is no control that takes a snooze back off"}
	}
	seen := make(map[string]bool, len(*s.PhotoIDs))
	for _, id := range *s.PhotoIDs {
		if !idPattern.MatchString(id) {
			return InvalidFieldError{Field: "photoIds",
				Why: fmt.Sprintf("%q is not an id", id)}
		}
		if seen[id] {
			return InvalidFieldError{Field: "photoIds",
				Why: fmt.Sprintf("%q appears twice, and a photograph is snoozed once", id)}
		}
		seen[id] = true
	}
	return nil
}

// ---------------------------------------------------------------- THE REFILE

// RefileWrite is the body of `POST /v1/photos/{id}/refile`: M2.2's 'Change',
// which moves a photograph's pin AND its occasion together.
//
// THE CLIENT MINTS BOTH IDS AND THE SERVER'S JOB IS TO CHECK THEM, NOT TO
// CHOOSE (v6's DEC-26 amendment). `refilePhoto` today picks the occasion
// itself — `place.visitsOn(photo.tripId).firstOrNull`, minting one when there
// is none — and against a server it still picks, but it has to SEND what it
// picked. docs/CLIENT-PREREQUISITES.md §11 carries that signature change.
//
// `visitAt` IS REQUIRED EXACTLY WHEN THE OCCASION IS NEW, AND ONLY THE STORE
// KNOWS WHICH. That is the same half-answerable shape `ValidateTrip` records
// for the date ordering, and it is enforced beside the existence checks.
//
// AN EXISTING OCCASION'S `at` IS NOT REWRITTEN BY A REFILE, AND THAT IS A
// DECISION RATHER THAN AN OMISSION. A visit is shared: thirty photographs at
// `fushimi-inari` hang off twenty-eight of them. Applying `visitAt` to an
// occasion that already exists would re-time it for every other photograph
// filed there, reorder the place's visits array, and move `lastVisited` on P1
// — which is the one-thing-too-many defect, from a control whose whole promise
// is that it moves ONE photograph.
type RefileWrite struct {
	PlaceID *string  `json:"placeId"`
	VisitID *string  `json:"visitId"`
	VisitAt *Instant `json:"visitAt"`
}

// ValidateRefile answers the SHAPE of the ids it was given and says nothing
// about whether they were given at all.
//
// THE SPLIT IS THE ONE ValidateCity ALREADY MAKES — "'a city needs a name' is
// not here either" — and here it is load-bearing rather than tidy. Whether a
// refile has NAMED its occasion is the question `Service.RefilePhoto` exists
// to refuse, because a nil `visitId` reaching a store is a `SELECT … LIMIT 1`
// waiting to be written. See internal/logbook/service.go.
func ValidateRefile(r RefileWrite) error {
	if r.PlaceID != nil && !idPattern.MatchString(*r.PlaceID) {
		return InvalidFieldError{Field: "placeId",
			Why: fmt.Sprintf("%q is not an id", *r.PlaceID)}
	}
	if r.VisitID != nil && !idPattern.MatchString(*r.VisitID) {
		return InvalidFieldError{Field: "visitId",
			Why: fmt.Sprintf("%q is not an id", *r.VisitID)}
	}
	return nil
}

// PhotoRefiled is what a refile answers, and IT CARRIES BOTH SHAPES BECAUSE
// THE ROUTE HAS BOTH — the device `CityWritten` uses, for its reason.
//
// When the client named an occasion that ALREADY EXISTS, one entity moved: the
// photograph. DEC-32's bare entity is the right answer and the phone splices
// it into its cached log.
//
// When the client MINTED one, two entities moved. The place gained a visit and
// every one of that place's ordinals was rewritten in `at` DESC, so the phone
// cannot splice what it was not sent — and `Document` is the whole envelope,
// exactly as `PUT /v1/cities/{id}` answers it when `attachTo` is honoured.
//
// `Document` IS NIL EXACTLY WHEN NO OCCASION WAS MINTED, which makes "which
// shape did this write earn" a property of the value rather than a second
// reading of the request the handler has to get right.
type PhotoRefiled struct {
	Photo    Photo
	Document *Document
	Version  int64
}
