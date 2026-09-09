## R3 — the three media routes, and a reference that waits for the bytes

The third step of plan-v7, and the first that makes the bucket R2 built
**reachable from a phone**. It is also the step that lands a blocker about a
file that does not exist yet.

**Four commits.** The same reading of DEC-23 R1 and R2 took: the record is
written with the code, a step this size as one commit is unreviewable, and a
section per commit would be four sections about one step.

### DEC-99 IS A BLOCKER ABOUT A MIGRATION NOBODY HAS WRITTEN

A `-- migrate:no-transaction` file that fails part way is an **unrecoverable
boot loop**: the runner applies statements 1..i, writes no `schema_migrations`
row, and every later boot re-runs from statement 1 — failing on an
already-applied statement with a **different** error that hides the original
fault, for ever, under DEC-95's `restart: always`. The sharp half is that
`migrate.go`'s own comment has demanded re-runnability since VS4 with nothing
enforcing it: no test, no lint, no acceptance check.

**The twice-run leg does not call `Migrate` twice, and that is the whole
design.** Calling it twice proves nothing — the first run records a ledger row
and the second skips the file entirely. What a half-applied failure leaves is
the statements applied and **no** row, so the leg deletes the row between the
runs. That is the boot loop's exact state, and "re-runnable" means the second
pass over the same statements is clean.

Measured, with `IF NOT EXISTS` taken off the fixture's second statement:

```
statement 2, "CREATE INDEX CONCURRENTLY notx_probe_x_idx ON notx_probe (x)"
(no-transaction, so statements 1..1 are already applied and NO schema_migrations
row was written, which means the next boot re-runs from statement 1):
ERROR: relation "notx_probe_x_idx" already exists (SQLSTATE 42P07)
```

**And the honest part, which must be written or the guard is vacuous:** 0003 is
**entirely transactional** and carries no directive at all, so `migrations/`
contributes **zero** subjects. The leg asserts the subject set is non-empty and
names the testdata fixture as the member that keeps it so — take the fixture
out and it fails on its own precondition rather than passing having run
nothing. The fixture uses `CREATE INDEX CONCURRENTLY`, which is the statement
class the ruling names and the one that leaves an INVALID index behind.

**The declaration is ENFORCED rather than documented, which is stricter than
the ruling asks.** DEC-99 wants the `IF NOT EXISTS` requirement "stated in the
file header and greppable"; `loadMigrations` now **refuses** a no-transaction
file whose header does not carry `-- migrate:re-runnable`, so the refusal
happens at load time and such a file never reaches a database at all. The scan
stops at the first non-comment line, because `splitStatements` attaches a
comment to the statement below it and a marker halfway down is a claim about
one statement rather than about the file.

**The failure message carries the statement TEXT and not only the ordinal**,
and the reason is that the ordinal MOVES between boots — statement 3 on the run
that found the fault, statement 2 on every run after it — so a number alone
reads as one failure wandering rather than two different ones. It is a
`statementSummary` rather than the raw bytes: `splitStatements` keeps a
statement's leading comment block and 0001's comments run to thirty lines,
which would bury the SQL under the prose explaining it.

**The recovery is in `migrate.go`**, where somebody looking at a boot loop
arrives: the psql that reads `schema_migrations`, the `pg_index` query that
finds the INVALID index, the `DROP INDEX CONCURRENTLY` that removes it, and the
one wrong answer named so it is not reached for — recording the version by hand
leaves the schema permanently disagreeing with the ledger.

### THE ALLOWLIST IS TWO VALUES IN TWO PLACES AND ONE LITERAL

0001's own comment calls `media_objects_content_type_present_ck` *"THE WEAKEST
CHECK IN THIS FILE"* and says why: DEC-51's list was named nowhere, so the
constraint that belonged there could not be written. It stopped `''` and
nothing else, and `text/html; <script>` was accepted — which on a bucket
origin, at a URL the public share envelope embeds, is stored XSS reachable by
anyone holding a share link.

0003 replaces it with an IN-list, and **the rename is the claim changing rather
than tidying**: `_present_ck` was true of a check that stopped `''` and is a lie
about one that enumerates two values. 0001's own header warns that a rename
moves a name the catalog legs assert, and `schema_test.go` is in this step's
file list for exactly that.

**`heic` is out (DEC-104) and the asymmetry is the whole answer.** Nothing in
this system can produce a HEIC — the client's shutter is inert by decision,
DEC-41 seeds two PNGs, the fixture's 284 photographs resolve to two `image/png`
objects — while `image/jpeg` earns its place independently of any camera,
because `schema_test.go`'s shared helper seeds with it and removing it breaks
every leg using that helper. One of the three is reachable from the test suite
today and one is reachable from nothing at all. **An allowlist entry nothing
can produce or test is not a free option: it is a claim the schema makes that
no leg can check.**

**One literal, and the regexp is derived from it.** `allowedContentTypes` is
the runtime list; `contentTypePattern` is built from it, so nothing in
`validate.go` spells `^image/(jpeg|png)$` — that string lives in the leg that
asserts what was compiled, which is where it can fail. Two spellings of one set
is how every count in this project has gone wrong.

**Enforced twice on DEC-58's precedent, and the leg reads BOTH rather than
restating either.** `TestTheSchemaAllowlistAndTheGoAllowlistAreTheSameSet`
walks the CHECK's own predicate out of `pg_constraint` and compares the sets in
both directions, then inserts each permitted type. Adding `image/heic` to
either half alone reddens it, with two different sentences naming two different
defects: a 422 the client never sees, and a 422 that passes followed by an
INSERT that raises and reaches the client as a 500 with no field on it.

### THE CONFLICT BRANCH'S `WHERE`, AND WHY `RETURNING` CANNOT COME BACK

Begin is an upsert on a client-minted content address and its conflict branch is
bounded by `WHERE media_objects.uploaded_at IS NULL`. Without it a client
re-beginning an already-**committed** digest rewrites what those bytes ARE —
the database lens turned a committed `(10 | image/png)` row into
`(999999 | text/html)`. **0003's allowlist does not close it**, because any
allowlisted-but-wrong type passes: the leg re-declares a png as a jpeg, which
the CHECK is perfectly happy with.

**The answer comes from a separate SELECT, and the reason is executed rather
than asserted.** v6 deleted the `RETURNING` projection as OE-4 with the reason
"not an xmax trick", which leaves the door open for somebody to put it back as
an ordinary projection. `DO UPDATE … WHERE <false>` emits **zero rows** — the
leg runs the statement with a `RETURNING` clause and counts them, with a
control counting the row an unsuppressed insert does emit — so a handler
reading its answer off `RETURNING` gets nothing on exactly the `alreadyExists`
path, which is the one path the response is about.

### DEC-58's "ENFORCED TWICE" IS LOOSER THAN IT READS, AND R3 IS WHERE IT MATTERS

The four foreign keys guarantee the **row exists** and say nothing about
`uploaded_at`, because an FK cannot see a column it does not reference. So the
schema refuses a reference to an object nobody ever began, and the Go check is
what refuses one that was begun and never uploaded — **two different lies, two
different guards, and only one of them is the database's**. The cover check was
a bare existence check until this step; it is `AND uploaded_at IS NOT NULL` now.

What it costs to get wrong is a photograph that never loads: the reference
resolves, the emitted log carries the locator, the phone mints a read
capability, and the bucket answers `NoSuchKey` — on a screen with no error
state, because DEC-51's read path has none.

**Both halves are in one leg**, because a validator that refuses everything
passes "an uncommitted asset is refused" perfectly. Making it refuse everything
reddens the POSITIVE half and nothing else does. That mutation is M11.

### `uploadHeaders`, AND THE ONE FACT THE CLIENT CANNOT DERIVE

A presigned PUT whose signature covers four extra headers is unusable unless
the caller replays each one exactly, and the checksum's value is **base64**
while the id is **hex** — so a client handed only a URL gets `400 AccessDenied`
on every upload, for ever. The map's values are already encoded server-side and
the client replays them verbatim.

**The leg is EQUALITY in both directions and not a subset**, because a map with
an extra header is as broken as one with a missing one. It runs at two tiers
and they prove different things: the handler leg parses `X-Amz-SignedHeaders`
off a `media.Memory` URL, which does not sign at all and is therefore about
this package's own bookkeeping; the arc's A17 parses it off a URL a **real
SigV4 signer** produced. Measured from outside the test binary:

```
content-length%3Bcontent-type%3Bhost%3Bif-none-match%3Bx-amz-checksum-sha256
uploadHeaders keys: ["content-length","content-type","if-none-match","x-amz-checksum-sha256"]
```

**`expiresAt` is read back off the URL the signer produced**, never computed
from a second copy of the lifetime. That mistake is silent — the client is told
a window the signature does not carry and the upload dies with
`SignatureDoesNotMatch` minutes later — so `media.ExpiresIn` gives it one
source. A URL with no window is an **error** and not a zero, because a zero
would put `expiresAt` at exactly now and read as a capability that has already
closed.

**And an `alreadyExists` response omits `uploadUrl`, `uploadHeaders` and
`expiresAt` entirely.** Handing out a live write capability against a committed
address is worse than a merely redundant one: what stops it landing is
`If-None-Match: *`, which is a property of the SIGNATURE and not of this
response, so an omitted URL is the only thing this response controls.

### `alreadyExists` IS DERIVED FROM `uploaded_at`, NEVER FROM ROW PRESENCE

Derive it from row existence and the FIRST begin reports true — at which point
the client skips an upload that never happened and the commit 409s with no way
forward. The leg proves both directions: false after a begin that never
uploaded (a **row** exists, and that is not what the field means), true after
the commit.

### THE COMMIT IS THE ONLY NON-ATOMIC SEAM IN THE PLAN

Every destructive path in R5, R6 and R7 is a single transaction and cannot
half-apply — the safety lens could not construct a partial cascade. This one
can: the bucket confirms, the database update fails, and the object exists with
`uploaded_at` NULL, which R7's photo route then rejects. **So a second commit
of an already-uploaded object is a 200 and not a 409**, and it is taken BEFORE
the bucket is asked — which is the retry contract doing real work rather than
being polite: a client that lost the response is told yes with no round trip at
all.

**Four checks, and the third is free.** `StatObject` with `Checksum: true`
returns the digest the bucket stored in the same call (DEC-88), and an object
uploaded through either of the two **banned** presign calls carries **no
checksum at all** — so requiring a non-empty matching value turns the ban from
an AST walk into a **runtime guard**. What makes that branch reachable from a
test is `media.Memory.PutWithoutChecksum`, which exists for this and nothing
else; without it the check is a branch no leg can enter and the "load-bearing"
emptiness `Attributes.SHA256`'s comment claims is a claim nothing checks.

### PD-09's TWO FIELDS LAND AND `Mutating` GOES

`Route` gains `NoStore bool` and `Limit`, and Mount applies both **from the
table**. `NoStore` is not derivable from anything else on the row:
`POST /v1/media/mint` carries capabilities and `PUT /v1/trips/{id}` does not,
and both are authenticated writes. A path-prefix guess in middleware would have
given `/v1/media/{id}/commit` — which answers a row and no capability — a
policy nobody chose, and would have missed R8's `/l/{token}` entirely.

**`Limit` is honestly early and it is still not decoration.** On every row in
the table today it is a pure function of `Auth`, which is exactly the shape
`Mutating` was in. The difference is that Mount **reads** it: a wrong value
changes which ceiling a route wears, where a wrong `Mutating` changed nothing
at all. The row that cannot be derived arrives in R8, and the leg that catches
a wrong one today sets the budget the row names to **one** and the other to a
thousand — so a route wired to the wrong bucket meets a ceiling of a thousand
and never 429s.

**And OE-10 STANDS, which is the one-line answer this step owed.** The
deletion's own text said *"R3 must check whether it has acquired a reader; if
it has, this deletion is withdrawn"*. It has not. **The trigger the deletion
named has actually FIRED and still does not save it**, which is the closest a
deleted thing has come to returning: `POST /v1/media/mint` IS the POST that
writes nothing, so `Mutating == (Method != GET)` genuinely stops holding at
this step. A field is real when something reads it, not when it would be
accurate. It comes back the day something asks "does this route change the
log" — an audit line, a cache invalidation, a read-replica router — and it
comes back with that caller in the same commit.

**The header leg asserts presence on the rows that declare the policy and
ABSENCE on the rows that do not**, plus both vacuous directions. The security
lens's finding about v7.0's only header-adjacent leg was that it compared two
answers to each other and would pass with the headers absent from both.

### THE DEFECT RUNNING IT FOUND THAT NO LEG DID

The fake auth store in `internal/httpapi` minted `traveller-1`.
`travellers.id` is a `uuid` column and `media.Address` refuses anything else
outright — because the traveller is a **path segment** in the bucket, so a
value carrying a `/`, a `.` or a `%` would let one traveller's key reach
outside their own prefix. Every media route answered:

```
500 {"code":"internal"}
err="media: \"traveller-1\" is not a traveller uuid, and the traveller is a path segment"
```

No unit test in that package had ever needed a well-formed traveller id, so the
fixture had been wrong since VS6 and nothing could see it. The
traveller-limit log leg that hardcoded that string now reads the id off the
store.

### AND TWO THE RECORD PHASE FOUND IN THIS STEP'S OWN COMMENTS

`scripts/slice-arc.sh record` walks every repo-relative path named in a comment
and asserts it is in the tree. It caught a Go type written as
`internal/logbook.Service` and a brace expansion over two testdata files, both
of which read as paths that do not exist. That check exists because VS1-FIXES
found a Dockerfile comment citing two files that had never existed; it has now
caught its author twice.

### THE ARC, AND WHAT IT PROVES THAT `go test` CANNOT

Seven new steps (A16-A22) plus four assertions on A15. Two things only this
tier can say:

- **The routes are in the running container.** VS6 and VS7 both shipped routes
  green in `go test` and answering 404 in the container.
- **A URL this server minted is a URL a client can use.** The handler legs run
  against `media.Memory`, whose URLs are `memory.invalid` by construction, so
  until R3 nothing in `go test ./...` had ever put a byte through a real SigV4
  signature. Everything about that signature is decided by the HOST it covers,
  and DEC-42's two addresses are two variables that can be swapped with every
  unit test still green.

**A15's ETag literal moved from `W/"2-1"` to `W/"2-2"` in the commit that moved
it**, because A21 writes the trip a second time to give it a cover. That is the
thing R2 found three times that R1 had not done. Its four new assertions are
the only ones in this repository that cross **both halves** of a restart: the
`media_objects` row lives in `pgdata` and the bytes live in `miniodata`, and a
`down` that kept one and lost the other is a reference that resolves and points
at nothing, with no error anywhere.

### THREE THINGS IN THE PLAN THAT TURNED OUT WRONG

- **R3's `heic` acceptance grep cannot pass against correct work.** It says
  `grep -rn 'heic' internal/ migrations/   # -> nothing`. DEC-104's own
  instruction requires the word in a comment ("say in the same place that an
  allowlist entry nothing can produce or test is not a free option"), and the
  legs that prove `image/heic` is REFUSED must name it. Eleven hits, every one
  a comment saying it is out or a leg asserting it is refused. This is the
  plan's own rule 10 exactly — an artefact check that matches its own
  replacement — and R2 hit the same class with the banned presign names. The
  check that means what it meant to mean is: **`heic` in no executable
  position**, which is `grep -n heic migrations/*.sql | grep -v ':[0-9]*:--'`
  and `grep -rn heic internal/ --include='*.go' | grep -v _test.go | grep -v
  '// '` — both empty here.
- **R3's expected `X-Amz-SignedHeaders` is one header short.** The step's
  acceptance check predicts
  `content-type;host;if-none-match;x-amz-checksum-sha256`. The real answer is
  five, with `content-length` — because R2 signs the length too (PD-11,
  DEC-51), which the plan's own R2 text describes and its R3 text did not
  carry forward.
- **`internal/postgres/testdata/notx_fixture.up.sql` is unloadable under that
  name.** `loadMigrations` refuses anything that is not `NNNN_name.up.sql`. The
  bytes live at the path the step names — a numbered file in `testdata` would
  read as a migration to anybody scanning the tree — and `fixtureFS` mounts
  them under a conforming name, one mapping in one place.

### What R3 leaves guarded by nothing

- **`MEDIA_MAX_BYTES`'s DEPLOYED value.** R2 recorded it as "loaded, bounded
  and read by nothing"; the route reads it now and a leg proves a body over the
  bound is refused before anything is minted. Nothing pins **26214400**. Set it
  to `1 << 20` and the suite is green and a 5 MB photograph is refused. Same
  hole R1 recorded for `REQUEST_TIMEOUT`, R2 for `S3_PRESIGN_TTL_PUBLIC` and
  VS8-SEC for the two rate limits — now **five** variables wide.
- **`MaxMintIDs` at 100.** Asserted through the constant, so it is
  self-consistent by construction and the number is defended by nothing. The
  derivation is in the comment; nothing measures it against a real grid.
- **The 201-on-both-paths decision.** Every leg reads `alreadyExists` off the
  body, so changing begin's status to 200 on the conflict path reddens one arc
  assertion and nothing in `go test`. Deliberate — the body is the contract —
  but stated rather than implied.
- **Real S3, still.** Every code asserted is MinIO's. `412 PreconditionFailed`
  on a second PUT is now measured through the whole arc rather than only in
  `internal/media`, and still only against MinIO.
- **The sweep, and anything that reclaims an object.** Unchanged from R2, and
  now with three routes that create them: a begin that is never uploaded leaves
  a row and no bytes, and a successful upload that is never committed leaves
  bytes nothing will ever reference. `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries
  the arithmetic and `next_slice` owns the sweep.

### Commands, not numbers

```bash
# the legs, and the gap the database variable makes
go test ./... -count=1 -v | grep -c -- '--- PASS'
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'
TEST_S3_ENDPOINT=... go test -tags integration ./internal/media/ -count=1 -v \
  | grep -c -- '--- PASS'

# the route table, and what each row declares
grep -c 'http.Method' internal/httpapi/routes.go

# the allowlist, in both places, re-derived rather than remembered
grep -n 'allowedContentTypes =' internal/logbook/validate.go
grep -n "content_type IN" migrations/0003_*.up.sql

# the arc, from cold, under its own project so a live stack is untouched
COMPOSE_PROJECT_NAME=... API_PORT=... POSTGRES_PORT=... MINIO_PORT=... \
  S3_PUBLIC_BASE_URL=http://127.0.0.1:... make slice
grep -c '     ok ' <the run's output>

# DEC-99's guard, and its own precondition
go test ./internal/postgres/ -run NoTransactionMigrationsAreReRunnable -count=1 -v
```

