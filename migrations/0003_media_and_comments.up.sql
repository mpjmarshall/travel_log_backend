-- 0003 — the content-type allowlist, five bounds 0001 could not write, and the
-- two catalog comments DEC-83 asked for.
--
-- IT IS ENTIRELY TRANSACTIONAL AND CARRIES NO `-- migrate:no-transaction`
-- DIRECTIVE. That is worth saying here rather than leaving to inference,
-- because DEC-99's twice-run guard lands in the same step and this file is not
-- its subject: every statement below is an ALTER or a COMMENT, all of which
-- take an ACCESS EXCLUSIVE lock briefly and none of which PostgreSQL refuses
-- inside a transaction block. The guard's subject is
-- internal/postgres/testdata/notx_fixture.up.sql, and the honesty of that
-- arrangement is written out in the fixture's own header.
--
-- WHY THIS IS NOT AN EDIT TO 0001, restated because it is the reason 0002
-- exists too: `loadMigrations` checksums sha256 over the whole file, COMMENTS
-- INCLUDED, so any edit to 0001 — including a `--` comment — raises
-- ErrChecksumMismatch against every database that has already applied it and
-- the process refuses to boot. That is executed rather than reasoned: PD-13
-- ran it, on 127.0.0.1:5434.

-- ------------------------------------------------------------------ THE ALLOWLIST
--
-- 0001's own comment calls media_objects_content_type_present_ck "THE WEAKEST
-- CHECK IN THIS FILE" and says why: DEC-51's allowlist was named nowhere, so
-- the constraint that belonged there could not be written. It stops '' and
-- nothing else, and `content_type = 'text/html; <script>'` was accepted.
--
-- WHAT THE PAYLOAD ACTUALLY DOES. An object stored as text/html is served AS
-- HTML from the bucket origin, at a URL the public share envelope embeds —
-- stored XSS on the storage origin, reachable by anyone holding a share link.
-- The defence is DEC-51's, stated in two halves: this allowlist, and
-- `response-content-disposition: attachment` on every presigned GET
-- (internal/media/minio.go). The residual is named rather than hidden: a
-- MISLABELLED object is downloaded, never rendered.
--
-- THE LIST IS `^image/(jpeg|png)$` AND heic IS NOT IN IT (DEC-104). Nothing in
-- this system can produce a HEIC: the client's shutter is inert by decision,
-- DEC-41 seeds two PNGs, and the fixture's 284 photographs resolve to two
-- `image/png` objects. `image/jpeg` earns its place independently of any
-- camera — internal/postgres/schema_test.go seeds shared fixtures with it, so
-- removing it breaks every leg that uses that helper. THAT ASYMMETRY IS THE
-- WHOLE OF THE ANSWER: one of the three is reachable from the test suite today
-- and one is reachable from nothing at all. And the general form is worth more
-- than the character: an allowlist entry nothing can produce or test is not a
-- free option — it is a claim the schema makes that no leg can check. `heic`
-- comes back with the real capture, which is the same dependency conversation
-- the shutter already needs, and adding a media type is additive and does not
-- move logbookFormatVersion.
--
-- IT IS AN IN-LIST AND NOT A REGEX, and that is not a style choice: an
-- enumerated list is what a reader can check against internal/logbook's
-- `contentTypePattern` by eye, and there is no third spelling of the same set
-- for the two to drift apart in.
--
-- THE RENAME IS A CATALOG-LEG CHANGE AND IT IS DELIBERATE. 0001's own header
-- warns that a rename moves a name the catalog legs assert;
-- internal/postgres/schema_test.go asserted the old one. The name changes
-- because the claim changes: `_present_ck` was true of a check that stopped ''
-- and is a lie about a check that enumerates two values.
ALTER TABLE media_objects
	DROP CONSTRAINT media_objects_content_type_present_ck;

ALTER TABLE media_objects
	ADD CONSTRAINT media_objects_content_type_ck
	CHECK (content_type IN ('image/jpeg', 'image/png'));

-- ---------------------------------------------------------- THE COMMIT ORDERING
--
-- A row could be committed ten years before it was created, and that inserted
-- successfully before this constraint existed. It is not cosmetic: the media
-- sweep's grace window keys off exactly these two timestamps — an object
-- uploaded but not yet referenced is live for as long as `created_at` says it
-- is young — so a row whose uploaded_at precedes its created_at is a row the
-- sweep reasons about wrongly, in the direction that deletes somebody's
-- photograph.
--
-- uploaded_at IS NULL is the ordinary case and must stay legal: it is exactly
-- what `alreadyExists: false` is derived from.
ALTER TABLE media_objects
	ADD CONSTRAINT media_objects_uploaded_after_created_ck
	CHECK (uploaded_at IS NULL OR uploaded_at >= created_at);

-- --------------------------------------------------------- FOUR EMPTY STRINGS
--
-- `travellers.name` and `walks.name` already carry `IS NULL OR <> ''`, and
-- these four nullable text columns did not — so the same field is bounded in
-- one place and not in another for no reason anybody chose.
--
-- THE DISTINCTION THE CONSTRAINT DEFENDS IS ONE THE CLIENT CAN SEE. An absent
-- caption and an empty one are different states in the app: M2 draws "Write a
-- note" for a photograph with no caption and draws the caption otherwise, so
-- `caption = ''` is a photograph whose note row is present and blank. Same for
-- a visit's note, a trip's summary and a place's plan. R7 enforces the same
-- rule in Go, for the 422 that names the field; this is the guarantee, on
-- DEC-58's precedent — keep both, because the Go check exists to produce a
-- message and not to be the guard.
ALTER TABLE photos
	ADD CONSTRAINT photos_caption_present_ck
	CHECK (caption IS NULL OR caption <> '');

ALTER TABLE visits
	ADD CONSTRAINT visits_note_present_ck
	CHECK (note IS NULL OR note <> '');

ALTER TABLE trips
	ADD CONSTRAINT trips_summary_present_ck
	CHECK (summary IS NULL OR summary <> '');

ALTER TABLE places
	ADD CONSTRAINT places_plan_present_ck
	CHECK (plan IS NULL OR plan <> '');

-- ------------------------------------------------------ A WALK HAS A TRACK
--
-- THE LOWER BOUND DEC-89's POINTER CONTRACT MAKES NECESSARY (SAF-MAJ-6,
-- PD-21). `points: []` became expressible the moment absence stopped meaning
-- empty, and `walks_points_array_ck` does not stop it — an empty array IS an
-- array. A GPS track is a recording of a day that has passed and cannot be
-- retyped, so a write that empties one destroys the only copy of it.
--
-- IT EXPRESSES SOMETHING THE MODEL ALREADY HOLDS: both fixture walks carry
-- points, checked rather than assumed.
--
-- IT IS SATISFIED BY A NON-ARRAY ON PURPOSE, and that is the whole shape of
-- the predicate. `jsonb_array_length('"not an array"')` RAISES rather than
-- answering, so a constraint written as a bare `jsonb_array_length(points) > 0`
-- would error on the value `walks_points_array_ck` exists to refuse — and
-- PostgreSQL does not promise which of two failing CHECKs it names, so the
-- sibling leg would start failing by the wrong constraint. One constraint, one
-- claim: this one says nothing at all about a non-array and refuses only an
-- empty one.
ALTER TABLE walks
	ADD CONSTRAINT walks_points_present_ck
	CHECK (jsonb_typeof(points) <> 'array' OR jsonb_array_length(points) > 0);

-- ---------------------------------------------- DEC-83, IN THE CATALOG (PD-13)
--
-- The ruling asked for a comment on these two columns in 0001. It cannot go
-- there: the checksum makes any edit to 0001 a refusal to boot. Here it is
-- forward-only and needs no edit — and a catalog comment is BETTER than a `--`
-- comment for a reason that has nothing to do with the checksum: `\d+` shows
-- it, and any tool a later DBA runs shows it, where a comment in a file nobody
-- re-reads shows nothing.
COMMENT ON COLUMN photos.place_id IS
	'DEC-83. The (place_id, visit_id) pair is coherent by a GO rule and not by the schema: a photograph can name place P while naming a visit belonging to a different place, and the schema accepts it. This is the SECOND integrity rule deliberately left in Go — DEC-02''s cross-kind id uniqueness is the first — so the omission is not an oversight. The schema-level fix is a composite FK to visits (traveller_id, place_id, id), declined because it CHANGES what a visit deletion does to place_id, and delete behaviour is decided by what the app''s sheets promise. The cheap alternative was EXECUTED and is unusable: CHECK ((place_id IS NULL) = (visit_id IS NULL)) — the exact shape of photos_coordinates_paired_ck three columns away — ABORTS D2''s keep branch, because the two SET NULL rules fire as two separate single-column UPDATEs and the intermediate row is always incoherent. Measured across the 284 fixture photographs: 95 have both columns set, 189 have neither, place-only 0, visit-only 0. TRIGGER for revisiting: a sheet that says what happens to a photograph''s pin when its occasion is deleted.';

COMMENT ON COLUMN photos.visit_id IS
	'DEC-83. See the comment on photos.place_id: the pair is coherent by a Go rule, the composite FK was declined because it changes delete behaviour, and the paired CHECK was executed and aborts D2''s keep branch.';
