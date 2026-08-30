// H1's three writes, and the only file in this package that ever holds a
// share token.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"travellog/internal/logbook"
)

// ShareStore is logbook.ShareStore over *sql.DB.
type ShareStore struct{ DB *sql.DB }

// setShareOptionsSQL is the pointer contract in one statement, the same shape
// `upsertTripSQL` uses and for the same reason.
const setShareOptionsSQL = `UPDATE trips SET
		share_photos      = CASE WHEN $3::boolean THEN $4::boolean ELSE share_photos      END,
		share_notes       = CASE WHEN $5::boolean THEN $6::boolean ELSE share_notes       END,
		share_coordinates = CASE WHEN $7::boolean THEN $8::boolean ELSE share_coordinates END
	WHERE traveller_id = $1::uuid AND id = $2`

// revokeLiveLinkSQL kills whatever link is live and keeps the row.
const revokeLiveLinkSQL = `UPDATE share_links SET revoked_at = now()
	WHERE traveller_id = $1::uuid AND trip_id = $2 AND revoked_at IS NULL`

// insertShareLinkSQL writes the DIGEST.
const insertShareLinkSQL = `INSERT INTO share_links (traveller_id, trip_id, token_hash)
	VALUES ($1::uuid, $2, $3)`

// resetShareFlagsSQL writes's three values by name, and every part of that
// sentence has been argued about.
const resetShareFlagsSQL = `UPDATE trips
	SET share_photos = true, share_notes = true, share_coordinates = false
	WHERE traveller_id = $1::uuid AND id = $2`

const tripExistsSQL = `SELECT 1 FROM trips WHERE traveller_id = $1::uuid AND id = $2`

// SetShareOptions writes only the flags that were sent, and touches no link.
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

// NewShareLink is revoke-then-insert in one transaction, and the order is
// load-bearing rather than tidy.
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

// StopSharing revokes the link and resets's three flags.
func (s ShareStore) StopSharing(ctx context.Context, travellerID, tripID string) (logbook.Trip, int64, error) {
	return s.write(ctx, travellerID, tripID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, revokeLiveLinkSQL, travellerID, tripID); err != nil {
			return fmt.Errorf("postgres: revoking the link on %s: %w", tripID, err)
		}
		if _, err := tx.ExecContext(ctx, resetShareFlagsSQL, travellerID, tripID); err != nil {
			return fmt.Errorf("postgres: resetting the share options of %s: %w", tripID, err)
		}
		return nil
	})
}

// write is the shape all three share.
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

// boolOrFalse is the value half of the pair: the `sent` flag is the pointer's
// nil-ness and this is what it points at.
func boolOrFalse(p *bool) bool { return p != nil && *p }
