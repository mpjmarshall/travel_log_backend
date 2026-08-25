// H1's three writes, and the only file in this package that ever holds a share
// token.
//
// IT IS ITS OWN FILE FOR THE REASON logbook.ShareStore IS ITS OWN INTERFACE:
// every statement here handles a capability, and no statement in
// logbook_store.go ever does. "The plaintext exists in exactly two places —
// the request body and this file's hash call" is a claim somebody can check by
// reading one file, and it stops being one the moment a share write lands
// beside the trip upsert.
//
// ALL THREE BUMP logbook_version, WHICH IS DEC-50's LIST AND NOT A JUDGEMENT
// CALL. `share_links` is on the bumping side because DEC-91's `shared` is
// derived from it — `EXISTS (… WHERE revoked_at IS NULL)` on every trip read —
// so a mint or a revoke that moved no version would leave every phone
// answering 304 to a log whose `shared` has changed, and H1's 'Stop sharing'
// would appear to do nothing until some unrelated write happened.
//
// AND ALL THREE ANSWER A WHOLE Trip, RE-READ FROM THE ROW (DEC-32). Not
// assembled from the request: the flags this route does not name are the ones
// the answer most needs to be right about, and a response built from the input
// is a response that agrees with the client about a write the database may
// have shaped differently.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"travellog/internal/logbook"
)

// ShareStore is logbook.ShareStore over *sql.DB.
//
// IT IS A SECOND TYPE OVER THE SAME POOL RATHER THAN MORE METHODS ON
// LogbookStore. Both are `struct{ DB *sql.DB }`, so this costs nothing at
// wiring time, and what it buys is that the interface a handler is given says
// what that handler can reach: the share handlers cannot read the whole log
// and the logbook handlers cannot mint a capability.
type ShareStore struct{ DB *sql.DB }

// setShareOptionsSQL is DEC-89's pointer contract in one statement, the same
// shape `upsertTripSQL` uses and for the same reason: a SET clause assembled
// per request would make the statement text vary with the body — eight shapes
// for three optional columns — so nothing is prepared twice and
// pg_stat_statements shows eight rows where it should show one.
//
// H1 FLICKS ONE SWITCH AT A TIME, so the "leave the other two alone" branch is
// not a corner case here: it is every single request the client makes. Each
// control on that screen goes inert while a write is in flight, precisely
// because two changes inside one save are both computed from the state as it
// was and the second puts the first back.
const setShareOptionsSQL = `UPDATE trips SET
		share_photos      = CASE WHEN $3::boolean THEN $4::boolean ELSE share_photos      END,
		share_notes       = CASE WHEN $5::boolean THEN $6::boolean ELSE share_notes       END,
		share_coordinates = CASE WHEN $7::boolean THEN $8::boolean ELSE share_coordinates END
	WHERE traveller_id = $1::uuid AND id = $2`

// revokeLiveLinkSQL kills whatever link is live and KEEPS THE ROW (DEC-67).
//
// `WHERE revoked_at IS NULL` is doing two jobs. It is the predicate
// `share_links_one_live` is built on, so revoking is exactly what makes room
// for the next insert; and it makes a second revoke a no-op rather than a
// statement that moves `revoked_at` forward on a row that was already dead —
// which would make the history say a link stopped working later than it did.
const revokeLiveLinkSQL = `UPDATE share_links SET revoked_at = now()
	WHERE traveller_id = $1::uuid AND trip_id = $2 AND revoked_at IS NULL`

// insertShareLinkSQL writes the DIGEST (DEC-85). The plaintext is in this
// process's memory for the length of one request and is written nowhere.
const insertShareLinkSQL = `INSERT INTO share_links (traveller_id, trip_id, token_hash)
	VALUES ($1::uuid, $2, $3)`

// resetShareFlagsSQL WRITES THE THREE VALUES BY NAME, and every part of that
// sentence has been argued about.
//
// IT IS NOT `SET col = DEFAULT`, AND THE REASON IS NOT THE ONE THE PLAN GAVE.
// The plan said "a DEFAULT does not reach an UPDATE", which is stated too
// broadly: `UPDATE … SET share_photos = DEFAULT` is legal SQL, it reaches the
// column default, and after migration 0002 it produces exactly true/true/false
// — the correct answer. So that mutation is GREEN and proves nothing. What is
// true is narrower and is the whole of it: an UPDATE THAT DOES NOT NAME THE
// COLUMN does not reach its default, so an implementation that revokes the
// link and stops leaves all three switches where the user left them.
//
// WRITING THE LITERALS IS STILL RIGHT, AND FOR A DIFFERENT REASON THAN THE
// PLAN'S. The three values are THE CLIENT'S — `Trip.defaultSharePhotos`,
// `defaultShareNotes`, `defaultShareCoordinates` — and what `stopSharing`
// resets to is those, not whatever the schema happens to default to today.
// They agree at this commit because 0002 made them agree. Leaning on the
// column default would make a future migration's DEFAULT silently redefine
// what "stop sharing" means.
//
// AND THE ASYMMETRY IS THE CLIENT'S. Two go back ON and one goes back OFF: a
// pin on your accommodation is not something to hand out by link, so it has to
// be actively turned on, every time.
const resetShareFlagsSQL = `UPDATE trips
	SET share_photos = true, share_notes = true, share_coordinates = false
	WHERE traveller_id = $1::uuid AND id = $2`

const tripExistsSQL = `SELECT 1 FROM trips WHERE traveller_id = $1::uuid AND id = $2`

// SetShareOptions writes only the flags that were sent, and touches no link.
//
// A BODY NAMING NONE OF THE THREE IS LEGAL AND WRITES NOTHING — DEC-89's
// contract is that absence is not an error, and refusing an empty body would
// refuse exactly the retry a client makes after a lost response. It still
// bumps the version, which is the honest report: this transaction happened,
// and the caller cannot tell from the outside that it changed no column.
func (s ShareStore) SetShareOptions(ctx context.Context, travellerID, tripID string, w logbook.ShareWrite) (logbook.Trip, int64, error) {
	return s.write(ctx, travellerID, tripID, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, setShareOptionsSQL, travellerID, tripID,
			w.SharePhotos != nil, boolOrFalse(w.SharePhotos),
			w.ShareNotes != nil, boolOrFalse(w.ShareNotes),
			w.ShareCoordinates != nil, boolOrFalse(w.ShareCoordinates),
		)
		if err != nil {
			return fmt.Errorf("postgres: writing the share options of %s: %w", tripID, err)
		}
		return nil
	})
}

// NewShareLink is revoke-then-insert in ONE transaction (DEC-67), and the
// order is load-bearing rather than tidy.
//
// `share_links_one_live` is a PARTIAL unique index on (traveller_id, trip_id)
// WHERE revoked_at IS NULL, and it is the only thing in the schema enforcing
// the "0..1 live" the class diagram claims. So an implementation that inserts
// without revoking does not silently create two live links — it RAISES, which
// is why the leg for this asserts the 201 and the surviving row counts rather
// than the absence of an error.
//
// THE TOKEN IS HASHED HERE AND ECHOED BACK. `logbook.HashShareToken` is the
// only thing that leaves this function with it, and the plaintext goes into
// the answer because the caller sent it a microsecond ago — DEC-85 means no
// later read can ever produce it again.
func (s ShareStore) NewShareLink(ctx context.Context, travellerID, tripID, token string) (logbook.Trip, int64, error) {
	trip, version, err := s.write(ctx, travellerID, tripID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, revokeLiveLinkSQL, travellerID, tripID); err != nil {
			return fmt.Errorf("postgres: revoking the live link on %s: %w", tripID, err)
		}
		if _, err := tx.ExecContext(ctx, insertShareLinkSQL,
			travellerID, tripID, logbook.HashShareToken(token)); err != nil {
			return fmt.Errorf("postgres: minting a link for %s: %w", tripID, err)
		}
		return nil
	})
	if err != nil {
		return logbook.Trip{}, 0, err
	}
	trip.ShareLinkID = &token
	return trip, version, nil
}

// StopSharing revokes the link and resets the three flags.
//
// IT IS IDEMPOTENT AND THAT IS DELIBERATE. Revoking nothing is not an error:
// H1's 'Stop sharing' and U1's own 'Stop' are the same method, reachable from
// two screens, and a second press — or a retry after a lost response — must
// not fail. What it still does is reset the flags, which is the honest reading
// of the control: the switches belong to the link.
func (s ShareStore) StopSharing(ctx context.Context, travellerID, tripID string) (logbook.Trip, int64, error) {
	return s.write(ctx, travellerID, tripID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, revokeLiveLinkSQL, travellerID, tripID); err != nil {
			return fmt.Errorf("postgres: revoking the link on %s: %w", tripID, err)
		}
		// THE RESET IS NOT OPTIONAL AND IT IS NOT THE COLUMN DEFAULT. See
		// resetShareFlagsSQL: removing it is a privacy leak rather than a
		// tidiness issue, because the NEXT link inherits whatever the last one
		// was set to.
		if _, err := tx.ExecContext(ctx, resetShareFlagsSQL, travellerID, tripID); err != nil {
			return fmt.Errorf("postgres: resetting the share options of %s: %w", tripID, err)
		}
		return nil
	})
}

// write is the shape all three share: the traveller's lock, the version bump,
// an existence check that names the field, the body, and the trip re-read from
// the row it just wrote.
//
// THE EXISTENCE CHECK IS FIRST AND IT IS WHAT MAKES AN UNKNOWN TRIP A 404
// RATHER THAN A 200 THAT DID NOTHING. All three statements are UPDATEs or
// INSERTs scoped by (traveller_id, trip_id): an UPDATE matching nothing
// reports success, so without this a share write against a trip that is not in
// the log answers 200 with a body the re-read cannot produce. It is race-free
// because the advisory lock is already held.
func (s ShareStore) write(ctx context.Context, travellerID, tripID string, body func(context.Context, *sql.Tx) error) (logbook.Trip, int64, error) {
	var trip logbook.Trip

	version, err := WithTravellerTx(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if err := requireRow(ctx, tx, tripExistsSQL, travellerID, tripID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", logbook.ErrNoTrip, tripID)
			}
			return fmt.Errorf("postgres: looking up the trip %s: %w", tripID, err)
		}
		if err := body(ctx, tx); err != nil {
			return err
		}
		read, err := readOneTrip(ctx, tx, travellerID, tripID)
		if err != nil {
			return err
		}
		trip = read
		return nil
	})
	if err != nil {
		return logbook.Trip{}, 0, travellerError(err, travellerID)
	}
	return trip, version, nil
}

// boolOrFalse is the value half of DEC-89's pair: the `sent` flag is the
// pointer's nil-ness and this is what it points at. The false it answers for a
// nil pointer never reaches a column, because the CASE keeps the stored value
// on that branch.
func boolOrFalse(p *bool) bool { return p != nil && *p }
