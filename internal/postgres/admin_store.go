package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AdminStore holds every read the panel makes. Its writes go through the
// stores that already own those invariants.
type AdminStore struct{ DB *sql.DB }

// Overview is the landing page's figures.
type Overview struct {
	Travellers    int
	Trips         int
	Photos        int
	Places        int
	LiveSessions  int
	UnusedInvites int
	BucketBytes   int64
}

// TravellerRow is one line of the travellers list.
type TravellerRow struct {
	ID             string
	Email          string
	Name           string
	CreatedAt      time.Time
	LogbookVersion int64
	Trips          int
	Photos         int
}

// TravellerDetail is that row plus everything the detail page counts. It
// carries no photograph and no URL, by decision.
type TravellerDetail struct {
	TravellerRow
	Cities      int
	Places      int
	Visits      int
	Walks       int
	BucketBytes int64
}

// SessionRow is one live session, named by its traveller.
type SessionRow struct {
	ID         string
	Email      string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

// InviteRow is one invite, spent or not.
type InviteRow struct {
	Hash      []byte
	Note      string
	CreatedAt time.Time
	Used      bool
	UsedBy    string
}

const overviewSQL = `
SELECT (SELECT count(*) FROM travellers),
       (SELECT count(*) FROM trips),
       (SELECT count(*) FROM photos),
       (SELECT count(*) FROM places),
       (SELECT count(*) FROM sessions
          WHERE expires_at > now() AND revoked_at IS NULL),
       (SELECT count(*) FROM invite_codes WHERE used_at IS NULL),
       (SELECT coalesce(sum(byte_size), 0) FROM media_objects
          WHERE uploaded_at IS NOT NULL)`

func (s AdminStore) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	err := s.DB.QueryRowContext(ctx, overviewSQL).Scan(
		&o.Travellers, &o.Trips, &o.Photos, &o.Places,
		&o.LiveSessions, &o.UnusedInvites, &o.BucketBytes)
	if err != nil {
		return Overview{}, fmt.Errorf("postgres: the admin overview: %w", err)
	}
	return o, nil
}

const travellersSQL = `
SELECT t.id::text, t.email, coalesce(t.name, ''), t.created_at, t.logbook_version,
       (SELECT count(*) FROM trips  WHERE traveller_id = t.id),
       (SELECT count(*) FROM photos WHERE traveller_id = t.id),
       count(*) OVER ()
  FROM travellers t
 WHERE ($1 = '' OR position(lower($1) in lower(t.email)) > 0)
 ORDER BY t.created_at, t.id
 LIMIT $2 OFFSET $3`

// Travellers answers one page and the whole count, which the pager needs and
// the page length cannot give it.
func (s AdminStore) Travellers(ctx context.Context, query string, limit, offset int) ([]TravellerRow, int, error) {
	rows, err := s.DB.QueryContext(ctx, travellersSQL, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: listing travellers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []TravellerRow{}
	total := 0
	for rows.Next() {
		var r TravellerRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Name, &r.CreatedAt,
			&r.LogbookVersion, &r.Trips, &r.Photos, &total); err != nil {
			return nil, 0, fmt.Errorf("postgres: reading a traveller: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: listing travellers: %w", err)
	}
	if len(out) == 0 {
		total, err := s.countTravellers(ctx, query)
		return out, total, err
	}
	return out, total, nil
}

// countTravellers answers the total when the page itself is empty, where the
// window function has no row to ride on.
func (s AdminStore) countTravellers(ctx context.Context, query string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM travellers
		  WHERE ($1 = '' OR position(lower($1) in lower(email)) > 0)`, query).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting travellers: %w", err)
	}
	return n, nil
}

const travellerSQL = `
SELECT t.id::text, t.email, coalesce(t.name, ''), t.created_at, t.logbook_version,
       (SELECT count(*) FROM trips   WHERE traveller_id = t.id),
       (SELECT count(*) FROM photos  WHERE traveller_id = t.id),
       (SELECT count(*) FROM cities  WHERE traveller_id = t.id),
       (SELECT count(*) FROM places  WHERE traveller_id = t.id),
       (SELECT count(*) FROM visits  WHERE traveller_id = t.id),
       (SELECT count(*) FROM walks   WHERE traveller_id = t.id),
       (SELECT coalesce(sum(byte_size), 0) FROM media_objects
          WHERE traveller_id = t.id AND uploaded_at IS NOT NULL)
  FROM travellers t
 WHERE t.id = $1::uuid`

func (s AdminStore) Traveller(ctx context.Context, id string) (TravellerDetail, error) {
	var d TravellerDetail
	err := s.DB.QueryRowContext(ctx, travellerSQL, id).Scan(
		&d.ID, &d.Email, &d.Name, &d.CreatedAt, &d.LogbookVersion,
		&d.Trips, &d.Photos, &d.Cities, &d.Places, &d.Visits, &d.Walks, &d.BucketBytes)
	if err != nil {
		return TravellerDetail{}, fmt.Errorf("postgres: the traveller %s: %w", id, err)
	}
	return d, nil
}

const sessionsSQL = `
SELECT s.id::text, t.email, s.created_at, s.last_used_at, s.expires_at
  FROM sessions s JOIN travellers t ON t.id = s.traveller_id
 WHERE s.expires_at > now() AND s.revoked_at IS NULL
   AND ($1 = '' OR s.traveller_id = $1::uuid)
 ORDER BY s.last_used_at DESC`

// Sessions answers only live ones: an expired session is not somebody signed
// in, and offering to revoke it revokes nothing.
func (s AdminStore) Sessions(ctx context.Context, travellerID string) ([]SessionRow, error) {
	rows, err := s.DB.QueryContext(ctx, sessionsSQL, travellerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []SessionRow{}
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.ID, &r.Email, &r.CreatedAt, &r.LastUsedAt, &r.ExpiresAt); err != nil {
			return nil, fmt.Errorf("postgres: reading a session: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const invitesSQL = `
SELECT i.code_hash, coalesce(i.note, ''), i.created_at,
       i.used_at IS NOT NULL, coalesce(t.email, '')
  FROM invite_codes i LEFT JOIN travellers t ON t.id = i.used_by
 ORDER BY i.created_at DESC`

func (s AdminStore) Invites(ctx context.Context) ([]InviteRow, error) {
	rows, err := s.DB.QueryContext(ctx, invitesSQL)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing invites: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []InviteRow{}
	for rows.Next() {
		var r InviteRow
		if err := rows.Scan(&r.Hash, &r.Note, &r.CreatedAt, &r.Used, &r.UsedBy); err != nil {
			return nil, fmt.Errorf("postgres: reading an invite: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rename goes through the logbook store so logbook_version moves, or every
// client keeps showing the old name until something else happens to move it.
func (s AdminStore) Rename(ctx context.Context, travellerID, name string) (int64, error) {
	_, version, err := LogbookStore{DB: s.DB}.SetTravellerName(ctx, travellerID, name)
	return version, err
}

// MintInvite records a hashed invite. The plaintext never reaches this layer.
func (s AdminStore) MintInvite(ctx context.Context, hash []byte, note string) error {
	return AuthStore{DB: s.DB}.MintInvite(ctx, hash, note)
}

// DeleteInvite revokes by removing the row: an unused invite is a live
// credential, and the clearest revocation is that it stops existing.
func (s AdminStore) DeleteInvite(ctx context.Context, hash []byte) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM invite_codes WHERE code_hash = $1`, hash)
	if err != nil {
		return fmt.Errorf("postgres: deleting an invite: %w", err)
	}
	return nil
}

// RevokeSessionByID stamps one session rather than deleting it, so the row
// stays as a record of a session that existed.
func (s AdminStore) RevokeSessionByID(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1::uuid AND revoked_at IS NULL`,
		sessionID)
	if err != nil {
		return fmt.Errorf("postgres: revoking the session %s: %w", sessionID, err)
	}
	return nil
}
