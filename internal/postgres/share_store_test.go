// H1's three writes against a real PostgreSQL, where the partial index, the
// column defaults and the digest all actually exist.
package postgres

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// stop then new link, which is the exact sequence that fails outright under
// the primary key nobody specified.
func TestStopThenNewLinkSucceedsAndTheRevocationRecordSurvives(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	if n := count(t, db, liveLinksSQL, tid); n != 1 {
		t.Fatalf("the fixture has %d live links, want 1", n)
	}

	if _, _, err := store.StopSharing(ctx, tid, "kyoto-in-may"); err != nil {
		t.Fatalf("StopSharing: %v", err)
	}
	trip, _, err := store.NewShareLink(ctx, tid, "kyoto-in-may", tokenTwo)
	if err != nil {
		t.Fatalf("NewShareLink after StopSharing: %v", err)
	}
	if trip.ShareLinkID == nil || *trip.ShareLinkID != tokenTwo {
		t.Errorf("the mint answered shareLinkId %v, want the token the client supplied — "+
			"it is ECHOED rather than recovered, and no later read can ever produce it",
			trip.ShareLinkID)
	}

	if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 2 {
		t.Errorf("share_links holds %d rows, want 2 — the revocation record survives, "+
			"which is what DEC-67 chose its primary key for", n)
	}
	if n := count(t, db, liveLinksSQL, tid); n != 1 {
		t.Errorf("%d links are live, want exactly 1 — share_links_one_live is the only "+
			"thing enforcing the cardinality the class diagram claims", n)
	}
}

// 'new link' ON A trip that is already shared, which is the sequence the
// revoke inside the mint exists for — and the one the obvious leg misses.
func TestNewLinkOnATripThatIsAlreadySharedKillsTheOldOne(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	if n := count(t, db, liveLinksSQL, tid); n != 1 {
		t.Fatalf("the fixture has %d live links, want 1 — this leg is about minting "+
			"OVER a live one", n)
	}

	trip, _, err := store.NewShareLink(ctx, tid, "kyoto-in-may", tokenTwo)
	if err != nil {
		t.Fatalf("NewShareLink over a live link: %v\n"+
			"    'New link' is offered whether or not one is live, and the mint has to\n"+
			"    revoke first: share_links_one_live is a PARTIAL unique index, so an\n"+
			"    insert without the revoke RAISES rather than quietly handing out two\n"+
			"    capabilities for one trip.", err)
	}
	if trip.ShareLinkID == nil || *trip.ShareLinkID != tokenTwo {
		t.Errorf("the mint answered shareLinkId %v, want %q", trip.ShareLinkID, tokenTwo)
	}

	if n := count(t, db, `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND trip_id='kyoto-in-may'`, tid); n != 2 {
		t.Errorf("share_links holds %d rows, want 2 — the old link is REVOKED and kept, "+
			"not deleted", n)
	}
	if n := count(t, db, liveLinksSQL, tid); n != 1 {
		t.Errorf("%d links are live, want exactly 1", n)
	}
	var hash []byte
	if err := db.QueryRowContext(ctx,
		`SELECT token_hash FROM share_links WHERE traveller_id=$1 AND trip_id='kyoto-in-may'
		   AND revoked_at IS NULL`, tid).Scan(&hash); err != nil {
		t.Fatalf("reading the live link back: %v", err)
	}
	if want := logbook.HashShareToken(tokenTwo); !bytes.Equal(hash, want) {
		t.Errorf("the live link is %x, want the newly minted %x — the OLD token is still "+
			"the one that works", hash, want)
	}
}

// A mint without the revoke raises rather than creating A second live
// link.
func TestASecondLiveLinkForOneTripIsRefusedByTheIndex(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx,
		`INSERT INTO share_links (traveller_id, trip_id, token_hash) VALUES ($1,'kyoto-in-may',$2)`,
		tid, logbook.HashShareToken(tokenTwo))
	if err == nil {
		t.Fatal("a second LIVE link was accepted for one trip")
	}
	if !strings.Contains(err.Error(), "share_links_one_live") {
		t.Errorf("refusal = %v, want it to name share_links_one_live", err)
	}
}

// the stop resets's three flags by name, and the coordinate one is the
// privacy half.
func TestStoppingSharingResetsTheThreeFlagsToTheClientsDefaults(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	on := true
	off := false
	if _, _, err := store.SetShareOptions(ctx, tid, "kyoto-in-may", logbook.ShareWrite{
		SharePhotos: &off, ShareNotes: &off, ShareCoordinates: &on,
	}); err != nil {
		t.Fatalf("SetShareOptions: %v", err)
	}
	if got := sharingOf(t, db, tid, "kyoto-in-may"); got != [3]bool{false, false, true} {
		t.Fatalf("the fixture is wrong: the flags read %v before the stop", got)
	}

	trip, _, err := store.StopSharing(ctx, tid, "kyoto-in-may")
	if err != nil {
		t.Fatalf("StopSharing: %v", err)
	}
	if got := sharingOf(t, db, tid, "kyoto-in-may"); got != [3]bool{true, true, false} {
		t.Errorf("the flags read %v after the stop, want [true true false].\n"+
			"    Removing the reset is a PRIVACY LEAK and not a tidiness issue: the\n"+
			"    next link hands out exact pins without anybody turning that on. And\n"+
			"    the two `true`s are the CLIENT's defaults, which the column defaults\n"+
			"    only coincidentally agree with since 0002.", got)
	}
	if trip.Shared {
		t.Errorf("the trip still reports shared:true after the stop — DEC-91's boolean " +
			"is `revoked_at IS NULL`, so H1's Stop sharing would appear to do nothing")
	}
	if trip.ShareLinkID != nil {
		t.Errorf("the stop answered a shareLinkId (%v). Nothing can produce a plaintext "+
			"after DEC-85 except the mint echoing what it was sent.", *trip.ShareLinkID)
	}
}

// the plaintext never reaches the table.
func TestAMintedTokenIsNowhereInTheClear(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	if _, _, err := (ShareStore{DB: db}).NewShareLink(ctx, tid, "autumn-crossing", tokenThree); err != nil {
		t.Fatalf("NewShareLink: %v", err)
	}

	var rows string
	if err := db.QueryRowContext(ctx,
		`SELECT coalesce(string_agg(share_links::text, ' '), '') FROM share_links WHERE traveller_id=$1`,
		tid).Scan(&rows); err != nil {
		t.Fatalf("reading share_links back: %v", err)
	}
	if strings.Contains(rows, tokenThree) {
		t.Errorf("the plaintext token is in the table:\n%s\n"+
			"    DEC-85 reverses DEC-10's plaintext half. Under DEC-67 the table\n"+
			"    revokes and KEEPS, so a dump would hold every capability ever issued.",
			rows)
	}

	var hash []byte
	if err := db.QueryRowContext(ctx,
		`SELECT token_hash FROM share_links WHERE traveller_id=$1 AND trip_id='autumn-crossing'`,
		tid).Scan(&hash); err != nil {
		t.Fatalf("reading token_hash: %v", err)
	}
	if want := logbook.HashShareToken(tokenThree); !bytes.Equal(hash, want) {
		t.Errorf("token_hash = %x, want %x — /l/{token} resolves by hashing what it was "+
			"given, so a different digest is a link that never works", hash, want)
	}
}

// against the real columns: a body naming one switch leaves the other two
// exactly as the row had them.
func TestAShareOptionsWriteTouchesOnlyTheColumnsItWasSent(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	on, off := true, false
	if _, _, err := store.SetShareOptions(ctx, tid, "kyoto-in-may",
		logbook.ShareWrite{ShareCoordinates: &on}); err != nil {
		t.Fatalf("the first write: %v", err)
	}
	if _, _, err := store.SetShareOptions(ctx, tid, "kyoto-in-may",
		logbook.ShareWrite{SharePhotos: &off}); err != nil {
		t.Fatalf("the second write: %v", err)
	}

	if got := sharingOf(t, db, tid, "kyoto-in-may"); got != [3]bool{false, true, true} {
		t.Errorf("the flags read %v, want [false true true].\n"+
			"    H1 flicks ONE switch at a time — every control on the screen goes\n"+
			"    inert while a write is in flight — so 'leave the other two alone' is\n"+
			"    every request the client makes, not a corner case.", got)
	}
}

// an empty body is legal and writes nothing.
func TestAShareOptionsWriteNamingNothingChangesNoColumn(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	on := true
	if _, _, err := store.SetShareOptions(ctx, tid, "kyoto-in-may",
		logbook.ShareWrite{ShareCoordinates: &on}); err != nil {
		t.Fatalf("the first write: %v", err)
	}
	before := sharingOf(t, db, tid, "kyoto-in-may")

	if _, _, err := store.SetShareOptions(ctx, tid, "kyoto-in-may", logbook.ShareWrite{}); err != nil {
		t.Fatalf("an empty share write answered %v", err)
	}
	if got := sharingOf(t, db, tid, "kyoto-in-may"); got != before {
		t.Errorf("an empty body moved the flags from %v to %v", before, got)
	}
}

// all three are setters and an unknown trip is logbook.ErrNoTrip, which the
// handler turns into a 404.
func TestTheShareWritesRefuseATripThatIsNotInTheLog(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()
	on := true

	cases := map[string]func() error{
		"SetShareOptions": func() error {
			_, _, err := store.SetShareOptions(ctx, tid, "never-existed", logbook.ShareWrite{SharePhotos: &on})
			return err
		},
		"NewShareLink": func() error {
			_, _, err := store.NewShareLink(ctx, tid, "never-existed", tokenTwo)
			return err
		},
		"StopSharing": func() error {
			_, _, err := store.StopSharing(ctx, tid, "never-existed")
			return err
		},
	}
	for name, call := range cases {
		if err := call(); !isNoTrip(err) {
			t.Errorf("%s on an unknown trip answered %v, want logbook.ErrNoTrip", name, err)
		}
	}
}

// A share write moves logbook_version, WHICH is the list rather than A
// judgement call.
func TestEveryShareWriteMovesTheVersion(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()
	on := true

	seen := map[string]int64{}
	var err error
	if _, seen["options"], err = store.SetShareOptions(ctx, tid, "autumn-crossing",
		logbook.ShareWrite{ShareCoordinates: &on}); err != nil {
		t.Fatalf("SetShareOptions: %v", err)
	}
	if _, seen["mint"], err = store.NewShareLink(ctx, tid, "autumn-crossing", tokenTwo); err != nil {
		t.Fatalf("NewShareLink: %v", err)
	}
	if _, seen["stop"], err = store.StopSharing(ctx, tid, "autumn-crossing"); err != nil {
		t.Fatalf("StopSharing: %v", err)
	}

	if !(seen["options"] < seen["mint"] && seen["mint"] < seen["stop"]) {
		t.Errorf("the three writes committed at versions %v, and each has to be higher "+
			"than the last — the phone's ETag is the only thing that tells it the log "+
			"moved", seen)
	}
}

const liveLinksSQL = `SELECT count(*) FROM share_links WHERE traveller_id=$1 AND revoked_at IS NULL`

func isNoTrip(err error) bool {
	return err != nil && strings.Contains(err.Error(), logbook.ErrNoTrip.Error())
}
