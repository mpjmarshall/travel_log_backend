// Sign-in codes against a real PostgreSQL. Test-first.
//
// Skips, saying so, when there is no database — internal/postgres/testdb
// decides that. Run `make test-db` and export what it prints.
//
// THE ONE-ROW-PER-TRAVELLER RULE IS THE WHOLE SECURITY ARGUMENT AND IT LIVES
// IN THE SCHEMA. A six-digit code survives five wrong guesses; that is a bound
// only if a traveller can hold ONE live code at a time. With many, an attacker
// requests two hundred codes and has a thousand guesses against a million —
// and every one of those codes is valid. The primary key is what makes
// requesting a code REPLACE rather than accumulate, so this is asserted here
// rather than in the service, where a fake would be asserting itself.
package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"travellog/internal/auth"
	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func codeStore(t *testing.T) (AuthStore, *sql.DB, string) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return AuthStore{DB: db}, db, schema
}

func TestACodeIsIssuedAndFoundByItsDigest(t *testing.T) {
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)

	_, hash, err := auth.NewCode(id)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(auth.CodeTTL).UTC().Truncate(time.Millisecond)
	if err := store.IssueCode(ctx, id, hash, expires); err != nil {
		t.Fatalf("issuing: %v", err)
	}

	got, err := store.CodeFor(ctx, id)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if !auth.SameHash(got.Hash, hash) {
		t.Fatal("the digest read back is not the one issued")
	}
	if got.Attempts != 0 {
		t.Fatalf("a fresh code has %d attempts, want 0", got.Attempts)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Fatalf("expiry round-tripped as %v, want %v", got.ExpiresAt, expires)
	}
}

func TestIssuingASecondCodeREPLACESTheFirst(t *testing.T) {
	// The security argument. Without this, five attempts per code is not a
	// bound on anything: an attacker requests codes until the budget is as
	// large as they like.
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)

	_, first, _ := auth.NewCode(id)
	_, second, _ := auth.NewCode(id)
	exp := time.Now().Add(auth.CodeTTL).UTC()
	if err := store.IssueCode(ctx, id, first, exp); err != nil {
		t.Fatal(err)
	}
	if err := store.IssueCode(ctx, id, second, exp); err != nil {
		t.Fatalf("issuing a second code: %v", err)
	}

	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sign_in_codes WHERE traveller_id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d live codes for one traveller, want exactly 1", rows)
	}

	got, err := store.CodeFor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.SameHash(got.Hash, second) {
		t.Fatal("the first code survived the second being issued")
	}
}

func TestIssuingResetsTheAttemptCount(t *testing.T) {
	// The converse of the rule above, and it has to be true or a traveller
	// who mistypes five times can never sign in again.
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)
	exp := time.Now().Add(auth.CodeTTL).UTC()

	_, first, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, first, exp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CountAttempt(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	_, second, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, second, exp); err != nil {
		t.Fatal(err)
	}
	got, err := store.CodeFor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 0 {
		t.Fatalf("a newly issued code carries %d attempts, want 0", got.Attempts)
	}
}

func TestCountAttemptAnswersTheNewTotal(t *testing.T) {
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)
	_, hash, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, hash, time.Now().Add(auth.CodeTTL).UTC()); err != nil {
		t.Fatal(err)
	}

	for want := 1; want <= 3; want++ {
		got, err := store.CountAttempt(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("attempt %d counted as %d", want, got)
		}
	}
}

func TestBurningACodeLeavesNothing(t *testing.T) {
	// Single use. The row goes rather than being marked, so a replay is
	// indistinguishable from a code that never existed — which is the same
	// call Authenticate makes about an unknown token.
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)
	_, hash, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, hash, time.Now().Add(auth.CodeTTL).UTC()); err != nil {
		t.Fatal(err)
	}

	if err := store.BurnCode(ctx, id); err != nil {
		t.Fatalf("burning: %v", err)
	}
	if _, err := store.CodeFor(ctx, id); err != auth.ErrNoCode {
		t.Fatalf("after burning, CodeFor answered %v, want ErrNoCode", err)
	}
}

func TestCodeForAnswersErrNoCodeWhenThereIsNone(t *testing.T) {
	store, db, _ := codeStore(t)
	id := aTraveller(t, db)
	if _, err := store.CodeFor(context.Background(), id); err != auth.ErrNoCode {
		t.Fatalf("answered %v, want ErrNoCode", err)
	}
}

func TestDeletingATravellerTakesTheirCodeWithThem(t *testing.T) {
	// The deletion decision is immediate and total, and a sign-in code is one
	// of the things that has to go with the traveller. A foreign key is what
	// makes that true without anybody remembering it.
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)
	_, hash, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, hash, time.Now().Add(auth.CodeTTL).UTC()); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM travellers WHERE id = $1`, id); err != nil {
		t.Fatalf("deleting the traveller: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sign_in_codes WHERE traveller_id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d codes survived the traveller being deleted", rows)
	}
}

func TestACodeWriteDoesNotMoveTheLogbookVersion(t *testing.T) {
	// DEC-50's split, which every store here is held to: signing in is not a
	// change to the log, so it must move logbook_version by zero. A bump
	// would make every device re-fetch the whole log because somebody typed
	// a code.
	store, db, _ := codeStore(t)
	ctx := context.Background()
	id := aTraveller(t, db)

	var before int64
	if err := db.QueryRowContext(ctx,
		`SELECT logbook_version FROM travellers WHERE id = $1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, hash, _ := auth.NewCode(id)
	if err := store.IssueCode(ctx, id, hash, time.Now().Add(auth.CodeTTL).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CountAttempt(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := store.BurnCode(ctx, id); err != nil {
		t.Fatal(err)
	}

	var after int64
	if err := db.QueryRowContext(ctx,
		`SELECT logbook_version FROM travellers WHERE id = $1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("logbook_version moved %d -> %d for a sign-in code", before, after)
	}
}
