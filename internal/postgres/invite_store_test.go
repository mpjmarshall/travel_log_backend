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
	if err := store.ClaimInvite(ctx, auth.HashInvite(code), traveller); err != nil {
		t.Fatalf("claiming: %v", err)
	}
	if err := store.ClaimInvite(ctx, auth.HashInvite(code), traveller); !errors.Is(err, auth.ErrInviteSpent) {
		t.Fatalf("a second claim answered %v, want ErrInviteSpent", err)
	}
}

func TestAnUnknownInviteIsRefused(t *testing.T) {
	store, db := inviteStore(t)
	if err := store.ClaimInvite(context.Background(), auth.HashInvite("nobodys-code"), aTraveller(t, db)); !errors.Is(err, auth.ErrInviteSpent) {
		t.Fatalf("answered %v, want ErrInviteSpent", err)
	}
}

func TestTwoRegistrationsRacingForOneInviteAdmitOne(t *testing.T) {
	store, db := inviteStore(t)
	ctx := context.Background()
	code, hash, _ := auth.NewInvite()
	if err := store.MintInvite(ctx, hash, ""); err != nil {
		t.Fatal(err)
	}
	a, b := anotherTraveller(t, db, "a@example.com"), anotherTraveller(t, db, "b@example.com")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = store.ClaimInvite(ctx, auth.HashInvite(code), id)
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
	if err := store.ClaimInvite(ctx, auth.HashInvite(code), id); err != nil {
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
	tr, err := AuthStore{DB: db}.CreateTraveller(context.Background(), email, "$argon2id$stub")
	if err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	return tr.ID
}
