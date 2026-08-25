-- 0002 — the share defaults are the CLIENT'S, and the rows written before this
-- are corrected rather than left to be guessed at.
--
-- WHY THIS IS NOT AN EDIT TO 0001. `loadMigrations` checksums
-- `sha256.Sum256(body)` over the whole file, COMMENTS INCLUDED, so any edit to
-- 0001 — including this comment, had it gone there — raises
-- ErrChecksumMismatch against every database that has already applied it, and
-- the process refuses to boot. Two SQL lines in an existing file look tidier
-- and are the one change this step must not make.
--
-- THE DEFAULTS. The client ships `sharePhotos: true, shareNotes: true,
-- shareCoordinates: false`, and 0001 defaulted all three to false. So a trip
-- created on the server was born with photo and note sharing off — a setting
-- nobody chose, and different from the same trip created on the phone.
--
-- share_coordinates IS DELIBERATELY NOT TOUCHED, and the reason is the
-- client's own sentence: a pin on your accommodation is not something to hand
-- out by link, so it has to be actively turned on. false is correct on both
-- sides, and an ALTER here would be a change that agrees with nothing.

ALTER TABLE trips ALTER COLUMN share_photos SET DEFAULT true;

ALTER TABLE trips ALTER COLUMN share_notes SET DEFAULT true;

-- AND THE BACKFILL (DEC-82), WHICH THE TWO ALTERs ABOVE DO NOT DO.
--
-- EXECUTED by the database lens: after the ALTERs alone, pre-existing rows
-- stay f|f|f while every new row reads t|t|f, and NOTHING IN THE TABLE CAN
-- DISTINGUISH 'written before 0002' from 'the user turned sharing off'. A
-- DEFAULT applies to rows written after it and never to rows already there.
--
-- Those rows carry a default the CLIENT NEVER HAD, so they are wrong data
-- rather than a choice, and correcting them is the only reading that does not
-- silently invent an intent for somebody. It is unscoped on purpose: it is
-- every trip in the table, because every trip in the table predates this file.
--
-- IT IS SMALL BY CONSTRUCTION AND WILL NOT STAY THAT WAY. Today the only rows
-- here are the ones the slice arc wrote. R4's seed refuses a non-empty
-- database and writes the values from the client document itself, so it is
-- unaffected either way.
UPDATE trips SET share_photos = true, share_notes = true;
