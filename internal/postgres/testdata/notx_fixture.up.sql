-- migrate:no-transaction
-- migrate:re-runnable
--
-- THE SUBJECT OF DEC-99's TWICE-RUN GUARD, AND IT IS A FIXTURE RATHER THAN A
-- SHIPPED MIGRATION FOR A REASON THAT MUST BE SAID OUT LOUD OR THE GUARD IS
-- VACUOUS: migration 0003 is entirely transactional and carries no directive
-- at all, so today `migrations/` holds NO no-transaction file and a test whose
-- subject set is empty is a green that means nothing.
--
-- The guard goes in NOW rather than with the first real `CREATE INDEX
-- CONCURRENTLY`, because a guard written after the incident is a cleanup. This
-- file is what it is written against, and `TestNoTransactionMigrationsAreReRunnable`
-- asserts the subject set is non-empty before it runs anything.
--
-- IT USES THE STATEMENT CLASS THE RULING NAMES. `CREATE INDEX CONCURRENTLY` is
-- exactly the statement that needs the directive AND that leaves an INVALID
-- index behind when it fails, so the fixture exercises the real hazard rather
-- than standing in for it with a CREATE TABLE.
--
-- ITS NAME HAS NO FOUR-DIGIT PREFIX ON PURPOSE. `loadMigrations` refuses
-- anything that is not `NNNN_name.up.sql`, so a numbered file sitting in
-- testdata could be mistaken for a migration; the test wraps these bytes in an
-- fstest.MapFS under a conforming name instead. See migrate_test.go's
-- fixtureFS.

CREATE TABLE IF NOT EXISTS notx_probe (x int);

CREATE INDEX CONCURRENTLY IF NOT EXISTS notx_probe_x_idx ON notx_probe (x);
