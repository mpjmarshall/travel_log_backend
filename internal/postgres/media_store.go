// media_objects, and's two statements the begin route is made of.
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

// beginSQL is the upsert, and the `WHERE` on the conflict branch is the whole
// of it.
const beginSQL = `INSERT INTO media_objects (traveller_id, id, byte_size, content_type)
	VALUES ($1::uuid, $2, $3, $4)
	ON CONFLICT (traveller_id, id) DO UPDATE
		SET byte_size = EXCLUDED.byte_size,
		    content_type = EXCLUDED.content_type
		WHERE media_objects.uploaded_at IS NULL`

// selectSQL reads the rows back, and it is a separate statement because
// `RETURNING` cannot do this job.
func selectSQL(n int) string {
	holders := make([]string, n)
	for i := range holders {
		holders[i] = "$" + strconv.Itoa(i+2)
	}
	return `SELECT id, byte_size, content_type, created_at, uploaded_at
		FROM media_objects
		WHERE traveller_id = $1::uuid AND id IN (` + strings.Join(holders, ", ") + `)`
}

// markSQL is the commit, and the `WHERE uploaded_at is NULL` is the retry
// contract.
const markSQL = `UPDATE media_objects SET uploaded_at = now()
	WHERE traveller_id = $1::uuid AND id = $2 AND uploaded_at IS NULL`

// BeginMedia upserts the declared object and answers the row as it STANDS,
// On the conflict path is not the row that was proposed.
func (s MediaStore) BeginMedia(ctx context.Context, travellerID string, b logbook.MediaBegin) (logbook.MediaObject, error) {
	if b.SHA256 == nil || b.ByteSize == nil || b.ContentType == nil {
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

// MediaObjects answers the rows these ids name, and silently omits the ones
// that are not there.
func (s MediaStore) MediaObjects(ctx context.Context, travellerID string, ids []string) ([]logbook.MediaObject, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []logbook.MediaObject
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
		if uploaded.Valid {
			at := uploaded.Time.UTC()
			m.UploadedAt = &at
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// mediaError maps this package's own sentinel onto the domain's.
func mediaError(err error) error {
	if errors.Is(err, ErrNoTraveller) {
		return fmt.Errorf("%w: %w", logbook.ErrNoTraveller, err)
	}
	return err
}
