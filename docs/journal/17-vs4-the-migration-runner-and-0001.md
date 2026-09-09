## VS4 — the migration runner, and 0001

**TEST-FIRST for everything a test could reach first**, in the §6.7 order, with
two labelled exceptions named under "How each leg was seen red". Written 23
August 2026 against PostgreSQL 17.11 in `postgres:17`, Go 1.26.5 darwin/arm64.

### THE BLOCKER, and why three review passes walked past it

A staff DBA executed the schema on PostgreSQL 17.11 and returned one blocker.
**A composite foreign key's `ON DELETE SET NULL` nulls EVERY column of the
referencing key** — `traveller_id` included, and `traveller_id` is `NOT NULL`.
PostgreSQL echoes its own generated statement:

```
UPDATE ONLY "public"."photos" SET "traveller_id" = NULL, "place_id" = NULL
  WHERE $1 = "traveller_id" AND $2 = "place_id"
```

So D2's keep branch — *"they lose the pin but keep their date and city"* — and
`_repointed` both **abort** rather than clearing a pin. Two of the seven
cascades this file exists to implement.

The fix is DEC-66's column-list form, `ON DELETE SET NULL (place_id)` and
`ON DELETE SET NULL (visit_id)`, and it is **verified rather than read**: after
the change, deleting the place leaves `traveller_id` intact and clears
`place_id` and `visit_id` together, and `pg_constraint.confdelsetcols` reads
`{5}`/`{6}` instead of empty.

**Why it hid, and what that means for the test that catches it.** Every cascade
leg anyone had written deleted a **trip**, and on that path the photograph is
cascade-deleted through `photos.trip_id` *before* the broken foreign key fires.
The leg that reaches it is `TestDeletingAPlaceClearsThePinAndLeavesThePhotographStanding`,
and it asserts on the **surviving rows and their `traveller_id`**, never on
error-or-no-error.

**PostgreSQL 15 is now a hard floor.** It is stated in the migration header,
in the package comment, and enforced by `testdb.Open`, which reads
`server_version_num` and refuses anything below `150000` with a message naming
both the feature and the version it found. Compose pins `postgres:17`; a
developer's local 14 would otherwise reproduce the blocker as
`syntax error at or near "("`.

### The scope divergence, stated rather than discovered

**The slice plan's VS4 says FOUR tables. This ships ELEVEN plus the ledger, and
that is forced rather than chosen.** DEC-64 replaces `trips.city_ids jsonb`
with a `trip_cities` join table carrying real foreign keys **to `cities`**, so
`cities` must exist; DEC-66's blocker fix cannot be tested without `places`,
`visits` and `photos`, because deleting a **place** is the only path that
reaches it; DEC-67 is about `share_links`; DEC-68 is about `visits.at`; and
DEC-57's third city RESTRICT is on `walks`. The four-table version would have
shipped the blocker with a green suite, which is exactly the state the review
found.

The plan's VS4 text is stale in two further places and both are superseded by a
ruling rather than by preference: `trips.city_ids jsonb` (DEC-64) and
`email text NOT NULL UNIQUE` (DEC-65).

### Findings this step made that the review did not

Three, and the first is a blocker of the same class as the one that was found.

**1. `DELETE FROM travellers` was impossible, and only the traveller-delete leg
could see it.** The review verified that account deletion survives seven
RESTRICT foreign keys — correct against a schema with no `trip_cities`. Add
DEC-64's table with DEC-69's RESTRICT on `city_id` and it stops being true:

```
ERROR: update or delete on table "cities" violates foreign key constraint
       "trip_cities_city_fk" on table "trip_cities" (SQLSTATE 23503)
```

The mechanism is the AFTER-trigger queue, and it is worth writing down because
the review's explanation — *"RESTRICT checks are AFTER-ROW triggers evaluated at
end of statement"* — is right about the timing and leads to the wrong
conclusion. Deleting a traveller queues one cascade per foreign key that
references `travellers` **directly**, in one batch; each cascade then **appends**
the checks its own delete provokes. The cascade into `cities` appends the
RESTRICT check for `trip_cities.city_id`; the rows that check looks for are
removed by the cascade from `trips`, which is appended **after** it. Every other
entity table already had a direct traveller cascade and is emptied in the first
batch, which is why `trip_cities` was the only one and why it could not exist
before DEC-64. The fix is `trip_cities_traveller_fk … ON DELETE CASCADE`, which
puts it in the first batch with everything else.

**2. DEC-70's instruction to drop `share_links_trip_idx` is stale, and DEC-70's
own mechanism is what caught it.** The ruling says the index duplicates the
primary key. It was measured against a reconstruction whose PK was
`(traveller_id, trip_id)`; **DEC-67, ruled in the same batch**, moves the PK to
`(traveller_id, token)`, after which `(traveller_id, trip_id)` leads no index at
all. `share_links_one_live` cannot serve the foreign key either — it is
**partial**, and an RI check needs an index covering every row. The index is
kept, and `TestEveryForeignKeyChildColumnSetLeadsSomeIndex` — which derives the
answer from `pg_index.indkey` rather than from a list, exactly as DEC-70 asks —
reddens without it. **Derive the list, do not read the ruling's copy of it.**

The same leg shows the ruling's other half is right and then some:
`visits_place_ordinal_uq (traveller_id, place_id, ordinal)` **subsumes**
DEC-63's separate index on `(traveller_id, place_id)`, so that one is not
created either. DEC-63 asked for eleven; the derivation asks for what is
missing, and the two answers differ in three places.

**3. `pg_depend` cannot tell you which `lower()` a functional index bound to.**
Dependencies on **pinned** system objects are not recorded, so the obvious query
returns zero rows — measured. `pg_get_indexdef` is no better: it prints
`lower(email)` whichever function it resolved. The answer is in the stored
expression tree, `substring(indexprs::text from ':funcid ([0-9]+)')`, and the
leg reads it.

**And the hazard behind that is real, measured, and worse than a missed index.**
Under `SET search_path = <schema>, pg_catalog` with a shadowing `lower` defined,
`WHERE lower(email) = lower($1)` does not merely stop using the index —
**both sides collapse to one constant and the predicate matches every row**, so
an address nobody registered resolves to a traveller, with no error anywhere.
That is why `Migrator.Schema` pins `search_path` for the run, and why the
application role must never be given a `search_path` that puts a schema ahead of
`pg_catalog`. The `"$user"` half is measured too: with a schema named after the
connecting role present, `CREATE TABLE lands_where (x int)` lands in
**`travellog`**, not `public`.

### The runner, and the four decisions in it

**One pinned `*sql.Conn` for the whole run.** `pg_advisory_lock` is
SESSION-scoped and `database/sql` is a POOL, so `db.ExecContext(lock)` and
`db.ExecContext(unlock)` can land on two different connections; the unlock then
does nothing and reports it as a **WARNING and a `false` return**, both of which
`database/sql` discards for an `Exec`. The lock survives until that specific
connection closes, which under `SetMaxIdleConns` may be never. That is
reproduced in this repository rather than quoted:
`TestASessionLockTakenOverThePoolCanBeUnlockedOnTheWrongConnection` takes the
lock on one pooled connection and releases it from another, and the release
returns `false` and raises nothing.

**`-- migrate:no-transaction`, now rather than later.** Measured:
`BEGIN; CREATE INDEX CONCURRENTLY foo_idx ON photos(caption);` answers
`ERROR: CREATE INDEX CONCURRENTLY cannot run inside a transaction block`, and
`VACUUM`, `ALTER SYSTEM`, `CREATE DATABASE` and `REINDEX CONCURRENTLY` are the
same class. The hatch is proven **from both sides** — the same file with and
without the directive, one refused by PostgreSQL and one applied.

**And the hatch is what forces the splitter.** The simple query protocol wraps
several statements sent in one message in an **implicit transaction block**, so
a whole file handed to one `Exec` re-creates exactly the condition the directive
exists to escape. `splitStatements` is a lexer over the five things that differ
from `strings.Split(src, ";")`: `'…'` with `''` and with `\` **only** in an
`E'…'` string, `"…"` identifiers, `$$`/`$tag$` bodies (and `$1`, which is not
one), `--` to end of line, and `/* */` **which nests in PostgreSQL**. Once it
exists the transactional path uses it too, so it is exercised by the real
migration on every run and a failure names the statement.

**The filename rule is a guard, not a convention.** S05 says lexical order, and
lexical order over an `embed.FS` is correct only at a constant width:
`10_x.up.sql` sorts before `2_x.up.sql`. The runner refuses
anything but `NNNN_name.up.sql`, refuses two files at one version, refuses an
`.up.sql` with no `.down.sql` beside it, and refuses an empty directory — and it
does all of that **before it creates the ledger**, so a refused directory leaves
no trace in the database.

**`schema_migrations` has three columns.** DEC-17's own text says
`(version, applied_at)` and S05's work field says `(version, checksum,
applied_at)`. The checksum column is what the loud failure is made of, so
DEC-17's text is the one that is wrong.

### `internal/postgres/testdb` — a schema per test, in the DSN

`testdb.Open(t)` creates a fresh schema and hands back a pool **whose DSN
carries `options=-c search_path=<schema>`**. Not `SET search_path`: that applies
to the one connection it landed on, and every other connection in the pool still
points at `public`. It is the same class of defect as the migration lock's, one
layer up.

`Open` takes a narrow `TB` interface rather than `*testing.T`, and that is what
makes the skip **falsifiable**: a fake records which method was called, so
"does it actually skip?" is proven rather than assumed. Asserting only the skip
STRING would have left that unguarded, and the message is asserted too — it
names `TEST_DATABASE_URL` and `make test-db`.

### Two sweeps were corrected, and the corrections are the interesting half

Both went red **against correct work**, which is the signal that the sweep's
premise was wrong rather than the code.

- **The `os.Getenv` monopoly.** `internal/config/sweep_test.go` excludes test
  files and its own comment says *"VS5's internal/store/testdb does exactly that
  by design"* — but `testdb.go` is **not a test file**. It is an ordinary
  package imported by other packages' tests, and the exclusion its comment cited
  never covered it. There is now a **named exemption list**, asserted by
  equality, keyed on the file path, with a second leg asserting the exempted
  file still exists. `TEST_DATABASE_URL` is not application configuration: no
  build reads it, the binary never sees it, and the function that reads it
  exists to skip a test.
- **The pgx monopoly.** `TestPgxIsImportedExactlyOnceBlankAndInMain` went red the
  moment `testdb` opened a pool, which it must. **"Exactly once" was never what
  go_backend.md L20 says**: it says *solely as a blank import driver*, which is a
  claim about HOW, not how many. The count became a named list asserted by
  equality — `cmd/api/main.go` and `internal/postgres/testdb/testdb.go`, each
  with its reason — and the blank-import assertion, the one a grep cannot make,
  now applies to every entry rather than to the only entry.

### `make migrate` stops failing loudly, and migrations run at boot

`run()` migrates before it listens, so a container that is running is a
container whose schema is current — the property `docker compose up -d` has to
have for VS8's arc to mean anything. `-migrate-only` is the same binary with no
listener, and `make migrate` invokes it **inside the compose network** so the
seven variables come from the compose file rather than a second copy in the
Makefile, which is the defect `make test-db` had.

Run against the real stack:

```
{"…","msg":"database ready","maxOpenConns":8,"maxIdleConns":4}
{"…","msg":"migrate: applied","version":"0001","file":"0001_init.up.sql","no_transaction":false}
{"…","msg":"migrations up to date","applied":1}
{"…","msg":"listening","addr":":8080"}

$ psql -tAc "select version, left(checksum,12) from schema_migrations"   ->  0001|6f9da0976b8e
$ psql -tAc "select count(*) from information_schema.tables where table_schema='public'"  ->  12
$ make migrate                       # a second run
{"…","msg":"migrations up to date","applied":0}
```

**And the checksum guard fired for real, unplanned, in the live stack.** After
the comment conventions landed mid-step, `0001_init.up.sql` was rewritten — same
schema, different bytes — while `pgdata` still held the row from the earlier
run. The api would not come up, and said exactly why, on a restart loop:

```
{"…","level":"ERROR","msg":"api: stopping","err":"migrations: postgres: a
 migration was edited after it was applied: 0001_init.up.sql was applied with
 checksum 6f9da0976b8e… and now hashes to 7c708df69771… — migrations are
 forward-only, so neither re-running it nor ignoring it is safe; revert the
 edit, or add a new migration"}
```

That is the acceptance check answering in the field rather than in a test, and
it is worth recording for two reasons beyond the tick. **A container that
refuses to start is the correct behaviour and it looks like a broken deploy** —
under `restart: unless-stopped` it loops, so the message has to be the first
thing in the log, which it is. And the fix here was `docker compose down -v`,
which is right ONLY because the volume held a pre-release schema created an hour
earlier by this step: `make down` deliberately keeps the volume, and against a
real log the answer is a new migration, never `-v`.

### How each leg was seen red

**67 top-level legs, 92 counting subtests**, in `internal/postgres` and
`internal/postgres/testdb` — re-derived, not remembered:
`go test -v -count=1 ./internal/postgres/... | grep -c -- '--- PASS'`.

- **Test-first (§6.7)** for the splitter, the directory rules and the testdb
  seam: each was written against a **compiling stub** — signatures with
  zero-value bodies — so the red is an assertion failure rather than
  `undefined: splitStatements`. Fourteen split legs and eight load legs came off
  that stub, and the four testdb legs came off a stub whose `Open` returned nil.
- **Test-first for the SCHEMA**, in the only sense available to a `.sql` file:
  `schema_test.go` was written and run before `0001_init.up.sql` had ever been
  executed, and **the first run was red in four places**. Three were defects in
  the schema or in the test and one was the account-deletion blocker above.
- **Labelled otherwise: the runner's DATABASE-facing legs.** By the time a real
  Postgres was reachable the runner compiled, so those nine legs went green on
  first run and their red comes from the mutation sweep instead. They are
  recorded as **mutation-proven, not test-first**, and each is named in the
  table below.

### The mutations

`scratchpad/vs4/mutate.py`. It **snapshots and restores by file copy**, never
through git, re-checks the sha256 of every touched file after each restore, and
**stops the run** if one has moved — VS2 lost a step's implementation to a
`git checkout` harness and contaminated six of thirteen results, and VS3's
package was untracked, where a git harness would have reported 54 clean kills
against 54 un-mutated files. Every mutation asserts the file **actually
changed** before the suite runs.

The sixteen CHECK-constraint mutations are **generated from the constraint
names the table-driven leg asserts**, rather than hand-written, so the list
cannot drift from the schema.

The four reds worth quoting in full:

```
Q1-blocker      ON DELETE SET NULL (place_id) -> ON DELETE SET NULL
  TestDeletingAPlaceClearsThePinAndLeavesThePhotographStanding
    deleting the place: ERROR: null value in column "traveller_id" of relation
    "photos" violates not-null constraint (SQLSTATE 23502)
  TestTheDeleteActionsAreWhatTheSheetsSay
    photos_place_fk is ON DELETE SET NULL with 0 named columns, want exactly 1

Q18  drop trip_cities_traveller_fk
  TestDeletingATravellerWorksDespiteEveryRestrict
    deleting a traveller: ERROR: update or delete on table "cities" violates
    foreign key constraint "trip_cities_city_fk" on table "trip_cities"

Q9   drop share_links_trip_idx   (the index DEC-70 said to drop)
  TestEveryForeignKeyChildColumnSetLeadsSomeIndex
    share_links_trip_fk on share_links (child columns [1 2]) leads no index

R2   drop the advisory unlock
  TestTheLockIsActuallyReleasedWhenTheRunFinishes
    the migration lock is still held after Migrate returned — the unlock landed
    on a different connection than the lock
```

### The count, and the one leg no mutation reddened

**71 mutations. 68 RED, 3 MISS, and no PATTERN-MISS, NO-DIFF or BUILD-BREAK in
the final run.** `set(VS4 legs) - set(reddened)` is computed by the harness
rather than felt, and it holds **exactly one** name:

**`TestASessionLockTakenOverThePoolCanBeUnlockedOnTheWrongConnection`** is a
CHARACTERISATION of `database/sql` and PostgreSQL, not a guard on this
repository: no mutation here can change what a pool does with a session-scoped
lock. It earns its line because it is the measurement the pinned-connection
design rests on, and it lives beside the code rather than in a review document.
It says so in its own doc comment.

**And one BRANCH is unfalsifiable while its leg is not**, which is a distinction
worth keeping apart. No mutation of the splitter's doubled-quote branch can
redden `TestSplitStatementsHandlesADoubledQuoteInsideAString`, and the reason is
arithmetic: over **well-formed** input, naive pairing `(1,2)(3,4)…` and
escape-aware pairing consume exactly the same quotes and put exactly the same
characters inside a string, so disabling the branch merely makes the lexer see
two adjacent strings and the semicolon lands inside the second either way.
Measured — the mutation ran, changed the file, the suite stayed green. The leg
IS reddened, by the mutation that stops a single quote opening a string at all,
so it is coverage; the branch is the part nothing proves, and the source says so.

One mutation is **test-side** and is labelled that way rather than counted with
the rest: `W3` points the environment-exemption list at a file that is not in
the tree, which reddens `TestEveryEnvironmentExemptionStillExists`. Nothing in
`lib` code can falsify a list that only describes itself.

Three other mutations are recorded as MISS rather than dropped, because a MISS
is sometimes the harness being wrong and sometimes a real fact:

| mutation | why it missed |
|---|---|
| `S2` doubled-quote branch disabled | the leg above — the branch is provably redundant for well-formed input |
| `S5` drop the closing-`$` check | the digit check still catches `$1`; `S5b` removes BOTH and reddens |
| `S6` drop the digit check | the closing-`$` check still catches `$1`; same answer |

**Two guards defeat one mutation each, which is the finding S5/S6 carry:** a
single mutation aimed at a doubly-guarded property proves nothing, and reading
the MISS as "the leg is weak" would have been the wrong conclusion.

### What VS4 leaves guarded by nothing

- **The tile of evidence the review could not supply: `ON DELETE SET NULL (col)`
  on PostgreSQL 14.** It is documented as added in 15 and verified working on
  17.11; nobody has pulled a 14 image and watched it fail. The floor is
  therefore **read from the documentation and confirmed on 17**, not bisected.
  `testdb` refuses the older server on the version number alone.
- **DEC-51's content-type allowlist.** `media_objects_content_type_present_ck`
  stops `''` and nothing else, so `content_type = 'text/html; <script>'` is
  accepted. The allowlist is named in no artefact, so the constraint that
  belongs there cannot be written yet. It is marked as the weakest check in the
  file, in the file.
- **Whether any index is CHOSEN.** The catalog leg proves every foreign key's
  child columns lead some non-partial index — a structural claim true at any
  size. It proves nothing about the planner, and at fixture scale the planner
  correctly declines nearly all of them: the review measured **exactly one**
  index used during a full trip cascade over 284 photographs. DEC-70's
  `enable_seqscan=off` half is **not implemented** and is the honest gap here.
- **`statement_timeout` and `idle_in_transaction_session_timeout`**, both still
  `0`, which is "no limit" rather than "untuned". Declined below.
- **The migration's behaviour under a real concurrent boot.** The lock is proven
  by holding it from another session, which is the mechanism; two containers
  starting at once is VS8's arc.

### Declined, each with the measurement

- **`ALTER ROLE … SET statement_timeout` in 0001.** The review's own
  `matters_at` says *only at scale*, and a role-level GUC is deployment
  configuration rather than schema: it is not reversed by the down file, it
  applies to every session including psql, and it would make the migration's
  effect depend on which role ran it. `search_path` — the half marked *this
  size*, because DEC-65's index is functional — **is** handled, on the runner's
  own connection.
- **A `content_type IN (…)` CHECK.** See above: the list does not exist.
- **The `share_links` duplicate-index drop DEC-70 asks for.** Measured: with
  DEC-67's primary key it is not a duplicate, and removing it reddens the
  catalog leg.
- **`DEFERRABLE INITIALLY DEFERRED` on `trip_cities_ordinal_uq`.** It does fix
  the in-place `UPDATE … SET ordinal = 1 - ordinal` form, and it moves the
  violation to `COMMIT`, which means a 422 mapped off an error returned by
  `tx.Commit()`. Delete-then-insert is mandated instead, and there is a leg
  proving a full reorder round-trips through it. Live trap recorded in the
  schema: `SET CONSTRAINTS ALL DEFERRED` against a NON-deferrable constraint
  succeeds **silently** and changes nothing.
- **`pg_advisory_xact_lock` for the migration.** It would scope the lock to a
  transaction and remove the unlock question entirely — and it is incompatible
  with the `-- migrate:no-transaction` hatch, which has no transaction to hang
  it on. The pinned connection is the form that keeps both.

### Three things about the layout that a later worker needs

- **`internal/store` is `internal/postgres`**, ruled by the human mid-step.
  `store.Store` stutters, and worse here specifically: the interface it will
  implement is declared in `internal/logbook` and is *also* called `Store`, so
  `logbook.Store` and `store.Store` would sit side by side meaning different
  things. `postgres.New(...)` says what it is. Two sibling renames were ruled at
  the same time and **neither package exists yet** — write them under the new
  names when they land: `internal/api` is **`internal/rest`** (it collided with
  `cmd/api`, the binary), and `internal/objects` is **`internal/media`** (it
  matches the domain's word and the `media_objects` table).
- **`migrations/` is a Go PACKAGE, not just a directory**, and that is forced:
  `//go:embed` cannot reach outside its own package directory, so
  `//go:embed ../../migrations` does not compile. The alternative was to move
  the `.sql` files under `internal/postgres`, and the layout above puts them at
  the repository root where a reviewer looking for the schema will find them.
  **Both** `.up.sql` and `.down.sql` are embedded, because the runner refuses an
  up file with no down file beside it and can only check that if the down files
  are there.
- **The two comment conventions landed mid-step** and are applied to VS4's files
  only — the repository-wide sweep is scheduled for after the slice runs end to
  end. In this step that meant moving every in-body sentence out of a function,
  a struct and a `const` block, and moving every per-constraint note in
  `0001_init.up.sql` out of the `CREATE TABLE` parentheses to a block above the
  table. The sheet line each foreign key implements is still beside it; it is
  now above the table rather than inside it.

### Divergence 3 — the Dockerfile is at the repository root

`go_backend.md` L17 names the Standard Go Project Layout. That layout puts Dockerfiles in
`build/package/` and orchestration in `deployments/`. **Both are among its most widely
ignored conventions** — open almost any production Go service and the Dockerfile is at the
root — and the Go team has distanced itself from the layout generally.

So: **`Dockerfile` at the repository root**, `deploy/` keeps its name, `migrations/` stays
at root. Recorded here as a deliberate divergence rather than left as an unrecorded
deviation, which is the point: a divergence register is only worth reading if nothing is
missing from it, and three deviations were sitting unrecorded beside two carefully
recorded ones.

A move was queued in the opposite direction and **withdrawn before application** — it
optimised for "matches the document the spec names" over "looks like what a person would
do", and those genuinely conflict here.

**Consequence beyond the path:** the build context already *is* the repository root, so
`context: .` and `dockerfile: Dockerfile` both stop being relative paths pointing up and
out of their own directory. `.dockerignore` did not move; it belongs to the context, not
to the Dockerfile.

### Fixed at the same time — the postgres healthcheck budgets did not nest

`deploy/docker-compose.yml` had postgres at `timeout: 3s` against `interval: 2s` — a
timeout **longer than the gap between probes**. That is precisely the overlap the API
image's own test asserts against (`hc.Timeout >= hc.Interval` fails it), so the rule was
enforced on one service and violated on the other. Now `interval: 3s`, `timeout: 2s`,
`retries: 20` — same ~60s total grace, correctly nested.

Found by the comment sweep while reading, not by any test. **Nothing guards the compose
healthcheck budgets**; the API image's are guarded. Worth a leg in VS8's arc.

---

