-- The reverse of 0001_init.up.sql.
--
-- DOWN FILES ARE CHECKED IN AND ARE NEVER RUN AUTOMATICALLY. The runner
-- (internal/postgres/migrate.go) applies .up.sql only, and refuses an .up.sql
-- with no .down.sql beside it — a migration nobody can reverse by hand is one
-- that has to be reversed by restore.
--
-- Dropped in reverse dependency order, so this file is readable as the inverse
-- of the one beside it rather than relying on CASCADE to sort it out. Every
-- index above belongs to one of these tables and goes with it.

DROP TABLE IF EXISTS share_links;
DROP TABLE IF EXISTS walks;
DROP TABLE IF EXISTS photos;
DROP TABLE IF EXISTS visits;
DROP TABLE IF EXISTS places;
DROP TABLE IF EXISTS trip_cities;
DROP TABLE IF EXISTS trips;
DROP TABLE IF EXISTS cities;
DROP TABLE IF EXISTS media_objects;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS travellers;
