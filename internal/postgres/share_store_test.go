// H1's three writes against a real PostgreSQL, where the partial index, the
// column defaults and the digest all actually exist.
//
// WHAT ONLY THIS CAN SAY. The handler legs in internal/httpapi run against a
// fake and are about what leaves the process — a 201, a whole Trip, a 404 on a
// setter. `share_links_one_live` is a PARTIAL UNIQUE INDEX and is the only
// thing enforcing the "0..1 live" the class diagram claims; the reset's three
// values are the CLIENT's and only coincidentally the column defaults; and
// whether the plaintext reaches the table is a question about bytes in a
// column. None of the three is visible to a twin.
package postgres

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"travellog/internal/logbook"
)

// STOP THEN NEW LINK, WHICH IS THE EXACT SEQUENCE THAT FAILS OUTRIGHT UNDER
// THE PRIMARY KEY NOBODY SPECIFIED (DEC-67).
//
// With `PRIMARY KEY (traveller_id, trip_id)` — the natural reading of "1 to
// 0..1" — this is `duplicate key value violates unique constraint`. DEC-67
// moved the key to (traveller_id, token) and 0004 moved it again to
// (traveller_id, token_hash), and what makes 'New link' work is that the table
// REVOKES AND KEEPS.
//
// THE REVOCATION RECORD SURVIVING IS ASSERTED, and its meaning has changed:
// history now shows which DIGEST was live when, not which token. That is
// DEC-67's argument re-derived rather than carried — see 0004's own comment.
func TestStopThenNewLinkSucceedsAndTheRevocationRecordSurvives(t *testing.T) {
	db := seeded(t)
	store := ShareStore{DB: db}
	ctx := context.Background()

	// seeded() already leaves one live link on kyoto-in-may.
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

// 'NEW LINK' ON A TRIP THAT IS ALREADY SHARED, WHICH IS THE SEQUENCE THE
// REVOKE INSIDE THE MINT EXISTS FOR — AND THE ONE THE OBVIOUS LEG MISSES.
//
// H1 offers 'New link' whether or not a link is live: "Mints a link, killing
// whatever one was live". Stop-then-new goes through a StopSharing that has
// already revoked, so a mint that forgot its own revoke passes that sequence
// perfectly. MEASURED at this working tree: deleting the revoke from
// NewShareLink leaves every other leg in this file GREEN. This is the one that
// reddens, and it reddens on the INSERT raising rather than on two live links
// existing — which is why it asserts the 201 and the row counts, exactly as
// the plan says.
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
	// AND THE ONE THAT IS LIVE IS THE NEW ONE. A revoke that killed the wrong
	// row would leave the count at one and the wrong capability working.
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

// AND A MINT WITHOUT THE REVOKE RAISES RATHER THAN CREATING A SECOND LIVE
// LINK, which is why the leg above asserts the ROW COUNTS and not the absence
// of an error.
//
// This is the plan's own named mutation, run as a leg instead: skipping the
// revoke does not quietly hand out two capabilities for one trip — the partial
// index refuses the INSERT. So a build with that defect FAILS LOUDLY, and a
// leg written as "NewShareLink returns no error" would have been proving the
// index rather than the code.
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

// THE STOP RESETS THE THREE FLAGS BY NAME, AND THE COORDINATE ONE IS THE
// PRIVACY HALF.
//
// Turn coordinates on, stop sharing, and the NEXT link must not carry them: a
// killed link that leaves a coordinate switch armed hands out exact pins on
// the next link without anybody having turned that on.
//
// THE OTHER TWO GO THE OTHER WAY AND THE ASYMMETRY IS THE CLIENT'S. It writes
// `Trip.defaultSharePhotos` and `defaultShareNotes`, both true, and
// `defaultShareCoordinates`, false — because a pin on your accommodation is
// not something to hand out by link, so it has to be actively turned on.
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

// THE PLAINTEXT NEVER REACHES THE TABLE (DEC-85).
//
// IT IS ASSERTED OVER THE WHOLE ROW AND NOT OVER `token_hash` ALONE, because
// the mistake this catches is not a wrong hash — it is a column somebody adds
// beside it. `share_links::text` renders every column of the row, so a
// `token`, a `label` or a `last_seen_token` carrying the plaintext is caught
// by the same query.
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

// DEC-89 AGAINST THE REAL COLUMNS: a body naming one switch leaves the other
// two exactly as the row had them.
//
// The fake in internal/httpapi asserts the same thing about the handler; this
// asserts it about the STATEMENT, which is where the CASE WHEN lives and where
// an EXCLUDED-form rewrite would silently reset the group.
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

// AN EMPTY BODY IS LEGAL AND WRITES NOTHING. DEC-89's contract is that absence
// is not an error, and refusing it would refuse exactly the retry a client
// makes after a lost response.
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

// ALL THREE ARE SETTERS AND AN UNKNOWN TRIP IS logbook.ErrNoTrip, which the
// handler turns into a 404. Without the existence check every one of them is
// an UPDATE matching nothing, which reports SUCCESS — and the re-read then
// fails, so the caller gets a 404 anyway but by way of an error that says
// nothing.
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

// AND A SHARE WRITE MOVES logbook_version, WHICH IS DEC-50's LIST RATHER THAN
// A JUDGEMENT CALL.
//
// `share_links` is on the bumping side because DEC-91's `shared` is derived
// from it on every trip read. A mint or a revoke that moved no version would
// leave every phone answering 304 to a log whose `shared` has changed, and
// 'Stop sharing' would appear to do nothing until some unrelated write
// happened.
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
