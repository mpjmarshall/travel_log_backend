// media_objects against a real PostgreSQL: the bounded conflict branch, the
// separate SELECT, and the reference that is only legal once the bytes have
// landed.
//
// EVERY LEG HERE NEEDS THE REAL DATABASE AND SAYS SO WHEN THERE IS NONE. What
// is here is exactly what a fake cannot say: that `ON CONFLICT … WHERE` really
// leaves the row alone, that `DO UPDATE … WHERE <false>` really returns zero
// rows, and that the four foreign keys really refuse a reference.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

func begun(t *testing.T, db *sql.DB, travellerID, digest string, size int64, contentType string) logbook.MediaObject {
	t.Helper()
	row, err := MediaStore{DB: db}.BeginMedia(context.Background(), travellerID,
		logbook.MediaBegin{SHA256: &digest, ByteSize: &size, ContentType: &contentType})
	if err != nil {
		t.Fatalf("BeginMedia(%s, %d, %s): %v", digest, size, contentType, err)
	}
	return row
}

// A CLIENT RE-BEGINNING AN ALREADY-COMMITTED DIGEST MUST NOT BE ABLE TO
// RESTATE WHAT THOSE BYTES ARE.
//
// EXECUTED BY THE DATABASE LENS AGAINST THE REAL TABLE, BEFORE THE GUARD
// EXISTED: without `WHERE media_objects.uploaded_at IS NULL` on the conflict
// branch, a committed `(10 | image/png)` row became `(999999 | text/html)`.
// Migration 0003's allowlist does NOT close it, because any
// allowlisted-but-wrong type passes — this leg re-declares a png as a jpeg,
// which the CHECK is perfectly happy with and which makes the row lie about an
// object whose bytes cannot change.
//
// MUTATION: drop the WHERE and the second half of this leg reddens on both
// columns.
func TestReBeginningACommittedObjectCannotRestateWhatItIs(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	store := MediaStore{DB: db}

	digest := strings.Repeat("d", 64)
	first := begun(t, db, tid, digest, 10, "image/png")
	if first.Committed() {
		t.Fatalf("a first begin answered a committed row — `alreadyExists` is " +
			"derived from uploaded_at, and a row that exists is not the same thing")
	}

	// THE UNCOMMITTED ROW IS STILL MUTABLE, and it has to be: a client that
	// picked a different rendition of the same photograph and re-began is
	// correcting a declaration about bytes that are not there yet.
	corrected := begun(t, db, tid, digest, 4096, "image/jpeg")
	if corrected.ByteSize != 4096 || corrected.ContentType != "image/jpeg" {
		t.Fatalf("re-beginning an UNCOMMITTED object left it at %d | %s — the "+
			"conflict branch must update while uploaded_at is NULL",
			corrected.ByteSize, corrected.ContentType)
	}

	if _, err := store.MarkMediaUploaded(ctx, tid, digest); err != nil {
		t.Fatalf("MarkMediaUploaded: %v", err)
	}

	again := begun(t, db, tid, digest, 999999, "image/png")
	if !again.Committed() {
		t.Errorf("alreadyExists = false on a committed digest")
	}
	if again.ByteSize != 4096 || again.ContentType != "image/jpeg" {
		t.Errorf("the committed row now reads %d | %s, want 4096 | image/jpeg — the "+
			"conflict branch rewrote bytes the client no longer controls",
			again.ByteSize, again.ContentType)
	}

	// AND THE ROW ON DISK, not only what the method answered. A store that
	// re-read correctly and wrote wrongly would pass everything above.
	var size int64
	var mediaType string
	if err := db.QueryRowContext(ctx,
		`SELECT byte_size, content_type FROM media_objects WHERE traveller_id=$1::uuid AND id=$2`,
		tid, digest).Scan(&size, &mediaType); err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if size != 4096 || mediaType != "image/jpeg" {
		t.Errorf("on disk the row reads %d | %s, want 4096 | image/jpeg", size, mediaType)
	}
}

// THE SEPARATE SELECT IS NOT A STYLE CHOICE, AND THIS IS THE MEASUREMENT THAT
// SAYS SO.
//
// v6 deleted the `RETURNING` projection as OE-4 with the reason "not an xmax
// trick", which leaves the door open for somebody to put it back as an
// ordinary projection. `DO UPDATE … WHERE <false>` returns ZERO ROWS — no row
// emitted at all — so a handler reading its answer off RETURNING gets NOTHING
// on exactly the `alreadyExists` path, which is the one path the response is
// about. This leg runs the statement WITH a RETURNING clause and counts what
// comes back.
func TestTheSuppressedConflictBranchEmitsNoRowAtAll(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	digest := strings.Repeat("e", 64)
	begun(t, db, tid, digest, 10, "image/png")
	store := MediaStore{DB: db}
	if _, err := store.MarkMediaUploaded(ctx, tid, digest); err != nil {
		t.Fatalf("MarkMediaUploaded: %v", err)
	}

	rows, err := db.QueryContext(ctx, beginSQL+" RETURNING id", tid, digest, 999999, "image/png")
	if err != nil {
		t.Fatalf("running the upsert with a RETURNING clause: %v", err)
	}
	defer rows.Close()

	var returned int
	for rows.Next() {
		returned++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the RETURNING rows: %v", err)
	}
	if returned != 0 {
		t.Fatalf("the suppressed conflict branch emitted %d row(s) — if that is "+
			"ever true, this comment and OE-4's reasoning are both wrong", returned)
	}

	// THE CONTROL. On the path where the branch is NOT suppressed, RETURNING
	// does emit a row — which is what makes the zero above a fact about the
	// WHERE rather than about RETURNING.
	fresh := strings.Repeat("f", 64)
	live, err := db.QueryContext(ctx, beginSQL+" RETURNING id", tid, fresh, 10, "image/png")
	if err != nil {
		t.Fatalf("the control upsert: %v", err)
	}
	defer live.Close()
	var control int
	for live.Next() {
		control++
	}
	if control != 1 {
		t.Errorf("an ordinary insert emitted %d rows through RETURNING, want 1", control)
	}
}

// A COMMIT IS IDEMPOTENT AT THE STORE, WHICH IS WHAT MAKES THE RETRY CONTRACT
// POSSIBLE AT THE ROUTE (SAF-MIN-12).
//
// The bucket-versus-database seam is the only non-atomic one in the plan: the
// bucket confirms, the update fails, and the object exists with uploaded_at
// NULL — bytes the user has uploaded and cannot attach.
func TestMarkingAnAlreadyUploadedObjectChangesNothingAndAnswersTheRow(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	store := MediaStore{DB: db}

	digest := strings.Repeat("1", 64)
	begun(t, db, tid, digest, 10, "image/png")

	first, err := store.MarkMediaUploaded(ctx, tid, digest)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second, err := store.MarkMediaUploaded(ctx, tid, digest)
	if err != nil {
		t.Fatalf("second commit: %v — a client that lost the first response has "+
			"no route to retry that says so", err)
	}
	if first.UploadedAt == nil || second.UploadedAt == nil {
		t.Fatalf("uploadedAt is nil after a commit: %v / %v", first.UploadedAt, second.UploadedAt)
	}
	if !first.UploadedAt.Equal(*second.UploadedAt) {
		t.Errorf("the second commit moved uploaded_at from %s to %s — the sweep's "+
			"grace window keys off these timestamps, so moving one on a retry "+
			"extends a window nobody asked to extend",
			first.UploadedAt, second.UploadedAt)
	}

	if _, err := store.MarkMediaUploaded(ctx, tid, strings.Repeat("2", 64)); !errors.Is(err, logbook.ErrNoMediaObject) {
		t.Errorf("committing an id nothing holds = %v, want ErrNoMediaObject", err)
	}
}

// AN ASSET IS REFERENCEABLE ONLY AFTER IT IS COMMITTED, AND THE POSITIVE HALF
// IS WHAT MAKES THIS LEG WORTH ANYTHING.
//
// v6's own note: a validator that rejects everything passes "an uncommitted
// asset is refused" perfectly, which is why both halves are in one leg. The
// mutation that matters is "make it refuse everything" — that reddens the
// POSITIVE half and nothing else does.
//
// IT LIVES HERE AND NOT IN internal/httpapi BECAUSE THE CHECK IS A QUERY, and
// the query has to run under the traveller's advisory lock beside the city and
// date checks — a check made out in a handler is a check made against a
// database that can move underneath it.
//
// AND DEC-58's "ENFORCED TWICE" IS PRECISE RATHER THAN LOOSE, which is worth
// writing down because the ruling's own phrasing is not. The four foreign keys
// guarantee the ROW EXISTS; they say nothing about `uploaded_at`, because an
// FK cannot see a column it does not reference. So the schema refuses a
// reference to an object nobody ever began, and THIS check is what refuses a
// reference to one that was begun and never uploaded. Two different lies,
// two different guards, and only one of them is the database's.
func TestATripCannotWearACoverWhoseBytesHaveNotLanded(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	store := LogbookStore{DB: db}

	digest := strings.Repeat("3", 64)
	begun(t, db, tid, digest, 10, "image/png")

	name := "Kyoto in May"
	cover := &digest
	write := logbook.TripWrite{
		ID:         ptr("kyoto-in-may"),
		Name:       &name,
		CoverAsset: &cover,
	}

	_, _, err := store.PutTrip(ctx, tid, write)
	var invalid logbook.InvalidFieldError
	if !errors.As(err, &invalid) || invalid.Field != "coverAsset" {
		t.Fatalf("referencing an UNCOMMITTED object = %v, want an InvalidFieldError "+
			"naming coverAsset", err)
	}

	if _, err := (MediaStore{DB: db}).MarkMediaUploaded(ctx, tid, digest); err != nil {
		t.Fatalf("MarkMediaUploaded: %v", err)
	}

	// THE POSITIVE HALF. Without it the assertion above is satisfied by a
	// store that refuses every cover.
	trip, _, err := store.PutTrip(ctx, tid, write)
	if err != nil {
		t.Fatalf("referencing a COMMITTED object = %v, want it to succeed", err)
	}
	if trip.CoverAsset == nil || *trip.CoverAsset != digest {
		t.Errorf("coverAsset = %v, want %s", trip.CoverAsset, digest)
	}

	// AND THE THIRD LIE, WHICH IS THE SCHEMA'S AND NOT THIS CHECK'S: an object
	// nobody ever began. The Go check answers it too, because a row that is
	// not there is not a committed row — but the FOREIGN KEY is what makes it
	// unbypassable by the next route somebody adds (DEC-58).
	neverBegun := strings.Repeat("4", 64)
	if _, err := db.ExecContext(ctx,
		`UPDATE trips SET cover_asset=$3 WHERE traveller_id=$1::uuid AND id=$2`,
		tid, "kyoto-in-may", neverBegun); err == nil {
		t.Error("the schema accepted a cover naming an object that was never begun — " +
			"trips_cover_fk is what stops a route added later from doing this")
	}
}

// MediaObjects ANSWERS ONLY THIS TRAVELLER'S ROWS, and an unknown traveller is
// ErrNoTraveller rather than an empty result.
//
// THE DIFFERENCE REACHES THE PHONE. An empty result read as "no such object"
// is a 404, and the honest answer for a traveller whose row has been deleted
// between the credential being accepted and the query running is that the
// credential is not live — sign in again.
func TestMediaObjectsIsScopedToTheTravellerAndReportsAnUnknownOne(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	store := MediaStore{DB: db}

	mine := strings.Repeat("5", 64)
	begun(t, db, tid, mine, 10, "image/png")

	// assetA and assetB are seeded for `tid`; otherT has no rows at all and no
	// traveller row either.
	rows, err := store.MediaObjects(ctx, tid, []string{mine, assetA, noSuch})
	if err != nil {
		t.Fatalf("MediaObjects: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("asked for three ids, one of which nothing holds, and got %d rows — "+
			"a miss is SILENTLY OMITTED because the caller decides what it means",
			len(rows))
	}

	if _, err := store.MediaObjects(ctx, otherT, []string{mine}); !errors.Is(err, logbook.ErrNoTraveller) {
		t.Errorf("MediaObjects for a traveller that is not there = %v, want ErrNoTraveller", err)
	}
}

// THE LEG CONTENT ADDRESSING EXISTS FOR: TWO PHOTOGRAPHS, ONE OBJECT, AND
// EITHER CAN BE DELETED.
//
// There is exactly ONE row in media_objects for bytes that appear twice, and
// what stops the object being destroyed by the first deletion is the four
// foreign keys — `ON DELETE RESTRICT` on the reference, and nothing at all in
// the other direction. The whole reference count IS the four keys, which is
// why OE-12 could delete `Remove` from the object store: the sweep's liveness
// query is a NOT EXISTS across those four columns and is not knowable until
// R7.
func TestTwoPhotographsShareOneObjectAndEitherCanGo(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	// The seeded fixture files p-may and p-autumn against assetA and assetB.
	// Point both at assetA, which is what two photographs of the same bytes
	// looks like — the client hashes the file, so identical bytes are
	// identical ids.
	if _, err := db.ExecContext(ctx,
		`UPDATE photos SET asset=$2 WHERE traveller_id=$1::uuid`, tid, assetA); err != nil {
		t.Fatalf("pointing both photographs at one object: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1::uuid AND asset=$2`,
		tid, assetA); n != 2 {
		t.Fatalf("%d photographs share the object, want 2", n)
	}
	if n := count(t, db, `SELECT count(*) FROM media_objects WHERE traveller_id=$1::uuid AND id=$2`,
		tid, assetA); n != 1 {
		t.Fatalf("%d rows for one digest, want 1 — the address IS the content", n)
	}

	if _, err := db.ExecContext(ctx,
		`DELETE FROM photos WHERE traveller_id=$1::uuid AND id='p-may'`, tid); err != nil {
		t.Fatalf("deleting the first photograph: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM media_objects WHERE traveller_id=$1::uuid AND id=$2`,
		tid, assetA); n != 1 {
		t.Errorf("the object went with the first photograph — the second one now "+
			"points at nothing, with no error anywhere (%d rows)", n)
	}
	if n := count(t, db, `SELECT count(*) FROM photos WHERE traveller_id=$1::uuid AND asset=$2`,
		tid, assetA); n != 1 {
		t.Errorf("%d photographs survive, want 1", n)
	}

	// AND THE OBJECT CANNOT BE REMOVED WHILE THE SURVIVOR POINTS AT IT.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM media_objects WHERE traveller_id=$1::uuid AND id=$2`, tid, assetA); err == nil {
		t.Error("an object a photograph still points at was deleted — photos_asset_fk " +
			"is RESTRICT, and the reference count IS the four foreign keys")
	}
}
