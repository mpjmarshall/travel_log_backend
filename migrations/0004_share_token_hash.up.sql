-- 0004 — the share token stops being stored in the clear (DEC-85), and
-- DEC-67's own premise is re-derived beside it because this file is what makes
-- that premise false.
--
-- WHY THIS IS NOT AN EDIT TO 0001, for the third time and for the same reason
-- 0002 and 0003 say it: `loadMigrations` checksums sha256 over the whole file,
-- COMMENTS INCLUDED, so any edit to 0001 raises ErrChecksumMismatch against
-- every database that has already applied it and the process refuses to boot.
--
-- WHAT DEC-10 TRADED AND WHY IT IS REVERSED. DEC-10 stored the plaintext so
-- `Trip.shareLinkId` could round-trip, and the trade was recorded and never
-- re-examined — while `sessions.token_hash` carries a whole argued section
-- explaining why a session token is hashed. Under DEC-67 this table REVOKES
-- AND KEEPS, so the plaintext column made a dump hold EVERY capability ever
-- issued, live and revoked, in the clear. Hashing costs that design nothing:
-- the row stays, the revocation stays, the history stays countable.
--
-- THE COST IS REAL AND IS LARGER THAN DEC-85's OWN ESTIMATE. The ruling says
-- "a RESTORED device shows a live link as not-shared". Measured by the
-- client-fidelity lens: DEC-32's write response is a whole Trip the phone
-- SPLICES into its cached log, so with the plaintext gone an ordinary RENAME
-- overwrote the local token with null and both 'Copy link' and 'Stop sharing'
-- went inert while the row was un-revoked and `GET /l/{token}` still served
-- it. DEC-91's `shared` boolean is the answer and it shipped in R1, derived
-- from `revoked_at IS NULL` on this table. So the trip's sharing STATE
-- survives this migration; only the capability itself does not, and the client
-- is the side that minted it and holds it.
--
-- ------------------------------------------------------------------------
-- AND DEC-67's HISTORY ARGUMENT, RE-DERIVED RATHER THAN CARRIED (SAF-MIN-9).
--
-- DEC-67 chose `PRIMARY KEY (traveller_id, token)` over the natural
-- `(traveller_id, trip_id)` for two reasons. The first stands untouched: with
-- the natural key, H1's 'Stop sharing' followed by 'New link' FAILS OUTRIGHT —
-- executed, `duplicate key value violates unique constraint`. The second was
-- "history is kept, which matters because DEC-10 stores the token in
-- plaintext: the record now shows which token was live when", and THIS FILE
-- MAKES THAT SENTENCE FALSE. The record no longer shows which token was live;
-- it shows which DIGEST was.
--
-- The key stays, and the re-derived reason is the first one plus a smaller
-- second: a revocation record with a digest still answers "how many links has
-- this trip had, and when did each stop working", which is every question
-- anybody asks of it, and it answers "is this token the one that was revoked"
-- for a holder who presents it. What it no longer does is let somebody READ a
-- past capability out of a backup, which is the point.
--
-- AND THE ONE THING THAT HISTORY DOES NOT SURVIVE, ACCEPTED IN WRITING RATHER
-- THAN BY SILENCE (SAF-MIN-9). `share_links_trip_fk` is ON DELETE CASCADE, so
-- D3's trip delete destroys the whole revocation history for that trip,
-- revoked rows included. Executed by the safety lens on the real fixture:
-- three rows for `autumn-crossing` — two revoked, one live — and
-- `DELETE FROM trips … id='autumn-crossing'` leaves `share_link_rows_left = 0`,
-- with `Trigger for constraint share_links_trip_fk on trips: calls=1` in the
-- EXPLAIN ANALYZE trigger list. IT IS ACCEPTED: the trip is gone, so "which
-- token was live on a trip that no longer exists" is not a question anyone
-- asks, and D3's sheet could not reasonably itemise a server-side artefact the
-- client's model never held — a Trip carries one `shareLinkId`. The
-- alternative is a nullable `trip_id` and a migration, to keep rows nobody
-- will read. This comment is here rather than in 0001 because 0001 cannot be
-- edited, and the point of putting it here is that the next reader finds
-- DEC-67's argument and its correction TOGETHER rather than in contradiction.
-- ------------------------------------------------------------------------
--
-- IT IS ENTIRELY TRANSACTIONAL and carries no `-- migrate:no-transaction`
-- directive: every statement below is an ALTER, an UPDATE, a CREATE INDEX or a
-- DROP INDEX, none of which PostgreSQL refuses inside a transaction block.
-- That matters more here than in 0003, because a half-applied 0004 is a table
-- with neither a usable token column nor a usable hash column.
--
-- `sha256()` IS CORE POSTGRESQL AND NEEDS NO EXTENSION. It has been in the
-- core distribution since 11 and this schema's floor is 15 (DEC-66), so the
-- backfill does not drag pgcrypto in — which would be a dependency decision
-- rather than a migration.
--
-- `convert_to(token, 'UTF8')` IS WHAT MAKES THE BACKFILL AGREE WITH GO.
-- `logbook.HashShareToken` is `sha256.Sum256([]byte(token))` — the token's own
-- UTF-8 bytes, with no decode step — and `token::bytea` is not a legal cast
-- from text at all. `convert_to` is the one spelling that produces the same
-- bytes Go hashes, and a leg computes the expected digest by CALLING that Go
-- function rather than restating a hex literal.

ALTER TABLE share_links ADD COLUMN token_hash bytea;

UPDATE share_links SET token_hash = sha256(convert_to(token, 'UTF8'));

ALTER TABLE share_links ALTER COLUMN token_hash SET NOT NULL;

-- The same CHECK `sessions` carries, for the same reason its own comment
-- gives: a one-byte token_hash inserted successfully before the constraint
-- existed, and nothing downstream can tell a truncated digest from a whole
-- one.
ALTER TABLE share_links
	ADD CONSTRAINT share_links_token_hash_sha256_ck
	CHECK (octet_length(token_hash) = 32);

-- THE PRIMARY KEY MOVES ONTO THE DIGEST, which is DEC-67's key with the
-- plaintext substituted and not a change of design. Dropping the constraint
-- drops its index with it; the new one is created by the ADD below.
--
-- `share_links_traveller_fk` NEEDS THIS INDEX AND NOT share_links_trip_idx:
-- its child column set is (traveller_id), which the new primary key leads
-- with. TestEveryForeignKeyChildColumnSetLeadsSomeIndex is derived from
-- pg_index rather than from a list, so it checks this rather than trusting it.
ALTER TABLE share_links DROP CONSTRAINT share_links_pkey;

ALTER TABLE share_links ADD CONSTRAINT share_links_pkey PRIMARY KEY (traveller_id, token_hash);

-- `share_links_token_key` KEEPS ITS NAME, and that is the opposite call from
-- 0003's rename of `media_objects_content_type_present_ck`. The rule is the
-- same one: a name changes when the CLAIM changes. `_present_ck` was a lie
-- about a check that had started enumerating two values; this index still
-- answers exactly the question it always answered — which trip does
-- `GET /l/{token}` resolve to, across every traveller — and only its input
-- representation has moved. Renaming it would move a string the catalog legs
-- and the arc both assert, for no change in meaning.
DROP INDEX share_links_token_key;

CREATE UNIQUE INDEX share_links_token_key ON share_links (token_hash);

-- LAST, AND IT TAKES `share_links_token_present_ck` WITH IT. A dropped column
-- takes its own CHECK constraints, so there is no second DROP CONSTRAINT here
-- and nothing is left behind naming a column that is gone.
ALTER TABLE share_links DROP COLUMN token;
