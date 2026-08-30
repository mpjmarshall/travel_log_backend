-- 0007 — the passphrase goes, because sign-in is a mailed code now.
--
-- Not an edit to an applied migration, for the reason 0002 to 0006 give.
--
-- WHAT THIS RETIRES BESIDES A COLUMN. Argon2id, its concurrency gate, the
-- ARGON2_MAX_CONCURRENT ceiling, and the check-then-insert race on first
-- registration: that race existed inside a Register that read whether any
-- traveller existed, and no such read remains.
--
-- IT IS NOT REVERSIBLE IN THE WAY MOST OF THESE ARE. The down migration adds
-- the column back empty, and nobody's passphrase comes with it: the hashes are
-- gone and cannot be recomputed. That is the point of dropping them rather
-- than leaving them unused.
--
-- Entirely transactional: one ALTER.

ALTER TABLE travellers DROP COLUMN passphrase_hash;
