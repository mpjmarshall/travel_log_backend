package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"travellog/internal/auth"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func inviteStore(t *testing.T) (AuthStore, *sql.DB) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return AuthStore{DB: db}, db
}

func TestAnInviteIsMintedHashedAndClaimedOnce(t *testing.T) {
	store, db := inviteStore(t)
	ctx := context.Background()
	code, hash, err := auth.NewInvite()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MintInvite(ctx, hash, "for matt"); err != nil {
		t.Fatal(err)
	}

	var plaintextRows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM invite_codes WHERE encode(code_hash,'escape') = $1`, code).
		Scan(&plaintextRows); err != nil {
		t.Fatal(err)
	}
	if plaintextRows != 0 {
		t.Fatal("the plaintext invite is in the table")
	}

	traveller := aTraveller(t, db)
	if err := store.SpendInvite(ctx, auth.HashInvite(code)); err != nil {
		t.Fatalf("spending: %v", err)
	}
	if err := store.RecordInviteUser(ctx, auth.HashInvite(code), traveller); err != nil {
		t.Fatalf("recording who spent it: %v", err)
	}
	if err := store.SpendInvite(ctx, auth.HashInvite(code)); !errors.Is(err, auth.ErrInviteSpent) {
		t.Fatalf("a second spend answered %v, want ErrInviteSpent", err)
	}
}

func TestAnUnknownInviteIsRefused(t *testing.T) {
	store, _ := inviteStore(t)
	if err := store.SpendInvite(context.Background(), auth.HashInvite("nobodys-code")); !errors.Is(err, auth.ErrInviteSpent) {
		t.Fatalf("answered %v, want ErrInviteSpent", err)
	}
}

func TestTwoRegistrationsRacingForOneInviteAdmitOne(t *testing.T) {
	store, _ := inviteStore(t)
	ctx := context.Background()
	code, hash, _ := auth.NewInvite()
	if err := store.MintInvite(ctx, hash, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.SpendInvite(ctx, auth.HashInvite(code))
		}()
	}
	wg.Wait()

	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if !errors.Is(err, auth.ErrInviteSpent) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of 2 racing claims succeeded, want exactly 1", won)
	}
}

func TestDeletingTheTravellerLeavesTheInviteSpent(t *testing.T) {
	store, db := inviteStore(t)
	ctx := context.Background()
	code, hash, _ := auth.NewInvite()
	if err := store.MintInvite(ctx, hash, ""); err != nil {
		t.Fatal(err)
	}
	id := aTraveller(t, db)
	if err := store.SpendInvite(ctx, auth.HashInvite(code)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInviteUser(ctx, auth.HashInvite(code), id); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM travellers WHERE id = $1`, id); err != nil {
		t.Fatalf("deleting a traveller who used an invite: %v", err)
	}

	var usedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT used_at FROM invite_codes`).Scan(&usedAt); err != nil {
		t.Fatal(err)
	}
	if !usedAt.Valid {
		t.Fatal("deleting the traveller handed their invite back")
	}
}

func anotherTraveller(t *testing.T, db *sql.DB, email string) string {
	t.Helper()
	tr, err := AuthStore{DB: db}.CreateTraveller(context.Background(), email)
	if err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	return tr.ID
}

// The gate, through the real service and a real database. The legs above are
// about one statement; this is about the order two of them run in.
func TestARefusedInviteLeavesNoTravellerRow(t *testing.T) {
	store, db := inviteStore(t)
	service := &auth.Service{Store: store}
	ctx := context.Background()

	if _, err := service.RegisterWithInvite(ctx, "stranger@example.com", "NOTAREALINVITE01"); !errors.Is(err, auth.ErrInviteSpent) {
		t.Fatalf("registering with an invite that never existed = %v, want ErrInviteSpent", err)
	}

	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM travellers WHERE lower(email) = 'stranger@example.com'`).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d traveller row(s) after a registration that answered ErrInviteSpent, want 0.\n"+
			"    The invite gate is the only control on who gets an account, and a\n"+
			"    row here is an account: POST /v1/auth/code at that address mails a\n"+
			"    sign-in code and POST /v1/auth/session turns it into a token.", rows)
	}
}

// used_at is the gate and used_by is provenance, so a fix that dropped the
// second write would pass every leg above this one.
func TestASuccessfulRegistrationRecordsWhoSpentTheInvite(t *testing.T) {
	store, db := inviteStore(t)
	service := &auth.Service{Store: store}
	ctx := context.Background()
	code, hash, err := auth.NewInvite()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MintInvite(ctx, hash, "for the leg"); err != nil {
		t.Fatal(err)
	}

	tr, err := service.RegisterWithInvite(ctx, "invited@example.com", code)
	if err != nil {
		t.Fatalf("registering behind a good invite: %v", err)
	}

	var usedBy sql.NullString
	var usedAt sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT used_by, used_at FROM invite_codes WHERE code_hash = $1`, hash).
		Scan(&usedBy, &usedAt); err != nil {
		t.Fatal(err)
	}
	if !usedAt.Valid {
		t.Error("used_at is null after a successful registration: the invite is not spent")
	}
	if usedBy.String != tr.ID {
		t.Errorf("used_by = %q, want the traveller it made, %q", usedBy.String, tr.ID)
	}
}
