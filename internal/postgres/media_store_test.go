// media_objects against a real PostgreSQL.
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

// A client re-beginning an already-committed digest must not be able to
// restate what those bytes are.
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

// The separate select is not A style choice, and this is the measurement that
// says so.
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

// A commit is idempotent at the store, which is what makes the retry contract
// possible at the route.
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

// an asset is referenceable only after it is committed, and the positive half
// is what makes this leg worth anything.
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

	trip, _, err := store.PutTrip(ctx, tid, write)
	if err != nil {
		t.Fatalf("referencing a COMMITTED object = %v, want it to succeed", err)
	}
	if trip.CoverAsset == nil || *trip.CoverAsset != digest {
		t.Errorf("coverAsset = %v, want %s", trip.CoverAsset, digest)
	}

	neverBegun := strings.Repeat("4", 64)
	if _, err := db.ExecContext(ctx,
		`UPDATE trips SET cover_asset=$3 WHERE traveller_id=$1::uuid AND id=$2`,
		tid, "kyoto-in-may", neverBegun); err == nil {
		t.Error("the schema accepted a cover naming an object that was never begun — " +
			"trips_cover_fk is what stops a route added later from doing this")
	}
}

// MediaObjects answers only this traveller's rows, and an unknown traveller
// is ErrNoTraveller rather than an empty result.
func TestMediaObjectsIsScopedToTheTravellerAndReportsAnUnknownOne(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()
	store := MediaStore{DB: db}

	mine := strings.Repeat("5", 64)
	begun(t, db, tid, mine, 10, "image/png")

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

// The leg content addressing exists for: two photographs, one object, and
// either can be deleted.
func TestTwoPhotographsShareOneObjectAndEitherCanGo(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

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

	if _, err := db.ExecContext(ctx,
		`DELETE FROM media_objects WHERE traveller_id=$1::uuid AND id=$2`, tid, assetA); err == nil {
		t.Error("an object a photograph still points at was deleted — photos_asset_fk " +
			"is RESTRICT, and the reference count IS the four foreign keys")
	}
}
