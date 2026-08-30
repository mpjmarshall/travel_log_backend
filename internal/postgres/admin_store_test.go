// The reads the panel makes, and the one delete it performs.
package postgres

import (
	"context"
	"database/sql"
	"strings"
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

func TestRenameMovesTheLogbookVersion(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	ada := makeTraveller(t, db, "ada@example.com")

	var before int64
	if err := db.QueryRow(
		`SELECT logbook_version FROM travellers WHERE id = $1::uuid`, ada).Scan(&before); err != nil {
		t.Fatal(err)
	}

	after, err := store.Rename(ctx, ada, "Ada Lovelace")
	if err != nil {
		t.Fatalf("Rename() = %v", err)
	}
	if after <= before {
		t.Errorf("logbook_version went %d -> %d: a rename that does not move it "+
			"leaves every client showing the old name until something else does",
			before, after)
	}

	var name string
	if err := db.QueryRow(
		`SELECT name FROM travellers WHERE id = $1::uuid`, ada).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Ada Lovelace" {
		t.Errorf("name = %q", name)
	}
}

func TestDeletingAnInviteRemovesTheRow(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	hash := make([]byte, 32)
	hash[0] = 7

	if err := store.MintInvite(ctx, hash, "for matt"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteInvite(ctx, hash); err != nil {
		t.Fatalf("DeleteInvite() = %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM invite_codes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d invites remain: revoking must remove the credential, not mark it", n)
	}
}

func TestRevokingASessionTakesItOutOfTheLiveList(t *testing.T) {
	store, db := adminStore(t)
	ctx := context.Background()
	ada := makeTraveller(t, db, "ada@example.com")

	var id string
	if err := db.QueryRow(
		`INSERT INTO sessions (traveller_id, id, token_hash, expires_at)
		 VALUES ($1::uuid, gen_random_uuid(), decode(repeat('00', 32), 'hex'), $2)
		 RETURNING id::text`,
		ada, time.Now().Add(time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}

	if err := store.RevokeSessionByID(ctx, id); err != nil {
		t.Fatalf("RevokeSessionByID() = %v", err)
	}
	rows, err := store.Sessions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("%d sessions still live after revoking the only one", len(rows))
	}
}

func TestDeletingATravellerTakesEveryCascadedRowAndNamesTheirObjects(t *testing.T) {
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
		if _, err := db.Exec(
			`INSERT INTO media_objects (traveller_id, id, byte_size, content_type, uploaded_at)
			 VALUES ($1::uuid, $2, 100, 'image/jpeg', now())`,
			id, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}

	objects, err := store.DeleteTraveller(ctx, ada)
	if err != nil {
		t.Fatalf("DeleteTraveller() = %v", err)
	}
	if len(objects) != 1 || objects[0] != strings.Repeat("a", 64) {
		t.Errorf("objects = %v, want ada's one object id: without them the bytes "+
			"are unreachable for ever", objects)
	}

	var travellers, trips, media int
	if err := db.QueryRow(
		`SELECT (SELECT count(*) FROM travellers), (SELECT count(*) FROM trips),
		        (SELECT count(*) FROM media_objects)`).Scan(&travellers, &trips, &media); err != nil {
		t.Fatal(err)
	}
	if travellers != 1 || trips != 1 || media != 1 {
		t.Errorf("after deleting one of two: travellers=%d trips=%d media=%d, want 1 1 1: "+
			"the cascade must take that traveller's rows and nobody else's",
			travellers, trips, media)
	}
}

func TestDeletingATravellerWhoIsNotThereIsAnError(t *testing.T) {
	store, _ := adminStore(t)
	_, err := store.DeleteTraveller(context.Background(),
		"00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("deleting a traveller that does not exist reported success")
	}
}
