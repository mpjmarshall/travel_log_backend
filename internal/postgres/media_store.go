// media_objects, and the two statements the begin route is made of.
//
// THE TWO WRITES TAKE THE TRAVELLER'S ADVISORY LOCK AND MOVE NO VERSION
// (DEC-50). `WithTravellerLock` rather than `WithTravellerTx`, because
// media_objects is not in the emitted logbook document — see
// internal/postgres/tx.go, where the two memberships are the spec rather than
// a description. Bumping here would invalidate the phone's whole cache on
// every begin of a photograph it has not finished uploading.
//
// THE READ TAKES NEITHER, AND ITS OWN COMMENT SAYS WHY. Three methods and two
// helpers is worth naming here rather than leaving to be noticed: `mint` is
// the busiest media route and a read behind the write lock queues every grid
// the phone paints behind every upload it is finishing.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"travellog/internal/logbook"
)

// MediaStore is the media half of the storage contract.
type MediaStore struct{ DB *sql.DB }

var _ logbook.MediaStore = MediaStore{}

// beginSQL is the upsert, and THE `WHERE` ON THE CONFLICT BRANCH IS THE WHOLE
// OF IT.
//
// EXECUTED BY THE DATABASE LENS AGAINST THE REAL TABLE: without
// `WHERE media_objects.uploaded_at IS NULL`, a client re-beginning an
// already-COMMITTED digest rewrites what those bytes ARE — a committed row
// reading `(10 | image/png)` became `(999999 | text/html)`. Migration 0003's
// allowlist does not close it, because any ALLOWLISTED-BUT-WRONG type passes:
// re-declare a png as a jpeg and the CHECK is satisfied while the row now lies
// about an object whose bytes cannot change. With the WHERE the row is
// untouched and the client is told `alreadyExists: true`, which is the true
// answer to what it asked.
//
// AN UPSERT AND NOT A SELECT-THEN-INSERT. A select-then-insert loses the race
// between a retry and its own original — which is exactly the retry content
// addressing exists to make free — and it would lose it under the advisory
// lock too, because the lock is per traveller and the race is a client racing
// itself across two requests.
const beginSQL = `INSERT INTO media_objects (traveller_id, id, byte_size, content_type)
	VALUES ($1::uuid, $2, $3, $4)
	ON CONFLICT (traveller_id, id) DO UPDATE
		SET byte_size = EXCLUDED.byte_size,
		    content_type = EXCLUDED.content_type
		WHERE media_objects.uploaded_at IS NULL`

// selectSQL reads the rows back, and IT IS A SEPARATE STATEMENT BECAUSE
// `RETURNING` CANNOT DO THIS JOB. v6 deleted the RETURNING projection as OE-4
// with the reason "not an xmax trick", which leaves the door open for somebody
// to put it back as an ordinary projection. The real reason is executed and
// closes it: `DO UPDATE … WHERE <false>` and `DO NOTHING` both return ZERO
// ROWS — `INSERT 0 0`, no row emitted — so a handler reading its answer off
// RETURNING gets NOTHING on exactly the `alreadyExists` path, which is the one
// path the response is about. A separate SELECT is correct, and it is race-free
// because both statements run inside one transaction holding this traveller's
// advisory lock.
//
// AND IT TAKES ITS ID LIST AS GENERATED PLACEHOLDERS, which is a DEPENDENCY
// decision rather than a style one.
//
// `selectSQL` takes its id list as generated placeholders rather than as an
// array parameter, and that is a DEPENDENCY decision rather than a style one:
// `= ANY($2)` over a text[] needs a driver-side array encoder, which
// `database/sql` does not have and which every candidate for supplying it is a
// SEVENTH package (go_backend.md L20 names the driver as a blank import and
// nothing else). `string_to_array($2, ',')` was the other answer and was
// declined because it makes the statement's meaning depend on an id never
// containing a comma — true today by the schema's own hex CHECK, and a silent
// wrong answer rather than an error the day it is not. The list is bounded at
// logbook.MaxMintIDs, so the generated statement is bounded too.
func selectSQL(n int) string {
	holders := make([]string, n)
	for i := range holders {
		holders[i] = "$" + strconv.Itoa(i+2)
	}
	return `SELECT id, byte_size, content_type, created_at, uploaded_at
		FROM media_objects
		WHERE traveller_id = $1::uuid AND id IN (` + strings.Join(holders, ", ") + `)`
}

// markSQL is the commit, and the `WHERE uploaded_at IS NULL` is the retry
// contract (SAF-MIN-12): a second commit updates nothing and the SELECT below
// answers the row that is there, so it is a 200 rather than a 409.
//
// `now()` AND NOT `created_at`: media_objects_uploaded_after_created_ck (0003)
// requires `uploaded_at >= created_at`, and now() inside a transaction is the
// TRANSACTION's start time, which is always at or after the row's own
// created_at default.
const markSQL = `UPDATE media_objects SET uploaded_at = now()
	WHERE traveller_id = $1::uuid AND id = $2 AND uploaded_at IS NULL`

// BeginMedia upserts the declared object and answers the row as it STANDS,
// which on the conflict path is not the row that was proposed.
func (s MediaStore) BeginMedia(ctx context.Context, travellerID string, b logbook.MediaBegin) (logbook.MediaObject, error) {
	if b.SHA256 == nil || b.ByteSize == nil || b.ContentType == nil {
		// Unreachable through the route, which validates first. Here because a
		// store that dereferenced a nil would fail as a panic in a middleware
		// nobody is looking at, rather than as an error.
		return logbook.MediaObject{}, errors.New("postgres: an incomplete media begin reached the store")
	}

	var out logbook.MediaObject
	err := WithTravellerLock(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, beginSQL, travellerID, *b.SHA256, *b.ByteSize, *b.ContentType); err != nil {
			return fmt.Errorf("postgres: beginning media %s: %w", *b.SHA256, err)
		}
		rows, err := readMediaRows(ctx, tx, travellerID, []string{*b.SHA256})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			// The INSERT ran in this transaction, so the row is there unless
			// something removed it — which nothing in this application can do,
			// because no code path deletes a media object at all (OE-12).
			return fmt.Errorf("postgres: media %s vanished between its own insert and its read", *b.SHA256)
		}
		out = rows[0]
		return nil
	})
	if err != nil {
		return logbook.MediaObject{}, mediaError(err)
	}
	return out, nil
}

// MediaObjects answers the rows these ids name, and SILENTLY OMITS the ones
// that are not there.
//
// ONE STATEMENT AND NOT N, and that is not OE-2 being ignored. OE-2 deleted
// `PresignGetBatch` from the OBJECT store, and its stated reason was that
// presigning is a local HMAC so a batch saves nothing a loop does not. A
// database read is the opposite case: `POST /v1/media/mint` takes up to a
// hundred ids and a hundred round trips is a hundred round trips.
func (s MediaStore) MediaObjects(ctx context.Context, travellerID string, ids []string) ([]logbook.MediaObject, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []logbook.MediaObject
	// A READ SNAPSHOT AND NOT THE WRITE LOCK. Nothing here writes, and
	// `POST /v1/media/mint` is the busiest media route — a hundred ids on one
	// request — so putting it behind the traveller's advisory lock would queue
	// every grid the phone paints behind every upload it is finishing. What
	// the snapshot still gives is the answer a bare SELECT would not: an
	// unknown traveller is ErrNoTraveller and therefore a 401, rather than an
	// empty result read as "no such object" and answered 404.
	//
	// The version it hands over is ignored, and that is DEC-50: media_objects
	// is not in the emitted document, so nothing about it moves that counter.
	err := WithReadSnapshot(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx, _ int64) error {
		rows, err := readMediaRows(ctx, tx, travellerID, ids)
		out = rows
		return err
	})
	if err != nil {
		return nil, mediaError(err)
	}
	return out, nil
}

// MarkMediaUploaded sets uploaded_at if it is not set, and answers the row
// either way.
func (s MediaStore) MarkMediaUploaded(ctx context.Context, travellerID, id string) (logbook.MediaObject, error) {
	var out logbook.MediaObject
	err := WithTravellerLock(ctx, s.DB, travellerID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, markSQL, travellerID, id); err != nil {
			return fmt.Errorf("postgres: committing media %s: %w", id, err)
		}
		// THE READ IS UNCONDITIONAL AND THAT IS THE RETRY CONTRACT. The UPDATE
		// above matches nothing on a second commit, and a store that reported
		// its own rows-affected as the answer would turn a client's retry into
		// a failure.
		rows, err := readMediaRows(ctx, tx, travellerID, []string{id})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("%w: %s", logbook.ErrNoMediaObject, id)
		}
		out = rows[0]
		return nil
	})
	if err != nil {
		return logbook.MediaObject{}, mediaError(err)
	}
	return out, nil
}

func readMediaRows(ctx context.Context, tx *sql.Tx, travellerID string, ids []string) ([]logbook.MediaObject, error) {
	args := make([]any, 0, len(ids)+1)
	args = append(args, travellerID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, selectSQL(len(ids)), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading media objects: %w", err)
	}
	defer rows.Close()

	var out []logbook.MediaObject
	for rows.Next() {
		var m logbook.MediaObject
		var uploaded sql.NullTime
		if err := rows.Scan(&m.ID, &m.ByteSize, &m.ContentType, &m.CreatedAt, &uploaded); err != nil {
			return nil, fmt.Errorf("postgres: reading a media object: %w", err)
		}
		// `.Valid` IS CHECKED, WHICH IS DEC-102's WHOLE FINDING. Three NOT
		// NULL date columns were scanned through sql.NullTime with .Valid
		// never read, so an absent value became year one and rode out to the
		// client as a date. Here the column is genuinely nullable and the
		// nullness IS the answer — `alreadyExists` is derived from it — so
		// getting this wrong reports every uncommitted object as committed.
		if uploaded.Valid {
			at := uploaded.Time.UTC()
			m.UploadedAt = &at
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// mediaError maps this package's own sentinel onto the domain's.
//
// The two exist separately for the reason internal/postgres and
// internal/logbook both declare ErrNoTraveller: the store's is about a row and
// the domain's is about a rule, and a handler switching on the storage
// package's sentinel would be a handler that knows which database it is
// talking to.
func mediaError(err error) error {
	if errors.Is(err, ErrNoTraveller) {
		return fmt.Errorf("%w: %w", logbook.ErrNoTraveller, err)
	}
	return err
}
