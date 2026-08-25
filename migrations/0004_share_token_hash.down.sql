-- THIS FILE REFUSES TO RUN WHILE share_links HOLDS A ROW, AND THAT REFUSAL IS
-- THE MIGRATION'S HONEST INVERSE.
--
-- DOWN FILES ARE CHECKED IN AND ARE NEVER RUN AUTOMATICALLY. The runner
-- applies .up.sql only, and refuses an .up.sql with no .down.sql beside it — a
-- migration nobody can reverse by hand is one that has to be reversed by
-- restore. So this is for a human, and what a human needs from it is the
-- decision stated rather than a statement that silently makes one for them.
--
-- 0002's down said out loud that it is not an inverse; 0003's said it is a
-- genuine one. THIS ONE IS NEITHER, AND THE REASON IS ARITHMETIC RATHER THAN
-- DESIGN: sha256 is one-way, so no statement in any language can put back what
-- 0004 replaced. That leaves exactly three things a down could do, and two of
-- them are worse than refusing:
--
--   1. RESTORE `token` AS NULLABLE and leave every value NULL. The pre-0004
--      primary key is (traveller_id, token) and a PRIMARY KEY column cannot be
--      NULL, so the table would come back in a shape 0001 never described —
--      a "reversal" that leaves the schema neither where it was nor where it
--      is. A build reading `token` would then serve NULL as a capability.
--   2. DELETE EVERY ROW and restore the schema whole. It is tempting, and it
--      is defensible on the grounds that a link whose plaintext is gone is
--      already dead — but it also destroys the revocation history DEC-67's
--      primary key exists to keep, silently, inside a file called "down".
--   3. REFUSE, and say what the operator has to decide. That is this file.
--
-- SO: on an EMPTY share_links this is a genuine, complete inverse and runs
-- without complaint — which is the case a developer rolling back a migration
-- they have just applied is actually in. On a table with rows it stops, names
-- the count, and leaves the two real choices to somebody who can weigh them.
--
-- The block is plain PL/pgSQL in a DO statement, which needs no extension.

DO $$
DECLARE
	held bigint;
BEGIN
	SELECT count(*) INTO held FROM share_links;
	IF held > 0 THEN
		-- THE WHOLE REASON IS IN THE MESSAGE AND NOT IN A HINT, AND THAT IS A
		-- MEASUREMENT. The first draft put it in `USING HINT`, which psql
		-- prints and `database/sql` DOES NOT: the driver's error string
		-- carries the MESSAGE alone, so a leg asserting the reason found only
		-- the count. A refusal whose reason is invisible to half its readers
		-- is a refusal somebody deletes.
		RAISE EXCEPTION 'migration 0004 cannot be reversed while share_links holds % row(s): '
			'sha256 is one-way, so the plaintext tokens 0004 replaced cannot be restored by '
			'any statement. Decide, and do it explicitly: either DELETE FROM share_links '
			'(which destroys the revocation history DEC-67 keeps — though every link is '
			'already dead, because nothing holds its plaintext), or stay on 0004. Restoring '
			'`token` as a nullable column is not an option: it is half of the pre-0004 '
			'primary key.', held;
	END IF;
END $$;

-- Below here share_links is empty, so every statement is exact.

ALTER TABLE share_links ADD COLUMN token text;

ALTER TABLE share_links
	ADD CONSTRAINT share_links_token_present_ck CHECK (token <> '');

ALTER TABLE share_links ALTER COLUMN token SET NOT NULL;

ALTER TABLE share_links DROP CONSTRAINT share_links_pkey;

ALTER TABLE share_links ADD CONSTRAINT share_links_pkey PRIMARY KEY (traveller_id, token);

DROP INDEX share_links_token_key;

CREATE UNIQUE INDEX share_links_token_key ON share_links (token);

ALTER TABLE share_links DROP CONSTRAINT share_links_token_hash_sha256_ck;

ALTER TABLE share_links DROP COLUMN token_hash;
