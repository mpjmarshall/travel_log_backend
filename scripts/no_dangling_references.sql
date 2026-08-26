-- The standing dangling-reference check, as a file an acceptance check can run.
--
-- IT IS NEW AT R7 AND IT IS A GAP RATHER THAN AN ADDITION. Three of this
-- plan's acceptance lines have named `scripts/no_dangling_references.sql`
-- since v7.0 and no step's file list ever created it, so R5's and R6's
-- acceptance checks were quoting a path that did not exist. The Go half has
-- been real the whole time — `internal/seed/cascade_test.go` runs the same
-- query after D3's cascade and `internal/seed/photo_writes_test.go` runs it
-- after every R7 route — and this is that query, once, where psql can reach it.
--
-- ------------------------------------------------------------------------
-- A DANGLING-REFERENCE CHECK IS NOT A FILING CHECK, AND ALL ZEROES HERE PROVE
-- LESS THAN THEY LOOK (DEC-89, SAF-MAJ-5).
--
-- Every cascade in this schema that can SET NULL leaves nothing dangling
-- BECAUSE it set the reference to NULL. Measured: obey `visits: []` at
-- `fushimi-inari` and 28 occasions and 30 filings go, whole-log 95 photographs
-- are unfiled and 5 visit notes destroyed — and this file answers all zeroes
-- afterwards. So does R6's place-without-occasion query, and so does the
-- pair-agreement assertion, because both columns are NULL and therefore agree.
--
-- WHAT SEES IT IS A COUNT THAT MUST NOT FALL, and it is the last query in this
-- file rather than a separate one, so that nobody runs the zeroes without it:
--
--     SELECT count(*) FROM photos WHERE place_id IS NOT NULL   -- 95, seeded
--
-- It is unchanged by `PUT /v1/photos/{id}`, `POST /v1/photos/snooze`,
-- `PUT /v1/walks/{id}` and a re-file between pins; it RISES on a re-file of an
-- unfiled photograph; and it falls by a KNOWN amount on `DELETE
-- /v1/photos/{id}` of a filed one and on D2's delete branch. Any other
-- movement is a filing that was destroyed by something that reported success.
-- ------------------------------------------------------------------------
--
-- Run it against a seeded database:
--
--     psql "$TEST_DATABASE_URL" -f scripts/no_dangling_references.sql
--
-- Every row but the last must read 0. The last is a count and is read against
-- the log it was run on.

-- ON_ERROR_STOP IS THE ONE LINE THAT MAKES THIS FILE ABLE TO FAIL, and it was
-- added after measuring the alternative. `psql -f` does NOT stop on an error by
-- default and exits 0 anyway: run verbatim against a database with no `photos`
-- table at all, this file printed `ERROR: relation "photos" does not exist` and
-- answered exit=0. An acceptance check whose command cannot exit non-zero is
-- rule 10's own class — it can only fail when the artefact is wrong, and here
-- it could not fail at all. `psql -c` gets it right by itself (exit=1 on the
-- same database), which is why only the file half needed this.
\set ON_ERROR_STOP on
\pset footer off

-- A photograph naming a visit that is gone. `photos_visit_fk` is
-- `ON DELETE SET NULL (visit_id)`, so this can only be non-zero if something
-- wrote the column directly.
SELECT 'photos naming a visit that is gone' AS check, count(*) AS n
FROM photos p
LEFT JOIN visits v ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
WHERE p.visit_id IS NOT NULL AND v.id IS NULL

UNION ALL

-- A photograph naming a place that is gone. `photos_place_fk` is the same
-- shape, and D2's keep branch is what exercises it.
SELECT 'photos naming a place that is gone', count(*)
FROM photos p
LEFT JOIN places pl ON (p.traveller_id, p.place_id) = (pl.traveller_id, pl.id)
WHERE p.place_id IS NOT NULL AND pl.id IS NULL

UNION ALL

-- A visit naming a trip that is gone. `visits_trip_fk` is CASCADE, so D3 takes
-- these with the trip.
SELECT 'visits naming a trip that is gone', count(*)
FROM visits v
LEFT JOIN trips t ON (v.traveller_id, v.trip_id) = (t.traveller_id, t.id)
WHERE t.id IS NULL

UNION ALL

-- THE HALF-FILED STATE, AND IT IS NOT A DANGLING REFERENCE. A photograph
-- naming a place with no occasion is a state the client's model has never
-- expressed — measured across the 284 fixture photographs: 95 carry both, 189
-- carry neither, place-only 0, visit-only 0. It is here because it is the
-- SECOND thing all-zeroes above cannot see, and because DEC-83 leaves this
-- rule in Go rather than in the schema: the paired CHECK was executed and
-- aborts D2's keep branch.
SELECT 'photographs that are half-filed', count(*)
FROM photos
WHERE (place_id IS NULL) <> (visit_id IS NULL)

UNION ALL

-- A photograph naming a visit that belongs to some OTHER place. This is
-- DEC-83's own executed finding — the schema accepts it — so it is checked
-- here rather than guaranteed anywhere.
SELECT 'photographs filed to another place''s occasion', count(*)
FROM photos p
JOIN visits v ON (p.traveller_id, p.visit_id) = (v.traveller_id, v.id)
WHERE p.place_id IS DISTINCT FROM v.place_id

UNION ALL

-- A walk with an empty track. `walks_points_array_ck` does not refuse it —
-- an empty array IS an array — and 0003's `walks_points_present_ck` does. A
-- non-zero here is a database that predates 0003 or a constraint somebody
-- dropped.
SELECT 'walks with an empty track', count(*)
FROM walks
WHERE jsonb_typeof(points) = 'array' AND jsonb_array_length(points) = 0

UNION ALL

-- AND THE COUNT THAT MUST NOT FALL. It is NOT a zero and it is deliberately in
-- the same output, so that nobody reads six zeroes and stops.
SELECT 'photographs naming a place (THIS ONE IS NOT A ZERO)', count(*)
FROM photos
WHERE place_id IS NOT NULL;
