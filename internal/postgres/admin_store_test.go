// The reads the panel makes, and the one delete it performs.
package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"travellog/internal/postgres/testdb"
	"travellog/migrations"
)

func adminStore(t *testing.T) (AdminStore, *sql.DB) {
	t.Helper()
	db, schema := testdb.Open(t)
	if _, err := (Migrator{Schema: schema, Logger: quietLogger()}).
		Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return AdminStore{DB: db}, db
}

func makeTraveller(t *testing.T, db *sql.DB, email string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(
		`INSERT INTO travellers (id, email) VALUES (gen_random_uuid(), $1) RETURNING id::text`,
		email).Scan(&id); err != nil {
		t.Fatalf("inserting %s: %v", email, err)
	}
	return id
}

func TestOverviewCountsWhatIsThere(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()

	ada := makeTraveller(t, db, "ada@example.com")
	makeTraveller(t, db, "grace@example.com")
	if _, err := db.Exec(
		`INSERT INTO trips (traveller_id, id, name) VALUES ($1::uuid, 'trip-1', 'Lisbon')`,
		ada); err != nil {
		t.Fatal(err)
	}

	got, err := store.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview() = %v", err)
	}
	if got.Travellers != 2 {
		t.Errorf("Travellers = %d, want 2", got.Travellers)
	}
	if got.Trips != 1 {
		t.Errorf("Trips = %d, want 1", got.Trips)
	}
}

func TestTravellersSearchesAndPages(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	for _, email := range []string{"ada@example.com", "grace@example.com", "alan@example.com"} {
		makeTraveller(t, db, email)
	}

	all, total, err := store.Travellers(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("unfiltered = %d rows of %d total, want 3 of 3", len(all), total)
	}

	found, total, err := store.Travellers(ctx, "GRACE", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(found) != 1 || found[0].Email != "grace@example.com" {
		t.Errorf("searching GRACE gave %d of %d, want grace@example.com: the search "+
			"must ignore case or an operator typing a capital finds nobody",
			len(found), total)
	}

	page, total, err := store.Travellers(ctx, "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d on a later page, want 3: the pager needs the whole count, "+
			"not the size of the page it was handed", total)
	}
	if len(page) != 1 {
		t.Errorf("the third row alone = %d rows, want 1", len(page))
	}
}

func TestTravellerDetailCountsOnlyThatTravellersRows(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	ada := makeTraveller(t, db, "ada@example.com")
	grace := makeTraveller(t, db, "grace@example.com")

	for _, id := range []string{ada, grace} {
		if _, err := db.Exec(
			`INSERT INTO trips (traveller_id, id, name) VALUES ($1::uuid, 'trip-1', 'Theirs')`,
			id); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Traveller(ctx, ada)
	if err != nil {
		t.Fatalf("Traveller() = %v", err)
	}
	if got.Email != "ada@example.com" {
		t.Errorf("Email = %q", got.Email)
	}
	if got.Trips != 1 {
		t.Errorf("Trips = %d, want 1: a count that is not scoped to one traveller "+
			"reports the whole deployment", got.Trips)
	}
}

func TestAnUnknownTravellerIsNotFound(t *testing.T) {
	store, _ := adminStore(t)
	_, err := store.Traveller(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("Traveller() found a traveller that does not exist")
	}
}

func TestLiveSessionsNameTheirTraveller(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	ada := makeTraveller(t, db, "ada@example.com")

	if _, err := db.Exec(
		`INSERT INTO sessions (traveller_id, id, token_hash, expires_at)
		 VALUES ($1::uuid, gen_random_uuid(), decode(repeat('00', 32), 'hex'), $2)`,
		ada, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Sessions(ctx, "")
	if err != nil {
		t.Fatalf("Sessions() = %v", err)
	}
	if len(rows) != 1 || rows[0].Email != "ada@example.com" {
		t.Fatalf("Sessions() = %+v, want one row naming ada", rows)
	}

	mine, err := store.Sessions(ctx, ada)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 {
		t.Errorf("Sessions(ada) = %d rows, want 1", len(mine))
	}
}

func TestAnExpiredSessionIsNotLive(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	ada := makeTraveller(t, db, "ada@example.com")

	if _, err := db.Exec(
		`INSERT INTO sessions (traveller_id, id, token_hash, created_at, expires_at)
		 VALUES ($1::uuid, gen_random_uuid(), decode(repeat('00', 32), 'hex'), $2, $3)`,
		ada, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Sessions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("Sessions() = %d rows, want 0: an expired session is not somebody "+
			"who is signed in, and showing it invites revoking nothing", len(rows))
	}
}

func TestInvitesReportWhetherTheyAreSpent(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO invite_codes (code_hash, note)
		 VALUES (decode(repeat('01', 32), 'hex'), 'for matt')`); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Invites(ctx)
	if err != nil {
		t.Fatalf("Invites() = %v", err)
	}
	if len(rows) != 1 || rows[0].Note != "for matt" || rows[0].Used {
		t.Errorf("Invites() = %+v, want one unused invite noted 'for matt'", rows)
	}
}
