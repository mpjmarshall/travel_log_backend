// The auth store against a real PostgreSQL. Test-first.
//
// This file needs a database and SKIPS, saying so, when there is none —
// internal/postgres/testdb decides that, and it refuses a server below 15
// rather than skipping it. Run `make test-db` and export what it prints.
//
// THREE THINGS HERE CANNOT BE ASSERTED ANYWHERE ELSE, and they are why the
// file exists rather than being folded into internal/auth's fake store:
//
//  1. DEC-65's functional unique index. The fake enforces lower(email)
//     uniqueness in Go; the real store enforces it nowhere in Go at all, and a
//     leg against the fake would be a leg about the fake.
//  2. That the index is actually USED. DEC-65 says to assert it with EXPLAIN
//     rather than by reading, because `WHERE email = $1` is a predicate that
//     compiles, runs, returns zero rows against a differently-cased address,
//     and reports an unknown traveller with no error anywhere.
//  3. The bump/no-bump split (DEC-50). A session write must move
//     logbook_version by ZERO, and only a real travellers row has a
//     logbook_version to move.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"travellog/internal/auth"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func authStore(t *testing.T) (AuthStore, *sql.DB, string) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("applying 0001: %v", err)
	}
	return AuthStore{DB: db}, db, schema
}

func digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func versionOf(t *testing.T, db *sql.DB, travellerID string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT logbook_version FROM travellers WHERE id = $1::uuid`, travellerID).Scan(&v); err != nil {
		t.Fatalf("reading logbook_version: %v", err)
	}
	return v
}

// AuthStore satisfies the interface internal/auth declares (DEC-62: the
// business rules own the contract and the storage implementation meets it).
// Its failure mode is a build error, which is the right one for a claim about
// types.
func TestAuthStoreIsTheStoreAuthDeclared(t *testing.T) {
	var _ auth.Store = AuthStore{}
}

// === travellers ===

func TestCreateTravellerStoresTheAddressExactlyAsTyped(t *testing.T) {
	store, db, _ := authStore(t)
	const typed = "Matt.Marshall@Example.COM"

	tr, err := store.CreateTraveller(context.Background(), typed, "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	if tr.Email != typed {
		t.Errorf("CreateTraveller answered %q, want %q", tr.Email, typed)
	}

	var stored string
	if err := db.QueryRow(`SELECT email FROM travellers WHERE id = $1::uuid`, tr.ID).Scan(&stored); err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if stored != typed {
		t.Errorf("the COLUMN holds %q, want %q.\n"+
			"    DEC-65: the index on lower(email) makes lowercasing on write unnecessary,\n"+
			"    and RFC 5321 makes the local part case-sensitive.", stored, typed)
	}
}

func TestCreateTravellerLeavesTheNameNull(t *testing.T) {
	store, db, _ := authStore(t)
	tr, err := store.CreateTraveller(context.Background(), "matt@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	if tr.Name != nil {
		t.Errorf("CreateTraveller answered a name, %q", *tr.Name)
	}
	var name sql.NullString
	if err := db.QueryRow(`SELECT name FROM travellers WHERE id = $1::uuid`, tr.ID).Scan(&name); err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if name.Valid {
		t.Errorf("the name column holds %q; DEC-61 leaves it NULL until PATCH /v1/me", name.String)
	}
}

func TestCreateTravellerStartsTheLogbookVersionAtZero(t *testing.T) {
	store, db, _ := authStore(t)
	tr, err := store.CreateTraveller(context.Background(), "matt@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	if got := versionOf(t, db, tr.ID); got != 0 {
		t.Errorf("a fresh traveller is at logbook_version %d, want 0", got)
	}
}

// DEC-60's surviving leg, which DEC-65 made stronger: it now fails at the
// database rather than depending on a call site remembering a rule.
func TestASecondRegistrationOfOneAddressInAnotherCasingIsRefused(t *testing.T) {
	store, _, _ := authStore(t)
	ctx := context.Background()

	if _, err := store.CreateTraveller(ctx, "A@B.com", "$argon2id$stub"); err != nil {
		t.Fatalf("the first registration: %v", err)
	}
	for _, variant := range []string{"a@b.com", "A@B.COM", "a@B.Com", "A@B.com"} {
		_, err := store.CreateTraveller(ctx, variant, "$argon2id$stub")
		if !errors.Is(err, auth.ErrEmailTaken) {
			t.Errorf("registering %q over A@B.com answered %v, want auth.ErrEmailTaken", variant, err)
		}
	}
}

func TestTravellerByEmailResolvesAnAddressInAnyCasing(t *testing.T) {
	store, _, _ := authStore(t)
	ctx := context.Background()

	created, err := store.CreateTraveller(ctx, "A@B.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	for _, spelling := range []string{"A@B.com", "a@b.com", "A@B.COM", "a@B.Com"} {
		found, hash, err := store.TravellerByEmail(ctx, spelling)
		if err != nil {
			t.Errorf("TravellerByEmail(%q) = %v", spelling, err)
			continue
		}
		if found.ID != created.ID {
			t.Errorf("TravellerByEmail(%q) answered %s, want %s", spelling, found.ID, created.ID)
		}
		if hash != "$argon2id$stub" {
			t.Errorf("TravellerByEmail(%q) answered the hash %q", spelling, hash)
		}
		if found.Email != "A@B.com" {
			t.Errorf("TravellerByEmail(%q) answered the address %q, want it as typed", spelling, found.Email)
		}
	}
}

func TestTravellerByEmailAnswersNoTravellerForAnAddressNobodyHolds(t *testing.T) {
	store, _, _ := authStore(t)
	if _, _, err := store.TravellerByEmail(context.Background(), "nobody@example.com"); !errors.Is(err, auth.ErrNoTraveller) {
		t.Errorf("TravellerByEmail on an empty table answered %v, want auth.ErrNoTraveller", err)
	}
}

// DEC-65 asks for this to be asserted with EXPLAIN rather than by reading, and
// the negative half is the point: `WHERE email = $1` does not error, does not
// warn, and returns zero rows against a differently-cased stored address — so
// a forgotten rule reads as an address nobody registered.
//
// enable_seqscan is turned off FOR THE TRANSACTION so the plan reflects what
// the index can serve rather than what the planner prefers on a table of four
// rows. That is the right question: on a table of four rows a sequential scan
// IS faster, and the leg is about whether the index is usable at all.
func TestTheLookupUsesTheLowerEmailIndexAndAPlainEqualityDoesNot(t *testing.T) {
	store, db, _ := authStore(t)
	ctx := context.Background()
	for _, email := range []string{"a@b.com", "c@d.com", "e@f.com", "g@h.com"} {
		if _, err := store.CreateTraveller(ctx, email, "$argon2id$stub"); err != nil {
			t.Fatalf("seeding %s: %v", email, err)
		}
	}
	if _, err := db.ExecContext(ctx, `ANALYZE travellers`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	plan := func(query string) string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			t.Fatalf("SET LOCAL: %v", err)
		}
		rows, err := tx.QueryContext(ctx, "EXPLAIN "+query, "a@b.com")
		if err != nil {
			t.Fatalf("EXPLAIN %s: %v", query, err)
		}
		defer rows.Close()
		var out strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scanning the plan: %v", err)
			}
			out.WriteString(line + "\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("reading the plan: %v", err)
		}
		return out.String()
	}

	const index = "travellers_email_lower_key"

	folded := plan(`SELECT id FROM travellers WHERE lower(email) = lower($1)`)
	if !strings.Contains(folded, index) {
		t.Errorf("the lookup this store makes does not use %s:\n%s", index, folded)
	}

	plain := plan(`SELECT id FROM travellers WHERE email = $1`)
	if strings.Contains(plain, index) {
		t.Errorf("`WHERE email = $1` uses %s, so the two predicates are interchangeable\n"+
			"    and DEC-65's rule guards nothing:\n%s", index, plain)
	}
}

// === sessions, and the half of DEC-50 that must move no number ===

func withTravellerRow(t *testing.T) (AuthStore, *sql.DB, auth.Traveller) {
	t.Helper()
	store, db, _ := authStore(t)
	tr, err := store.CreateTraveller(context.Background(), "matt@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	return store, db, tr
}

func TestCreateSessionStoresTheHashAndTheExpiryAndAnswersAnId(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()
	hash := digest("a token")
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Microsecond)

	id, err := store.CreateSession(ctx, tr.ID, hash, expires)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatalf("CreateSession answered no id")
	}

	var stored []byte
	var storedExpiry time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT token_hash, expires_at FROM sessions WHERE id = $1::uuid`, id).
		Scan(&stored, &storedExpiry); err != nil {
		t.Fatalf("reading the session back: %v", err)
	}
	if string(stored) != string(hash) {
		t.Errorf("the stored token_hash is not what was handed in")
	}
	if !storedExpiry.UTC().Equal(expires) {
		t.Errorf("the stored expiry is %s, want %s", storedExpiry.UTC(), expires)
	}
}

// THE LEG THE SLICE EXISTS FOR (DEC-50, and the slice's own definition of
// done). Creating a session and touching one must move logbook_version by
// ZERO. Count them and the phone's whole 85 KB cached log is invalidated on
// every authenticated request, so GET /v1/logbook never once answers 304 in
// real use — which was a real defect (V3-B4) and is invisible until somebody
// measures the cache.
func TestASessionWriteMovesNoLogbookVersion(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()

	before := versionOf(t, db, tr.ID)

	id, err := store.CreateSession(ctx, tr.ID, digest("a token"), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := versionOf(t, db, tr.ID); got != before {
		t.Fatalf("creating a session moved logbook_version from %d to %d", before, got)
	}

	for i := range 5 {
		if err := store.TouchSession(ctx, tr.ID, id, time.Now().UTC()); err != nil {
			t.Fatalf("TouchSession %d: %v", i, err)
		}
	}
	if got := versionOf(t, db, tr.ID); got != before {
		t.Errorf("five touches moved logbook_version from %d to %d.\n"+
			"    `last_used_at` is written on EVERY authenticated request. Counting it\n"+
			"    means every ETag the phone holds is stale the moment it authenticates.",
			before, got)
	}
}

func TestCreateSessionAndTouchSessionBothTakeTheTravellerLock(t *testing.T) {
	store, db, schema := authStore(t)
	tr, err := store.CreateTraveller(context.Background(), "matt@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	other := testdb.Second(t, schema)
	ctx := context.Background()

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithTravellerLock(ctx, db, tr.ID, func(context.Context, *sql.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	if lockIsFree(t, other, tr.ID) {
		t.Fatalf("the lock is not held while a locked write is running — the fixture is wrong")
	}

	blocked := make(chan error, 1)
	go func() {
		_, err := store.CreateSession(ctx, tr.ID, digest("a token"), time.Now().UTC().Add(time.Hour))
		blocked <- err
	}()
	select {
	case err := <-blocked:
		t.Errorf("CreateSession finished (%v) while another writer held this traveller's lock, "+
			"so it is not taking it", err)
	case <-time.After(500 * time.Millisecond):
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the holding write: %v", err)
	}
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("CreateSession, once the lock was free: %v", err)
		}
	case <-timeout():
		t.Fatalf("CreateSession never completed after the lock was released")
	}
}

func TestSessionByTokenHashAnswersTheSessionAndItsTraveller(t *testing.T) {
	store, _, tr := withTravellerRow(t)
	ctx := context.Background()
	hash := digest("a token")
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	id, err := store.CreateSession(ctx, tr.ID, hash, expires)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	session, owner, err := store.SessionByTokenHash(ctx, hash)
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if session.ID != id {
		t.Errorf("session id = %s, want %s", session.ID, id)
	}
	if session.TravellerID != tr.ID {
		t.Errorf("traveller id on the session = %s, want %s", session.TravellerID, tr.ID)
	}
	if string(session.TokenHash) != string(hash) {
		t.Errorf("the session's token_hash did not come back, so the service cannot re-compare it")
	}
	if !session.ExpiresAt.UTC().Equal(expires) {
		t.Errorf("expires_at = %s, want %s", session.ExpiresAt.UTC(), expires)
	}
	if session.RevokedAt != nil {
		t.Errorf("a fresh session is revoked at %s", session.RevokedAt)
	}
	if owner.ID != tr.ID || owner.Email != "matt@example.com" {
		t.Errorf("the traveller came back as %+v", owner)
	}
	if owner.Name != nil {
		t.Errorf("the traveller came back named %q", *owner.Name)
	}
}

func TestSessionByTokenHashAnswersNoSessionForAHashNobodyHolds(t *testing.T) {
	store, _, _ := withTravellerRow(t)
	if _, _, err := store.SessionByTokenHash(context.Background(), digest("never issued")); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("SessionByTokenHash on an unheld hash answered %v, want auth.ErrNoSession", err)
	}
}

func TestSessionByTokenHashCarriesARevocation(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()
	hash := digest("a token")
	id, err := store.CreateSession(ctx, tr.ID, hash, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	session, _, err := store.SessionByTokenHash(ctx, hash)
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if session.RevokedAt == nil {
		t.Errorf("a revoked session came back with revoked_at nil, so nothing above this\n" +
			"    layer can tell it is dead")
	}
}

func TestTouchSessionWritesLastUsedAt(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()
	id, err := store.CreateSession(ctx, tr.ID, digest("a token"), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var before time.Time
	if err := db.QueryRowContext(ctx, `SELECT last_used_at FROM sessions WHERE id = $1::uuid`, id).
		Scan(&before); err != nil {
		t.Fatalf("reading last_used_at: %v", err)
	}

	want := before.Add(90 * time.Minute).UTC().Truncate(time.Microsecond)
	if err := store.TouchSession(ctx, tr.ID, id, want); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	var after time.Time
	if err := db.QueryRowContext(ctx, `SELECT last_used_at FROM sessions WHERE id = $1::uuid`, id).
		Scan(&after); err != nil {
		t.Fatalf("reading last_used_at back: %v", err)
	}
	if !after.UTC().Equal(want) {
		t.Errorf("last_used_at = %s, want %s", after.UTC(), want)
	}
}

func TestTouchSessionRefusesASessionThatIsNotThisTravellersAndOneThatIsGone(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()
	id, err := store.CreateSession(ctx, tr.ID, digest("a token"), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stranger, err := store.CreateTraveller(ctx, "other@example.com", "$argon2id$stub")
	if err != nil {
		t.Fatalf("CreateTraveller: %v", err)
	}
	if err := store.TouchSession(ctx, stranger.ID, id, time.Now().UTC()); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("touching another traveller's session answered %v, want auth.ErrNoSession", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("deleting the session: %v", err)
	}
	if err := store.TouchSession(ctx, tr.ID, id, time.Now().UTC()); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("touching a session that is gone answered %v, want auth.ErrNoSession.\n"+
			"    An UPDATE that matches nothing reports success, which is a sign-in that\n"+
			"    keeps working against a row that is not there.", err)
	}
}

func TestASessionOfATravellerWhoIsGoneIsNotLive(t *testing.T) {
	store, db, tr := withTravellerRow(t)
	ctx := context.Background()
	id, err := store.CreateSession(ctx, tr.ID, digest("a token"), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM travellers WHERE id = $1::uuid`, tr.ID); err != nil {
		t.Fatalf("deleting the traveller: %v", err)
	}
	if err := store.TouchSession(ctx, tr.ID, id, time.Now().UTC()); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("touching the session of a deleted traveller answered %v, want auth.ErrNoSession", err)
	}
}

func TestCreateSessionRefusesATravellerThatIsNotThere(t *testing.T) {
	store, _, _ := authStore(t)
	_, err := store.CreateSession(context.Background(), noTraveller,
		digest("a token"), time.Now().UTC().Add(time.Hour))
	if !errors.Is(err, auth.ErrNoTraveller) {
		t.Errorf("CreateSession for a traveller that is not there answered %v, want auth.ErrNoTraveller.\n"+
			"    The foreign key would refuse the INSERT anyway; what this asserts is that the\n"+
			"    answer is a NAMED sentinel and not a driver error, because the caller's\n"+
			"    response differs — an unknown traveller is a 401 and a driver error is a 500.", err)
	}
}
