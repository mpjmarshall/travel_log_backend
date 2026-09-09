## R4 — the seed, the round trip four lists had never had, and the first backup

The fourth step of plan-v7, and **the step where the volume stops being
disposable**. Everything before it wrote rows a test made and a test could
remake. From here `travellog_pgdata` holds a log, the plan's own premise is
that PostgreSQL is the record and the phone is a cache — and the tooling that
was destroying it is the same tooling five of the eight steps' acceptance
checks tell a developer to run.

**Four commits**, the same reading of DEC-23 R1 through R3 took: the record is
written as the step runs, and a step this size as one commit is unreviewable.

### THE ROUND TRIP IS THE STRONGEST LEG THIS PROJECT CAN WRITE, AND IT COST TWO CORRECTIONS TO THE PLAN'S OWN SKETCH

`internal/logbook` proved the client's own document round-trips through the
**types**. Nothing had ever put a city, a place, a visit, a photograph or a
walk into PostgreSQL and read it back through the emitter, so the ten read
queries' column ordering, the visits nesting and the `jsonb_array_elements`
unnest were guarded by their scans compiling and by nothing else.

The plan writes the leg out in full. **Neither half of it can pass against
correct work**, and both are the same class as R2's banned-presign grep and
R3's `heic` grep — a check written from the shape somebody expected rather than
the shape that is there.

- **It diffs the two documents POSITIONALLY.** The store orders every top-level
  list by its own id **on purpose** — `logbook_store.go`'s own header says it,
  "about determinism rather than display", because two reads with no write
  between them have to be byte-identical or the ETag is a claim the server
  cannot keep. The captured document is in the order the client's encoder wrote
  it, which is travel order. Measured, both ways in one leg: **2,013 positional
  differences, 0 aligned**, the first being `logbook.cities[0].id: client
  "kyoto", server "busan"`. Not one of the 2,013 is a defect. So the five lists
  are aligned by id before the diff and **ORDER is asserted by three separate
  legs instead** — which is what leaves the diff able to say something.
- **It asserts "visits come back newest first" as a RULE.** Two of the
  seventeen fixture places break that rule: `bukchon` is 2027-10-02 then
  2027-12-12, and `nishiki` puts a 2027 date before four 2026 ones. The
  definition of done says the right thing where the sketch does not —
  *"asserted against the fixture's own ordering, not against a rule written
  here"* — and what is load-bearing is that **the order the client wrote is the
  order that comes back**, because the client reads `visits.first.at` as "last
  visited". `visits.ordinal` is the only thing carrying it.

Both legs carry positive controls, because an ordering assertion over
single-element lists cannot fail: at least two places with more than one visit,
at least one trip whose `cityIds` are not alphabetical, at least one place that
does lead with its newest visit.

### THE ONE INPUT THE ROUND TRIP CANNOT CHECK, AND WHERE IT IS ACTUALLY GUARDED

The plan's fourth mutation is "point one locator at the other's digest — the
diff names `asset` on 189 photographs, which is a satisfying red". **Run, it
does not redden the round trip at all.** `RewriteAssets` is applied once and its
output is both the reference and the input to the load, so a wrong mapping
agrees with itself on both sides. The leg that went red instead was
`TestFromDocumentRefusesAnAssetWithNoObject`, which is about something else.

That is a general shape worth carrying: **a transformation applied to both
sides of a comparison is invisible to it**, and the same trap is why
`RewriteAssets` copies its input rather than rewriting in place — an in-place
rewrite makes the "before" and the "after" the same memory and the whole leg
passes against anything.

What guards the mapping is two things in two places:

- the per-digest row counts in `internal/logbook` — **205 rows address
  card-ireland** (189 photographs + 3 trip + 7 city + 6 place covers) and
  **103 address hero-mountain** — so a collapsed mapping gives one digest 308.
- **`cmd/seed` computes each digest from the file's bytes and never writes one
  down.** The address IS the content, so a wrong mapping is bytes whose sha256
  is not the key they were signed for, and DEC-88's signed checksum refuses the
  PUT. It is `media.Address`'s argument one layer out, and it is why the plan's
  `media.FixtureDigests` — a table of literals — is **not** what was built.

### DEC-97's REFUSAL IS ONE PREDICATE AND IT IS NOT THE ONE THE PLAN WROTE

R4's own text scopes the refusal to "a non-empty LOG". **A freshly migrated
database is empty**, so the guard does not fire on `make up && make seed`,
which is the obvious first thing an operator types — and DEC-86 closes
registration once any traveller row exists. The result is a deployment whose
only account has a passphrase printed in a terminal, carrying a 600/min budget,
with the real traveller unable ever to create theirs.

So the predicate is **the same one register uses: any traveller row**. It covers
the other direction too, which the plan left unspecified: a traveller who has
registered and written nothing is not an empty database.

**It is checked twice, and the two are not the same check.** Once before the
upload, so a refused run leaves no bytes in a bucket; once inside the
transaction, which is where it is a guard — outside it, two seeds racing each
other both read zero and both insert.

**And a second guard the plan does not have.** `--i-know-this-is-a-dev-database`,
because PD-02's required `-dsn` stops an ambient `DATABASE_URL` aiming the
command and cannot stop a correct-looking URL pointing somewhere real.

**Both refusals print where they were pointed** (SAF-MAJ-8) — and the refusal
says what is at stake in terms the plan left implicit: **nothing in this
application can delete a city** (DEC-57). Exhausting every control the client
has still leaves twelve cities and two media objects standing, with no route,
no sheet and no control able to touch them. A seed into somebody's record is
permanent.

**The DSN is printed REDACTED, and that is a stated narrowing of the ruling.**
The host, the port, the database and the user are the whole of "which
database"; the password identifies nothing and does go into terminal
scrollback.

### THE PASSPHRASE IS GENERATED, AND SO IS THE TRAVELLER ID — FROM THE DATABASE

24 characters over a 31-character alphabet, about 118 bits, drawn from
`crypto/rand` and printed once. Nothing stores it; the column holds an argon2id
hash. `auth.MinPassphraseBytes` is 8 and this is not chosen against that floor:
it is chosen so that a credential printed in a terminal and never rotated is
not the weak link.

The traveller id is `SELECT gen_random_uuid()` — **the database's, not a
package's**, which is `auth_store.go`'s own rule — and it is drawn *before* the
upload because the bucket key is `<traveller>/<digest>` (DEC-38).

### THE SEED'S OWN SEAM, STATED RATHER THAN DISCOVERED

Media first, database second (DEC-78). The two PNGs are uploaded to the bucket
**before** the ten-table transaction, so a load that dies at table seven rolls
the database back and **leaves the objects**. That is intended — content
addressing makes the re-upload idempotent, and a database referencing bytes
nobody uploaded is the worse failure — but it has to be written down, because
once `-sweep-media` has a grace window an uploaded-not-committed object is
indistinguishable from an orphan.

A second upload of the same address answers **412 PreconditionFailed** and the
seed reads it as success, exactly as the commit route does (DEC-88).

### `unnest` WAS THE RIGHT SHAPE AND IS NOT AVAILABLE, AND THAT IS ARITHMETIC THE GENERATOR INHERITS

The first draft of `load.go` built one `unnest($1::text[], …)` per table —
fourteen parameters at any row count. `cmd/api/imports_test.go` went red on it:
database/sql has no array type, so it needs pgx's `pgtype`, and spec L20 says
pgx is used **solely as a blank import driver**. The sweep was right and the
draft was wrong.

What ships is one multi-row `VALUES` per table, and the ceiling is now a fact
the generator has to know: the wire protocol counts parameters in an **int16**,
so the limit is **65,535**. The fixture's largest statement is photos at
**284 x 14 = 3,976**. `DefaultPhotos` at the same width is **700,000**, more
than ten times over — so `generate.go` chunks and the 284-row seed does not.
The sentence is in `generate.go` beside the stub, where whoever writes it will
be standing.

**THE STATED REASON ABOVE IS WRONG, AND R6 MEASURED IT.** The conclusion — one
multi-row `VALUES` per table — is fine and is not being changed. What is wrong
is *"database/sql has no array type, so it needs pgx's `pgtype`"*: an ARRAY
PARAMETER NEEDS NO IMPORT AT ALL. Measured at R6, against this module's own
driver, with nothing added to any import block:

```
[]string            n=2 err=<nil>      []bool             n=2 err=<nil>
[]*string (one nil) n=2 err=<nil>      []time.Time        n=2 err=<nil>
[]int64             n=2 err=<nil>      []sql.NullString   n=2 err=<nil>
[]int32             n=2 err=<nil>      []float64          n=2 err=<nil>
```

The precise sentence: `database/sql`'s own `driver.Value` set has no array, but
pgx's stdlib `Conn` implements `driver.NamedValueChecker` and takes a Go slice,
converting it through pgx's type map. `cmd/api/imports_test.go` forbids
IMPORTING anything under `jackc/pgx` outside `cmd/api/main.go`, and passing a
slice imports nothing — so the sweep never fires and `= ANY($2)` is available.
R6's `internal/postgres/place_store.go` uses it, which is what makes PERF-MIN-8
implementable as written: one statement for a whole visits array rather than
one round trip per occasion inside the advisory lock.

**What this does not say is that the R4 draft would have passed.** Nobody has
re-run it, and `unnest` as an INSERT SOURCE across fourteen columns is a
different shape from `= ANY` in a WHERE. What is established is that the REASON
recorded here does not hold, so the next worker should measure rather than
inherit it.

### DEC-92 — THE ARC CAN NO LONGER DESTROY THE LIVE VOLUME, AND THE GUARD IS RUN RATHER THAN GREPPED

`scripts/slice-arc.sh` step A0 was a bare `docker compose down -v` against the
default project, with no prompt, no `--yes` and no guard variable in 700+
lines. Two of its five phases already ran under their own
`COMPOSE_PROJECT_NAME`, so the **main** phase using the live project was the
inconsistency rather than the pattern.

- **The project name is read from compose, never from the variable.** Compose
  resolves it from `COMPOSE_PROJECT_NAME`, from a `name:` key, or from the
  directory, and only compose knows which won. `deploy/docker-compose.yml`
  carries `name: travellog`, so a guard reading the variable would let a bare
  `make slice` straight through.
- **`SLICE_DESTROY_VOLUME=1` is the way to mean it.** A variable rather than a
  prompt, because the arc runs unattended; required rather than defaulted,
  because what is on the other side is a record with no second copy.
- **Two arc assertions that invoke it.** `grep -n 'SLICE_DESTROY_VOLUME'` —
  which is the plan's own acceptance line — passes against a variable nothing
  consults. R4 runs the arc phase under the live project, asserts **exit 1**,
  and then asserts `travellog_pgdata` is **still there**, which is the half a
  grep cannot make. Safe to invoke, because the refusal is the statement
  *before* `down -v`. R5 is the other direction, so "refuse always" fails too.

### THE REORDER IS RIGHT AND THE FIVE LINES ARE STILL NOT A SCRIPT

DEC-92 reorders R4..R8's acceptance checks to `make check && make slice &&
make seed`, and `scripts/check-plan.py` enforces it. **Run verbatim in one
compose project, the new order cannot produce a successful seed**: the arc
registers `arc@travellog.test` at A3 — which is the basis of everything it
asserts afterwards — so the seed meets DEC-97's predicate and refuses. Measured,
in that order, in one project: `check=0, slice=0, seed=2, seed=2, go test=0`.

That is the guard working rather than a defect, and the answer is the one DEC-92
already gives: **the two commands belong to two stacks.** `make slice` under its
own `COMPOSE_PROJECT_NAME`, `make seed` against the one holding the log. The
reorder fixes the sentence it was written for — the documented procedure no
longer teaches "seed, then wipe" — and it does not make the five lines runnable
top to bottom. `scripts/slice-arc.sh`'s A23 says so where somebody will be
standing.

**A23 IS ALSO THE ONLY PLACE THE COMMAND ITSELF IS EXERCISED.** `internal/seed`'s
legs are about `FromDocument` and `Load`; A23 is the binary, its flags, its
refusal, its exit code and its unchanged row count, against a real database in a
running stack.

### THE BACKUP HAS BEEN RESTORED, AND THE PROOF IS NOT A ROW COUNT

`make backup` is `pg_dump -Fc` **inside the container**, so client and server
are the same build by construction — a host `pg_dump` older than the server
refuses outright, which is a failure that only appears on somebody else's
machine. Keep 7. It deletes an empty output rather than leaving a file in
`backups/` that looks like a backup.

One rehearsal, run at `c3699fd`, with two proofs and a negative control:
every table's **content digest** matched across all twelve, and the restored
copy read through `postgres.LogbookStore` and `logbook.Emit` emitted
**95,577 bytes, sha256 16771d3d…** — the same bytes the running server serves.
Changing one caption in the restored copy moved both. Full output in
`docs/EVIDENCE.md`.

**AND THE FIRST DRAFT OF THAT COMPARISON PASSED VACUOUSLY**, which is the most
useful thing in the rehearsal. It built the digests with `RAISE NOTICE` inside
a `DO` block, whose output the capture missed — so `diff` compared two empty
files and printed *"IDENTICAL: every table's content digest matches"*. The
comparison now refuses to report anything unless it covers exactly twelve
tables. Rule 9, arriving in the shape it actually arrives in: not a mutation
that failed to redden, but a **check that passed while measuring nothing**.

**WHEN THE BUCKET IS BACKED UP, THE ORDER IS FIXED: BUCKET FIRST, DATABASE
SECOND.** A dump newer than the bucket copy references objects that were never
copied; a bucket copy newer than the dump only leaves unreferenced garbage. It
is written in the `make backup` recipe rather than only here, because that is
where somebody adding the second half will be reading.

### DEC-70's HONEST LIMIT, AND ITS PREMISE IS FALSE WHILE ITS CONCLUSION HOLDS

Measured at 284 rows, reproduced twice, during a full trip cascade: **the three
indexes DEC-63 mandates on `photos` were chosen ZERO times**, and the one the
planner did reach for is `photos_asset_idx`, **six times** — an index with
nothing to do with a trip, which leads on `traveller_id` and at one traveller
is enough. That is OE-13's argument concretely: an assertion of the form "an
index was used" cannot tell the right index from the wrong one, so **the
catalog leg is the sole load-bearing proof**. None of the zeroes is a reason to
delete an index; they are the correct answer at this size and the wrong one the
day the log grows.

DEC-70 also says RI checks *"NEVER appear in EXPLAIN, not even under ANALYZE"*.
Executed, a trip deletion prints **six trigger lines with timings**, by
constraint name — five of the trip's own cascades and one `photos_visit_fk`
firing as its visits go. The sentence is false; the conclusion it was used to
support — that you cannot assert the plan changed, because the plan inside the
trigger is not shown — still stands.

### THE JSON MONOPOLY BECAME A NAMED LIST, WHICH IS THE THIRD TIME

`make seed` has to read the captured fixture off disk, and `DecodeJSON` cannot
do it: it takes an `http.ResponseWriter` and a `*http.Request`, because
`MaxBytesReader` needs both. Spec L19 says "exclusively use `encoding/json` for
payload encoding and decoding", which is a **mandate to use that package**
rather than a confinement to one file; the confinement is this project's own
mechanism.

internal/config's environment sweep was a count until VS4 and cmd/api's pgx
sweep was a count until VS4; both went red against correct work and both became
a named list with a reason per entry. This is the same correction, and the
property it buys is the same: a third entry has to be added and argue for
itself.

### Divergences from the step's file list, each deliberate

- **`internal/logbook/rewrite.go` carries `DecodeEnvelope` too**, rather than a
  separate decode file. They are the two halves of "read a captured document
  in", and splitting them would put one exemption on the JSON list against two
  files.
- **`media.FixtureDigests` was not written.** It is a table of literals in the
  object-storage package, and the digests are computed from the bytes instead —
  see the mapping section above. `internal/media` also has no business knowing
  what a Flutter bundle path is.
- **`FromDocument` takes the traveller row and the media objects**, not a bare
  `travellerID`. DEC-97 generates the passphrase per run, so the credential
  cannot be invented inside a mapping function, and
  `travellers_passphrase_hash_present_ck` refuses an empty one five statements
  before anything interesting happens.
- **`LoadOptions` lost `Force` and `Reset`** rather than implementing them.
  Force is a switch whose only use is to defeat the guard that is now the point
  of the function; Reset is `DROP` wearing a friendlier word, against the one
  volume that holds the record. The dev-database marker that does gate it lives
  in `cmd/seed`, where an operator types it.
- **`cmd/api` must not reach `internal/seed`, and that is a mechanism now.** It
  was a sentence in the definition of done with nothing behind it, because
  until R4 there was no command to import. It walks the import graph
  **transitively** — the way the rule actually breaks is a helper in
  `internal/postgres` reaching for a seed constant — and carries a positive
  control in the same run, because a graph walk that found nothing would pass
  just as well.
- **`internal/seed/load.go` is on the transaction allowlist.** It CREATES the
  traveller the advisory lock would be keyed on, which is the argument
  register's predicted exemption rested on; it writes ten tables where the
  helpers wrap one; and it refuses to run when any traveller exists, so the
  concurrency the lock orders is a state it cannot be in.

### What R4 leaves guarded by nothing

- **The backup is not off-box, not scheduled, and does not include the
  BUCKET.** `make backup` writes into `backups/` on the same machine as the
  volume it protects, and nothing runs it. A database restore without a bucket
  restore is a log every reference of which resolves, pointing at nothing:
  284 photographs and 24 covers — 308 references — addressing two objects
  that are not there.
  DEF-07 owns the media half; `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries the
  trigger.
- **`make backup`'s ROTATION beyond one file.** Keep-7 is written and the
  rehearsal produced one dump, so the `tail -n +8` branch has been read and not
  executed. A human with eight days.
- **`make seed`'s SUCCESS PATH.** The arc's A23 runs the command and proves the
  REFUSAL — exit code, message, traveller, database, unchanged row count. **The
  loading half is guarded by having been run by hand**, with the output in
  `docs/EVIDENCE.md`, and it cannot be added to the arc as it stands: the arc
  registers a traveller at A3, so a successful seed and the arc are mutually
  exclusive in one project by construction. The generated passphrase, the two
  uploads and the 412 branch are all on that side of the line.
- **`-skip-media`.** It writes the `media_objects` rows without the bytes,
  which is a log whose covers all 404 at mint. Nothing asserts what it does and
  nothing passes it; saying so is the whole of its guard.
- **The generated passphrase's LENGTH and alphabet.** Both are constants with
  their derivation in the comment, and both are self-consistent by
  construction. Same hole R1 recorded for `REQUEST_TIMEOUT`, R2 for
  `S3_PRESIGN_TTL_PUBLIC` and R3 for `MEDIA_MAX_BYTES` — **now six variables
  wide.**
- **Nothing still reclaims a media object** (OE-12), unchanged from R2 and R3,
  and R4 adds a fourth way to make one: a seed that fails between the upload
  and the commit leaves two objects in the bucket with no row.
- **The plan's `docs/CLIENT-PREREQUISITES.md` claim about `shareLinkId`.** The
  seed writes the captured plaintext token into `share_links.token`, which is
  the LAST release in which that is possible: R5's migration 0004 replaces the
  column with a sha256 (DEC-85). Nothing here checks that R5 migrates the rows
  this step wrote.

### Commands, not numbers

```bash
# the legs, and what the database variable buys
                       go test ./... -count=1 -v | grep -c -- '--- PASS'   # 566
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'    # 730

# the seed, and the refusal — the second run must be non-zero and change nothing
COMPOSE_PROJECT_NAME=travellog-r4 POSTGRES_PORT=5464 MINIO_PORT=9005 \
  API_PORT=8085 S3_PUBLIC_BASE_URL=http://127.0.0.1:9005 make seed

# the backup, and what is in backups/
make backup && ls -lh backups/

# the arc, under its own project, so the live volume is untouched
COMPOSE_PROJECT_NAME=travellog-r4arc API_PORT=8086 POSTGRES_PORT=5465 \
  MINIO_PORT=9006 S3_PUBLIC_BASE_URL=http://127.0.0.1:9006 make slice

# the guard itself — it must refuse, and the volume must survive being asked
COMPOSE_PROJECT_NAME=travellog scripts/slice-arc.sh arc; echo "exit=$?"
docker volume inspect travellog_pgdata >/dev/null && echo present

# which indexes a full trip cascade actually chooses at 284 rows
psql "$DSN" -c "SELECT pg_stat_reset();"
psql "$DSN" -c "BEGIN; DELETE FROM trips WHERE traveller_id='<tid>' AND id='autumn-crossing'; ROLLBACK;"
psql "$DSN" -c "SELECT relname, indexrelname, idx_scan FROM pg_stat_user_indexes
                WHERE schemaname='public' ORDER BY idx_scan DESC, indexrelname;"
```

