-- The reverse of 0003_media_and_comments.up.sql.
--
-- DOWN FILES ARE CHECKED IN AND ARE NEVER RUN AUTOMATICALLY. The runner
-- applies .up.sql only, and refuses an .up.sql with no .down.sql beside it — a
-- migration nobody can reverse by hand is one that has to be reversed by
-- restore.
--
-- THIS ONE IS A GENUINE INVERSE, WHICH 0002'S IS NOT, and the difference is
-- worth naming because "down" implies inverse and 0002 had to say out loud
-- that it is not one. Nothing here writes data: 0003 adds constraints and
-- comments, so dropping them restores the schema exactly and no row is
-- touched. The one thing a down cannot restore is a row that was REFUSED while
-- the constraint stood, and that is the point of the constraint.
--
-- AND ONE THING IT DOES NOT DO. It does not restore
-- `media_objects_content_type_present_ck`'s ORIGINAL comment in 0001 — the
-- constraint comes back with the same name and the same predicate, and 0001's
-- text is untouched throughout, because the checksum makes it untouchable.

COMMENT ON COLUMN photos.visit_id IS NULL;

COMMENT ON COLUMN photos.place_id IS NULL;

ALTER TABLE walks DROP CONSTRAINT IF EXISTS walks_points_present_ck;

ALTER TABLE places DROP CONSTRAINT IF EXISTS places_plan_present_ck;

ALTER TABLE trips DROP CONSTRAINT IF EXISTS trips_summary_present_ck;

ALTER TABLE visits DROP CONSTRAINT IF EXISTS visits_note_present_ck;

ALTER TABLE photos DROP CONSTRAINT IF EXISTS photos_caption_present_ck;

ALTER TABLE media_objects DROP CONSTRAINT IF EXISTS media_objects_uploaded_after_created_ck;

ALTER TABLE media_objects DROP CONSTRAINT IF EXISTS media_objects_content_type_ck;

ALTER TABLE media_objects
	ADD CONSTRAINT media_objects_content_type_present_ck
	CHECK (content_type <> '');
