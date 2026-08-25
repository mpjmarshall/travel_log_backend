-- THIS FILE IS NOT AN INVERSE OF 0002, AND THAT IS THE POINT OF WRITING IT
-- DOWN HERE (DEC-82).
--
-- It reverts the two DEFAULTs and DELIBERATELY LEAVES THE BACKFILLED ROWS
-- ALONE. The obvious inverse — `UPDATE trips SET share_photos = false,
-- share_notes = false` — would be wrong in the other direction: it cannot tell
-- a row 0002 corrected from a row a user has since chosen to share, so it
-- would turn sharing off on trips somebody deliberately turned it on for. One
-- of those two errors is recoverable by the user and the other silently
-- withdraws a link they are relying on.
--
-- So a down of 0002 restores the SCHEMA and not the DATA. Say it out loud
-- because "down" implies otherwise, and the runner never runs these
-- automatically — they are checked in so a migration can be reversed BY HAND,
-- by somebody who has read this.

ALTER TABLE trips ALTER COLUMN share_photos SET DEFAULT false;

ALTER TABLE trips ALTER COLUMN share_notes SET DEFAULT false;
