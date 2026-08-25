-- The reverse of notx_fixture.up.sql.
--
-- It exists because `loadMigrations` refuses an .up.sql with no .down.sql
-- beside it — a migration nobody can reverse by hand is one that has to be
-- reversed by restore — and the fixture is loaded through the real runner, so
-- it is held to the real rule.
--
-- BOTH STATEMENTS ARE `IF EXISTS`, which is the same requirement the up file's
-- `-- migrate:re-runnable` header declares: a down file for a no-transaction
-- migration can half-apply too.

DROP INDEX IF EXISTS notx_probe_x_idx;

DROP TABLE IF EXISTS notx_probe;
