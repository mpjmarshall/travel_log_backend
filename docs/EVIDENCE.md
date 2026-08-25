# EVIDENCE

Every guard in this repository, and the mutation that was watched to break it.

The plan has asked for this file since v1 and it did not exist until VS8. What
stood in for it was `CLAUDE.md`, step by step, which is where the reasoning
belongs — but a proof scattered across nine sections of a 3,000-line document
cannot be counted, and a count that cannot be re-derived is the thing this
project has already got wrong four times.

**The standard, and it is the project's own:**

1. **A mutation that does not change the file is a green suite proving
   nothing.** Every entry below was diffed before the suite ran.
2. **A mutation that does not compile looks exactly like one that proved
   everything.** Build breaks are recorded as build breaks and never as reds.
3. **Restore by file copy, never `git checkout`.** Against an uncommitted tree
   `git checkout --` no-ops on untracked files and restores tracked ones from
   the index, so mutations stack and a later red belongs to an earlier one.
   VS2 lost a step's implementation that way and contaminated six of thirteen
   results.
4. **A mutation count is evidence only if it was run, at a stated commit.**

---

## Tiers, and why the file is arranged by them

| tier | what runs it | in `make check`? |
|---|---|---|
| **Go** | `go test ./...` | yes |
| **Go, database** | `go test ./internal/postgres/...` with `TEST_DATABASE_URL` | yes, and it **skips and says so** without the variable |
| **Docker image** | `make test-image` (`TRAVELLOG_IMAGE_TESTS=1`) | no — opt-in, needs a daemon |
| **Shell / stack** | `make slice` (`scripts/slice-arc.sh`) | no — opt-in, brings three compose projects up |
| **Artefact** | `make slice`'s `record` phase | no |
| **Nothing** | a human | — |

An **artefact check can only fail when the record is wrong**. Phase 2 of the
parent plan ran seven of them red against correct work. They are written
because a stale record is cheap to catch and expensive to inherit; they are
never counted as evidence about code.

---

## VS8 — mutations run at `cbb467a`, green control at `a7040d5`

Every mutation table below was run at **`cbb467a`**, and `make slice` was run
green end to end at **`a7040d5`** — **exit 0, 76 assertions, 1m26s** on a warm
image cache. The two commits are named separately on purpose: a mutation result
belongs to the tree it was applied to, and the commits between them are the
record corrections the mutations produced.


Go 1.26.5 darwin/arm64, Docker 27.4.0, Compose 2.31.0-desktop.2, PostgreSQL 17.

Every mutation below was applied by `perl -0pi`, its diff printed, the phase
run, and the file restored from a copy taken before it — with `cmp -s` and a
re-computed sha256 checked after each restore. The harness refuses to run a
mutation whose sha256 did not move.

### The arc, `scripts/slice-arc.sh arc`

| # | mutation | leg that went red | actual output |
|---|---|---|---|
| MU-A1 | `deploy/docker-compose.yml`: `pgdata:/var/lib/postgresql/data` → `…/dataX` | **A15**, after the restart | `GET /v1/logbook after restart = 401, want 200` |
| MU-A2 | `logbook_handlers.go`: `WriteJSON(…, logbook.EmitTrip(trip))` → `WriteJSON(…, trip)` | **A8** | `cityIds on the WRITE's answer = null, want []` |
| MU-A3 | `auth_store.go`: `WHERE lower(email) = lower($1)` → `WHERE email = $1` | **A6** | `POST /v1/auth/session = 401, want 201` |
| MU-A4 | `auth_store.go`: `ON CONFLICT (lower(email))` → `ON CONFLICT (email)` | **A4** | `POST /v1/auth/register = 500, want 201` |
| MU-A5 | `0001_init.up.sql`: the unique index on `(lower(email))` → `(email)`, **and** `ON CONFLICT (email)` | **A5** | `register ARC@TRAVELLOG.TEST = 201, want 409` |

**MU-A1 is the one worth reading twice.** It is the `postgres:17` → `18` trap
the VS1 review flagged and nobody could test: the tag bump moves the image's
default `PGDATA` out from under the mount, and the stack comes up **looking
perfectly fine**. Under this mutation every leg from A0 to A13 passed —
register, sign in, the write, the read, the 304, all of it — and A15 answered
**401**, because the sessions table went with the trips. The failure is
invisible until a redeploy, which is exactly why the restart leg is the point
of the whole script.

**MU-A2 is the defect VS7 found by running the binary and no leg caught.**
`Emit` normalises nil slices; the write path answered a bare entity that never
went through it, so `"cityIds":null` reached a client whose `trip.g.dart` reads
`(json['cityIds'] as List<dynamic>)` with no null branch. It is a standing leg
now. Note that the assertion is on the **raw value** and not on
`jq '.cityIds | length'` — `length` is 0 for both `null` and `[]`, so the
obvious form of the leg cannot fail.

**MU-A4 and MU-A5 are two halves of DEC-65 and only MU-A5 proves A5.** MU-A4
reddens A4, not A5: naming an index that does not exist breaks the *first*
register too, so it says nothing about the uppercase one. Isolating A5 needs
the schema and the statement moved **together** — a plain b-tree on `email`
with `ON CONFLICT (email)` — under which registering `arc@travellog.test` and
then `ARC@TRAVELLOG.TEST` both answer **201**. That is the shape of the system
DEC-65 exists to refuse, and A5 is the only thing in the arc that sees it.

### The gate, `scripts/slice-arc.sh gate`

| # | mutation | leg that went red | actual output |
|---|---|---|---|
| MU-G1 | `Makefile`: `gofmt -l . 2>&1` → `2>/dev/null`, **and** `if [ $$st -ne 0 ]` → `if [ 0 -ne 0 ]` — the `ee543b9` pre-state | **G2** | `make check with an unparseable .go file = 0, want 2` |

**And the control that carries VS1-FIXES' lesson, re-measured here.** Under the
same mutation, a repository copy carrying VS1's own mutation — a *parseable*
but misformatted file — still gives `make exit=2`. The unparseable file alone
gives `make exit=0`.

```
MISFORMATTED under MU-G1:                make exit=2
UNPARSEABLE (alone) under MU-G1:         make exit=0
UNPARSEABLE + misformatted under MU-G1:  make exit=2
```

The guard VS1 proved still works, exactly as proved, while the class beside it
walks through. **A guard proven once against one mutation is proven against
that mutation, not against its class.**

### `make test-db`, `scripts/slice-arc.sh testdb`

| # | mutation | leg that went red | actual output |
|---|---|---|---|
| MU-T1 | `Makefile`: the derived `TEST_DATABASE_URL` → the `ee543b9` literal | **T1** | `the printed URL's port does not contain :5999/: export TEST_DATABASE_URL=postgres://travellog:travellog@127.0.0.1:5434/travellog?sslmode=disable` |

T1–T2 are string comparison; **T3 is what makes the phase evidence** — the URL
the target prints opens a session, and it answers `alice@otherdb`.

### The healthcheck, `scripts/slice-arc.sh healthcheck`

| # | mutation | leg that went red | actual output |
|---|---|---|---|
| MU-H1 | `deploy/docker-compose.yml`: postgres `timeout: 2s` → `5s`, against `interval: 3s` | **H3** | `postgres: healthcheck timeout 5000ms is not below interval 3000ms` |

**H1 and H2 need no mutation, because H1 IS one.** The phase runs the
socket-only `pg_isready` first and **fails if it does not disagree**. A poll
that never catches a disagreement proves the poll is too slow just as well as
it proves the fix, and the window on a normal cold start was measured at 0.33s
against a 3s interval — far too narrow to see. Under a 15-second init script:

```
H1  socket-only (the pre-fix recipe):  16 samples, 10 with docker=healthy while TCP=REFUSED
H2  the shipped recipe:                17 samples,  0
```

### The record checks, `scripts/slice-arc.sh record`

| # | mutation | leg that went red | actual output |
|---|---|---|---|
| MU-R1 | a comment naming `internal/httpx/nowhere.go` | **R1** | `a comment names internal/httpx/nowhere.go, which is not in the tree` |
| MU-R2 | `.PHONY` gains a target with no `##` doc line | **R2** | `the ## headings and .PHONY disagree:` / `4a5` / `> ghost` |

R3 needs no mutation: it **is** one. It runs `make slice` twice against stub
scripts, one exiting 0 and one exiting 3, and asserts make answers 0 and 2.

**R1 found two stale citations on its first run, and both were fixed rather
than exempted:** `internal/auth/bearer.go` named `internal/httpapi/middleware.go`
as "the middleware", a file that has never existed — `RequireTraveller` is in
`auth_handlers.go` — and `internal/seed/seed.go` named `cmd/seed` as its entry
point when DEC-75's command does not exist. `deploy/docker-compose.yml` and
`deploy/.env.example` both told a developer to run `go test ./internal/store/...`,
which matches no package.

### Three defects in the arc script itself, each found by running it

Recorded because each is a class rather than a typo, and each would have made a
leg pass while proving the opposite.

1. **A JSON body written inline inside a quoted command substitution is not
   quoted.** `assert_eq 201 "$(req … -d "{\"email\":\"$X\",\"passphrase\":…}")" "…"`
   reaches the shell with the braces bare, **brace-expands**, and runs `curl`
   twice with half an object each. The server answered `400 invalid_body` to
   both and the leg reported one failure with the label `400`. The identical
   text as the right-hand side of an **assignment** parses correctly, which is
   what made it look like a server defect. `jq` builds every body now.
2. **`curl -o` does not create or truncate its output file when the response
   has no body.** The 304 leg therefore measured the *previous* request's
   document — **333 bytes** — so "the 304 answers an empty body" would have
   passed as "the 304 answers the whole log". *An absence assertion is the
   easiest kind to write so that it cannot fail.*
3. **`jqbody() { jq -r "$1" … }`** dropped every flag, so `jqbody -c .logbook.cities`
   compared the whole envelope against `[]`.

**And one in the record phase, which is the same class as (2).** R2's first
draft read the Makefile's headings with `grep -oE '^## [a-z-]+'`, which matches
the *continuation* lines under a heading as well — it compared `normally`,
`without` and `three` against the target list and failed against a correct
Makefile. The em dash is what separates a heading from its prose. That is the
seventh artefact check in this project to go red against correct work.

---

## Inherited — what earlier steps recorded, and what VS8 could verify

**Nine sections of `CLAUDE.md` carry mutation proofs and every one quotes real
output. Three of them state the commit they were run at. Six do not.** That is
recorded as a finding rather than smoothed over: it is precisely the gap this
file exists to close, and the commit column below says `not stated` where it
was not stated. The step's own commit is an *inference*, and an inference is
not a statement.

| step | mutations | legs | commit stated | where |
|---|---|---|---|---|
| VS1 | 1 | 1 | **no** — "the VS1 tree immediately before the VS1 commit" (`6b246a9`) | `CLAUDE.md` "VS1 mutation proof" |
| VS1-BACKFILL | 10 | 11 | **yes** — `6b246a9` plus a named extraction | "VS1-BACKFILL" |
| VS1-IMAGE-TESTS | 12 | 18 | **yes** — `ee543b9` | "VS1-IMAGE-TESTS" |
| VS1-FIXES | 11 findings, 9 shell/Docker legs | — | **yes** — `ee543b9`, on repository copies | "VS1-FIXES" |
| VS2 | 13 | 13 previously-never-red legs | **no** | "The thirteen mutations" |
| VS3 | 54 | 68, **0 never reddened** | **no** | "The mutation harness" |
| VS4 | 71 (68 RED, 3 MISS) | 67 | **no** | "The mutations" |
| VS6 | per-file, 97 legs | 97 | **no** | "The mutations" |
| VS7 | 71 | 73 new | **no** | "Three mutations that survived" |

**Nothing above was re-run wholesale.** Two hundred and forty-three mutations
across five packages is not a step's work, and re-running them without cause
would be theatre. What VS8 did instead is take a **three-mutation sample**, one
from each tier that could regress silently, and re-run it at `cbb467a` against
the current tree. All three reddened, with the text the record quotes.

| sample | of | mutation | actual output at `cbb467a` |
|---|---|---|---|
| S1 | VS2 M-C | `main.go`: `":" + cfg.Port` → `":" + os.Getenv("PORT")` | `files reading the environment = [cmd/api/main.go internal/config/config.go internal/postgres/testdb/testdb.go], want exactly [internal/config/config.go internal/postgres/testdb/testdb.go]` |
| S2 | VS7 L2 | `types.go`: drop `.UTC()` from `Instant.MarshalJSON` | `takenAt = [2027-10-02T19:15:00.000Z], want [2027-10-02T10:15:00.000Z] — a non-UTC instant reached the wire with the wall-clock time of another zone under a Z` |
| S3 | VS4 Q1 (the blocker) | `0001_init.up.sql`: `ON DELETE SET NULL (place_id)` → `ON DELETE SET NULL` | `deleting the place: ERROR: null value in column "traveller_id" of relation "photos" violates not-null constraint (SQLSTATE 23502)` and `photos_place_fk is ON DELETE SET NULL with 0 named columns, want exactly 1` |

S1 is the AST sweep — the guard that a `grep` cannot be. S2 is the survivor
VS7 closed with a leg written for it. S3 is the blocker three review passes
walked past, and the reason the DBA pass exists. Each still names the defect in
its own words.

**One inherited claim VS8 corrected rather than carried.** VS6 recorded
`docker compose build api` stalling for ~15 minutes at
`resolve image config for docker-image://docker.io/docker/dockerfile:1` and
concluded that "egress to `registry-1.docker.io` did not answer here". Measured
on the same machine at VS8:

```
$ curl -o /dev/null -w '%{http_code} in %{time_total}s\n' https://registry-1.docker.io/v2/
401 in 0.168s                       # correct for an unauthenticated request

$ echo 'https://index.docker.io/v1/' | docker-credential-desktop get
(no output; killed at 15s)          exit 143

$ DOCKER_CONFIG=<copy of ~/.docker with credsStore deleted> docker compose build api
… Service api  Built                8.6s
```

Hub was reachable the whole time. What hangs is the **credential helper**, and
the same build finishes in 8.6 seconds without it. `scripts/slice-arc.sh` now
probes the helper with a 10-second deadline before the build and prints all of
the above when it does not answer; on a machine whose helper answers it changes
nothing.

---

## R2 — the media store, every mutation run at `90f6a68`

Twelve mutations, one commit, and the control re-run green after each. The
stack they ran against: MinIO **RELEASE.2025-09-07T16-13-09Z** on 127.0.0.1:9412
(`make test-s3`), Go 1.26.5 darwin/arm64, Docker 27.4.0.

Every mutation's `git diff --stat` was printed before its legs ran, because a
mutation that does not change the file is a green suite proving nothing.
Restoration is `git checkout -- .` and is safe **only** because every file
under mutation was committed first — the tree was clean at `90f6a68` before
each one.

### THE RECORDED INTERMEDIATE RED — the leg failing against a naive implementation

This is the run the whole step exists to justify, and it is worth more than
any mutation below: the checksum leg, against real MinIO, with `PresignPut`
implemented through minio-go's plain presigned-PUT helper. Run at `2758da6`
plus the step's working tree, before the real signer existed:

```
$ TEST_S3_ENDPOINT=http://127.0.0.1:9412 go test -tags integration ./internal/media/ \
    -count=1 -run TestABodyThatDoesNotMatch -v
=== RUN   TestABodyThatDoesNotMatchThePresignedDigestIsRefusedByTheBucket
    minio_integration_test.go:230: the bucket answered 200  for bytes that are
    not the digest the URL was signed for; want XAmzContentChecksumMismatch.
--- FAIL: TestABodyThatDoesNotMatchThePresignedDigestIsRefusedByTheBucket (0.01s)
exit=1
```

**200, and the object stored at the poisoned address.** A leg written after the
correct implementation cannot say that.

### The signature: what each header is holding up

| # | mutation | reddens | stays green |
|---|---|---|---|
| M1 | `content-length` out of the signed set | the LENGTH leg, with `400 XAmzContentChecksumMismatch` where it wanted `SignatureDoesNotMatch`; and the header-map leg | **the CHECKSUM leg** — the pair that proves they are two controls and not one |
| M2 | the banned presigner in place of `PresignHeader` | `ban_test.go` (`minio.go calls the banned presigner at minio.go:209:12`); the omit-the-digest leg, with **200**; the header-map leg (`the signature covers []`) | **the CHECKSUM leg**, and that is the finding — see below |
| M5 | the checksum leg does not replay one signed header | the CHECKSUM leg, with `400 AccessDenied` — the right red for the wrong reason | — |
| M6 | the digest signed as hex rather than base64 | the round trip, with `400 InvalidArgument`; and the unit `Address` leg, three rows | — |
| M8 | `If-None-Match: *` out of the signature | the write-once leg: `the SAME bytes again answered 200, want 412 PreconditionFailed` | the twin's own write-once, which enforces it in Go — a second control |

**M2 IS THE ONE WORTH READING.** The plan predicted that swapping in the banned
presigner would redden the checksum leg. **It does not.** With `host` signed and
nothing else, and the four headers still *sent*, MinIO validates the digest
anyway and answers `XAmzContentChecksumMismatch` — so the leg passes. It
exercises an **honest** client. An attacker omits the header, and with only
`host` in the signature there is nothing left to refuse. `TestAnUploadThatOmits
TheDigestIsRefused` was written for that and is what turns red (200, object
present). Three legs now cover the ban and none replaces another.

### The key, the traveller, and the two lifetimes

| # | mutation | reddens |
|---|---|---|
| M3 | `Address` drops the traveller prefix | the unit cross-traveller leg **and** the repointed-URL leg against real MinIO |
| M4 | the two lifetimes swapped inside `media.New` | the TTL-by-audience leg (`X-Amz-Expires`, read off the signature) |
| M4b | the two lifetimes swapped at the **wiring** site, `cmd/api` | the wiring leg — the other end of the same wire, and not implied by M4 |
| M4c | the two **addresses** swapped at the wiring site | `the signed base = "http://minio:9000", want "https://media.example.test"` |
| M7 | the key and the header derived from two variables | the digest-disagreement leg |

### The bucket, and the one guard that is the arc's alone

| # | mutation | `make check` | `up --wait` | the arc |
|---|---|---|---|---|
| M9 | the `EnsureBucket` call deleted from `run()` | **exit 0 — green** | three services **healthy** | **RED**: `buckets named travellog-media in mc ls local/ = 0, want 1` |

**`make check` stays green and that is stated rather than glossed.**
`cmd/api/media_test.go` guards what `mediaStore` *does* — against a closed port
it must refuse to come up — and cannot see whether `run()` calls it. **A2b is
the only guard on that call site.** It is also the exact fail-open shape DEC-98
describes: presigning is offline arithmetic once the region is pinned, so a URL
minted against a missing bucket is perfectly well-formed and fails on the
phone.

### Three assertions the arc carried out of R1, all red before R2 touched them

Found by running `make slice`, not by reading. Each is a literal in
`scripts/slice-arc.sh` that a shipped change moved:

| step | the arc said | the running stack answers | what moved it |
|---|---|---|---|
| A8/A9/A11/A15 | `W/"1-1"` | `W/"2-1"` | `EmitterVersion` 1 → 2 (DEC-91) |
| A10 | `false\|false\|false` | `true\|true\|false` | migration 0002's DEFAULTs (DEC-82, PD-01) |
| A13 | `not_found` | `unsupported_route` | DEC-103 |

### The `.dockerignore` patterns, verified rather than reasoned

Six canaries written into the tree, the **build stage** built, and its contents
listed from inside the image:

```
absent   .env.local          absent   secrets/s3.txt
absent   server.pem          absent   id_rsa
absent   tls.key             absent   deploy/.env
PRESENT  deploy/.env.example
```

A `!deploy/.env.example` line was written and then **deleted**: nothing
excludes that path — `deploy/.env` matches one exact path and `.env.*` matches
the context root only — so it was an exception excepting nothing. Removing it
and re-running gave the identical table.

### What R2 leaves guarded by nothing

- **The `mem_limit`, `restart` and `logging` VALUES.** The acceptance check
  reads them out of `docker compose config`; nothing asserts `256m` is the
  right number, and no leg reads them at all. Same tier as the iOS manifest
  flags in the client project: a human with a box.
- **`S3_PRESIGN_TTL_PUBLIC`'s DEFAULT value.** `internal/config` bounds it at
  1s..168h and `internal/media` asserts the audience gets the configured one.
  Nothing pins **15m**, which is the number four sentences of client copy are
  written against. Set it to `168h` and the whole suite is green and the copy
  is a lie. This is the same hole R1 recorded for `REQUEST_TIMEOUT` and
  VS8-SEC for the two rate limits, and it is now **four** variables wide.
- **`MEDIA_MAX_BYTES` has no enforcement anywhere yet.** It is loaded, bounded
  and documented; the route that refuses to mint above it is R3's. Nothing
  today reads the value.
- **Real S3.** Every S3 error code asserted in `internal/media` is MinIO's.
  DEC-43's asymmetry cuts the project's way for what matters — a REFUSAL here
  is strong evidence, since a stricter server also refuses — but
  `If-None-Match: *` on S3 is documentation and not a measurement, and so is
  S3's answer to a chunked PUT.
- **`up -d --wait` does not rebuild.** The acceptance check's compose line was
  run once against a stale image left by the M9 mutation and reported three
  healthy services with **zero** buckets. `make up` passes `--build`; the
  plan's line does not. Same class as VS6/VS7's "green in `go test`, 404 in
  the container".

---

## R3 — the three media routes, every mutation run at `7bfacff` + a clean tree

Eighteen mutations, each restored by file copy and each re-run against the
suite. Every "reddens" below is a leg that was **green immediately before and
immediately after** the mutation was applied and reverted; the exact failure
line is quoted where it says something the leg name does not.

### DEC-99 — the no-transaction guard, which is the blocker

| # | mutation | reddens | the line |
|---|---|---|---|
| M1 | `CREATE INDEX CONCURRENTLY IF NOT EXISTS` -> `CREATE INDEX CONCURRENTLY` in the testdata fixture | `TestNoTransactionMigrationsAreReRunnable` | `statement 2, "CREATE INDEX CONCURRENTLY notx_probe_x_idx ON notx_probe (x)" … ERROR: relation "notx_probe_x_idx" already exists (SQLSTATE 42P07)` |
| M2 | the no-transaction failure message drops `statementSummary(s)` | `TestAFailingNoTransactionFileNamesTheStatementTextItDiedOn` | `the failure does not carry "SELECT 1/0", so log line one is not enough` — on BOTH runs |
| M3 | `loadMigrations` stops refusing an undeclared no-transaction file | `TestANoTransactionFileMustDeclareItIsReRunnable` | `a no-transaction migration with no re-runnability declaration was accepted` |
| M4 | the testdata fixture leaves `noTransactionSubjects` | `TestNoTransactionMigrationsAreReRunnable` | `no \`-- migrate:no-transaction\` migration anywhere — this leg has nothing to be about, and a subject set of zero is a green that means nothing` |

**M1 is the measurement the ruling asked for, reproduced here.** The second run
reports `relation … already exists` and NOT the real fault, which is exactly
the boot loop: statements 1..i applied, no `schema_migrations` row, and every
later boot failing on a statement that succeeded the first time. What is new is
that the message now carries the statement TEXT, so the two runs can be told
apart by reading rather than by counting — with the ordinal alone, "statement
3" on boot one and "statement 2" on boot two reads as one failure moving.

**M4 is the honest one.** `migrations/` holds NO no-transaction file — 0003 is
entirely transactional and carries no directive at all — so without the fixture
and the non-empty assertion, this guard is a green that ran nothing.

### The allowlist, and 0003's five bounds

| # | mutation | reddens |
|---|---|---|
| M5 | `image/heic` added to `allowedContentTypes` (Go side alone) | `TestTheSchemaAllowlistAndTheGoAllowlistAreTheSameSet`, `TestTheContentTypeExpressionIsTheOneDEC104Narrowed`, `TestTheAllowlistTakesTwoTypesAndRefusesTheRest` |
| M6 | `image/heic` added to 0003's IN-list (schema side alone) | `TestTheSchemaAllowlistAndTheGoAllowlistAreTheSameSet`: *the schema permits "image/heic" and internal/logbook refuses it — a 422 the client never sees, and a row the schema accepts* |
| M7 | `walks_points_present_ck` deleted from 0003 | `TestTheSchemaRefusesTheDataTheAppForbids/a_walk_with_an_empty_track` |
| M8 | the two `COMMENT ON COLUMN` statements deleted from 0003 | `TestTheTwoGoOnlyIntegrityColumnsCarryTheirRulingInTheCatalog`, both sub-legs |

**M5 and M6 are one mutation applied to each half in turn**, and that pair is
the whole point of enforcing twice. Either alone leaves the two lists
disagreeing, and the two failure sentences name the two different defects: a
422 the client never sees, and a 422 that passes followed by an INSERT that
raises and reaches the client as a 500 with no field on it.

### The store

| # | mutation | reddens |
|---|---|---|
| M9 | `WHERE media_objects.uploaded_at IS NULL` dropped from the conflict branch | `TestReBeginningACommittedObjectCannotRestateWhatItIs` (*the committed row now reads 999999 \| image/png*, and again off disk) **and** `TestTheSuppressedConflictBranchEmitsNoRowAtAll` (*emitted 1 row(s)*) |
| M10 | the cover check goes back to bare existence | `TestATripCannotWearACoverWhoseBytesHaveNotLanded`, `TestPutTripRefusesACoverThatWasNeverUploaded/an_object_begun_and_never_uploaded` |
| M11 | the cover check refuses EVERYTHING (`AND false`) | `TestATripCannotWearACoverWhoseBytesHaveNotLanded`'s **POSITIVE** half, `TestPutTripAcceptsACoverThatHasBeenUploaded` |

**M11 is the one v6 warned about and the only one that proves the leg is not
vacuous.** "An uncommitted asset is refused" is satisfied perfectly by a
validator that refuses everything; M11 is that validator, and it reddens the
positive half and nothing else. M9 reddening TWO legs is not redundancy: one is
about the row's contents and one is about `RETURNING` emitting no row at all,
which is the reason the store does a separate SELECT.

### The routes

| # | mutation | reddens |
|---|---|---|
| M12 | a fifth header signed into the PUT and left out of `uploadHeaders` | `TestTheUploadHeadersAreExactlyTheHeadersTheSignatureCovers`, printing both sets |
| M13 | `Cache-Control: no-store, private` dropped from `CapabilityHeaders` | `TestTheCapabilityHeadersAreOnTheRowsThatDeclareThem`, on `POST /v1/media` and `POST /v1/media/mint` and on neither other row |
| M14 | `alreadyExists` derived from row existence rather than `uploaded_at` | `TestBeginMintsAnUploadCapabilityAndCommitTurnsItIntoAnAsset` (*alreadyExists = true on a first begin*), `TestASecondBeginAnswersAlreadyExistsAndMintsNoSecondUploadURL` |
| M15 | `response-content-disposition` dropped from `PresignGet`, in both implementations | `TestEveryMintedReadURLIsMarkedAsADownload`, on both URLs |
| M16 | the commit stops comparing `got.SHA256` with the address | `TestACommitRefusesAnObjectThatCarriesNoStoredChecksum`: *commit of an object with no stored checksum = 200 … want 409* |
| M17 | the commit stops comparing `got.ContentType` with the row's | `TestACommitRefusesAnObjectStoredAsSomethingElse`: *commit of an object stored as image/png behind a row declaring image/jpeg = 200 … want 409* |
| M18 | the commit stops comparing `got.Size` with the row's | `TestACommitRefusesAnObjectOfADifferentSize`: *commit of 50 bytes behind a row declaring 51 = 200 … want 409* |

**M16 is DEC-98's free half turning the ban into a runtime guard.** An object
uploaded through either banned presign call carries NO stored checksum, so a
commit that requires a non-empty matching value refuses it at the moment it
would otherwise become referenceable. What makes that branch reachable from a
test is `media.Memory.PutWithoutChecksum`, which exists for this and for
nothing else — without it the check is a branch no leg can enter, and the
"load-bearing" emptiness `Attributes.SHA256`'s comment claims is a claim
nothing checks.

### The defect R3 found by running rather than by reading

The fake auth store in `internal/httpapi` minted `traveller-1`. `travellers.id`
is a `uuid` column, and `media.Address` refuses anything that is not one —
because the traveller is a **path segment** in the bucket, so a value that
could carry a `/`, a `.` or a `%` would let one traveller's key reach outside
their own prefix. Every media route answered:

```
500 {"code":"internal"}
err="media: \"traveller-1\" is not a traveller uuid, and the traveller is a path segment"
```

No unit test in this package had ever needed a well-formed traveller id, so the
fixture had been wrong since VS6 and nothing could see it.

### And two the record phase found in R3's own comments

`scripts/slice-arc.sh record` walks every repo-relative path named in a comment
and asserts it is in the tree. It caught a Go type written as
`internal/logbook.Service` and a brace expansion over two testdata files, both
of which read as paths that do not exist. That check exists because VS1-FIXES
found a Dockerfile comment citing two files that had never existed; it has now
caught its author twice.

### The arc, and what it proves that `go test` cannot

`make slice` from cold, under its own `COMPOSE_PROJECT_NAME`, exit 0, **107
assertions** (`grep -c "     ok "` over the run). R2 recorded 80.

Seven new steps, A16-A22, plus four assertions added to A15. The two things
only this tier can say:

- **The routes are in the running container.** VS6 and VS7 both shipped routes
  green in `go test` and answering 404 in the container.
- **A URL this server minted is a URL a client can use.** The handler legs run
  against `media.Memory`, whose URLs are `memory.invalid` by construction, so
  nothing in `go test ./...` had ever put a byte through a real SigV4
  signature. Everything about that signature is decided by the HOST it covers,
  and DEC-42's two addresses are two variables that can be swapped with every
  unit test still green.

A15's four new assertions are the only ones in this repository that cross both
halves of a restart: the `media_objects` row lives in `pgdata` and the bytes
live in `miniodata`, and a `down` that kept one and lost the other is a
reference that resolves and points at nothing, with no error anywhere.

### What R3 leaves guarded by nothing

- **`MEDIA_MAX_BYTES`'s DEPLOYED value.** R2 recorded it as "loaded, bounded
  and read by nothing"; R3's route reads it, and a leg proves a body over the
  bound is refused *before anything is minted*. Nothing pins **26214400**. Set
  it to `1 << 20` and the whole suite is green and a 5 MB photograph is
  refused. Same hole R1 recorded for `REQUEST_TIMEOUT` and R2 for
  `S3_PRESIGN_TTL_PUBLIC` — now **five** variables wide.
- **`MaxMintIDs` at 100.** The bound is asserted through the constant, so it is
  self-consistent by construction and the number is defended by nothing. The
  derivation is in the comment (a presigned URL is 394 characters, so 100 ids
  is roughly 39 kB) and nothing measures it against a real grid.
- **The 201-on-both-paths decision.** Every leg reads `alreadyExists` off the
  body; changing begin's status to 200 on the conflict path reddens exactly one
  assertion in the arc and nothing in `go test`. That is deliberate — the body
  is the contract — but it is stated rather than implied.
- **Nothing, on the four commit checks — and that entry was WRONG when it was
  first written here.** The draft of this section said the content-type check
  "has no mutation proof of its own, because `media.Memory.Put` stores the type
  from the same `Upload` the row was built from". It does not: `Put` stores the
  type it is HANDED, which is what a bucket does, so the twin produces the
  disagreement in one line and M17 and M18 both redden. Two legs and two
  mutations were added rather than the claim being left standing. **Recording
  it because it is this project's named recurring defect in its purest form:**
  a "guarded by nothing" entry written from reading rather than from running,
  which would have made a real guard look absent to the next reader and invited
  its deletion.
- **Real S3, still.** Every code asserted is MinIO's, and `412
  PreconditionFailed` on a second PUT is now measured through the whole arc
  rather than only in `internal/media` — but only against MinIO.

---

## R4 — the seed, the round trip, and the first backup this project has had

Every mutation below was run at `c3699fd` with a clean tree, restored by file
copy, and re-run. The database tier was live throughout:
`TEST_DATABASE_URL=postgres://travellog:travellog@127.0.0.1:5464/travellog?sslmode=disable`,
a postgres 17.11 under `COMPOSE_PROJECT_NAME=travellog-r4` — never the live
stack's.

### The round trip through PostgreSQL

| # | mutation | reddens | stays green |
|---|---|---|---|
| M1 | `readVisitsSQL` loses `ORDER BY place_id, ordinal, id` -> `ORDER BY place_id` | `TestTheClientsOwnLogSurvivesPostgreSQL` **and** `TestVisitsComeBackInTheOrderTheClientWroteThem` | every leg in `internal/logbook` |
| M2 | `readTripCitiesSQL` loses `ORDER BY trip_id, ordinal` -> `ORDER BY trip_id, city_id` | the round trip, `TestATripsCitiesComeBackInTravelOrder`, and `internal/postgres`'s own `TestAnAssembledReadCarriesTheOrderedCityIDs` | the visit-order leg |
| M3 | `FromDocument` assigns visit ordinals in reverse | the round trip **and** the visit-order leg | the trip-cities leg |
| M4 | `cmd/api/main.go` gains `_ "travellog/internal/seed"` | `TestNothingUnderCmdAPIReachesInternalSeed` | everything else |
| M5 | `internal/logbook/rewrite.go`'s import of `encoding/json` (the first draft, before the named list) | `TestOnlyNamedFilesImportTheJSONPackage` | everything else |
| M6 | **`Load`'s predicate becomes the plan's own** — refuse only when a traveller exists AND `photos` is non-empty | `TestLoadRefusesATravellerWhoHasRegisteredAndWrittenNothing`, **and nothing else** | the other ten seed legs, including the refusal leg above |

**M6 IS THE SHARPEST MUTATION IN THIS STEP, because it is the plan's own text.**
R4 scopes the refusal to "a non-empty LOG"; DEC-97 replaces it with "any
traveller row". The mutation puts the plan's wording back and exactly one leg
goes red — the one written for the state the two predicates disagree about: a
traveller who has registered through `AuthStore` (which is how the running
server makes one) and has written nothing. Every other leg in the package stays
green, which is the whole problem: on a real deployment that state lasts from
the owner's registration until their first write, and DEC-86 has closed
registration behind them.

**M1 AND M3 REDDEN THE SAME PAIR, AND THAT IS THE POINT RATHER THAN A
DUPLICATE.** The plan warns that two legs reddening from one mutation is one
assertion wearing two names unless they say different things. These say
different things and each mutation proves it: M1 breaks the READ and M3 breaks
the WRITE, and the ordinal leg names the visit — `place bukchon: visit 0 came
back as v-bukchon-1, the client wrote v-bukchon-0` — while the round trip names
the field, `logbook.places[0].visits[0].at`. Neither could have told you which
half was wrong on its own.

**M2's THIRD RED IS THE INTERESTING ONE.** `TestAnAssembledReadCarriesTheOrderedCityIDs`
was written at VS7 against a two-city trip the test itself inserted. It still
reddens, so it was not vacuous — but it could only ever have been about the one
trip a write route can make, which is what the DoD names as the gap the seed
closes.

### The two things in the plan's own leg sketch that cannot pass against correct work

**IT DIFFS THE TWO DOCUMENTS POSITIONALLY.** The store orders every top-level
list by its own id on purpose, and the captured document is in the order the
client's encoder wrote it — travel order for cities and trips. Measured, by
running the diff both ways in one leg:

```
positional differences: 2013
aligned differences:       0
```

The first reported line is `logbook.cities[0].id: client "kyoto", server
"busan"`. Not one of the 2,013 is a defect. The lists are aligned by id before
the diff and ORDER is asserted by three separate legs instead — which is what
leaves the diff able to say something.

**IT ASSERTS "VISITS COME BACK NEWEST FIRST" AS A RULE.** Two of the captured
document's seventeen places break that rule:

```bash
python3 - <<'EOF'
import json
L=json.load(open('internal/logbook/testdata/client_sample_log.json'))['logbook']
for p in L['places']:
    ats=[v['at'] for v in p['visits']]
    if ats != sorted(ats, reverse=True): print(p['id'], ats)
EOF
# bukchon  ['2027-10-02T10:15:00.000Z', '2027-12-12T08:40:00.000Z']
# nishiki  ['2027-09-18…', '2026-09-18…' x4, '2027-05-13…']
```

The definition of done says the right thing and the sketch does not: *"asserted
against the fixture's own ordering, not against a rule written here"*. What is
load-bearing is that the order the client wrote is the order that comes back.
The leg carries two positive controls — at least two places with more than one
visit, and at least one that does lead with its newest — so it cannot pass by
having nothing to be about.

### THE ONE INPUT THE ROUND TRIP CANNOT CHECK, AND WHAT DOES CHECK IT

The plan's fourth mutation is "point one locator at the other's digest", with
the predicted red being 189 photographs named in the diff. **It does not
redden the round trip, and it cannot.** `RewriteAssets` is applied ONCE and its
output is both the reference and the input to the load, so a wrong mapping
agrees with itself on both sides. Run at `c3699fd`:

```
M4' the test's fixtureMapping points hero-mountain at card-ireland's digest
    -> TestTheClientsOwnLogSurvivesPostgreSQL          PASS
    -> TestFromDocumentRefusesAnAssetWithNoObject      FAIL
```

So the mapping is guarded somewhere else, and in two places rather than one:

- `TestRewriteAssetsLeavesNoBundlePathInTheClientsOwnLog` counts the rows per
  digest — 205 addressing card-ireland (189 photographs + 3 trip covers + 7
  city covers + 6 place covers) and 103 addressing hero-mountain — so a
  collapsed mapping gives one digest 308 and reddens.
- **`cmd/seed` computes the digest from the file's bytes and never writes one
  down.** The address IS the content, so a wrong mapping is bytes whose sha256
  is not the key they were signed for, and DEC-88's signed checksum refuses the
  PUT. It is the same argument `media.Address` rests on, one layer out.

### DEC-92 — the backup, and the arc that can no longer destroy the live volume

| # | mutation | reddens |
|---|---|---|
| M6 | run `scripts/slice-arc.sh arc` under `COMPOSE_PROJECT_NAME=travellog` | the record phase's R4: `the arc's exit code under the live project = 1`, and `travellog_pgdata across the refusal = present` |
| M7 | run it under any other project | R5: `travellog-slice-guardprobe_pgdata is this arc's own volume` — so the guard is not "refuse always" |

**THE GUARD IS RUN, NOT GREPPED, AND THE DIFFERENCE IS MEASURABLE.** A `grep -n
'SLICE_DESTROY_VOLUME' scripts/slice-arc.sh Makefile` — which is the plan's own
acceptance line — passes against a variable nothing consults. R4 invokes the
real arc phase and then asserts the volume is still there, which is the half a
grep cannot make. It is safe to invoke because the refusal is the statement
*before* `down -v` rather than after it.

**THE PROJECT NAME IS READ FROM COMPOSE, NOT FROM THE VARIABLE.** Compose
resolves a project name from `COMPOSE_PROJECT_NAME`, from a `name:` key, or from
the directory, and only compose knows which won. `deploy/docker-compose.yml`
carries `name: travellog`, so the live project is the default whether or not
anybody set anything — a guard reading the variable would pass a bare
`make slice` straight through.

### THE REORDERED ACCEPTANCE CHECK, RUN VERBATIM, IN ONE PROJECT

DEC-92 reorders R4..R8's acceptance checks from `make seed && make check &&
make slice` to `make check && make slice && make seed`, so the documented
procedure stops teaching a developer that seeding and then wiping is normal.
`scripts/check-plan.py` enforces it. **Run verbatim in ONE compose project, the
new order cannot produce a successful seed** — and that is the guard working
rather than a defect:

```
COMPOSE_PROJECT_NAME=travellog-r4acc API_PORT=8087 POSTGRES_PORT=5466 MINIO_PORT=9007

make check                                    exit=0
make slice                                    exit=0   (five phases green)
make seed                                     exit=2   <- refuses
make seed                                     exit=2   <- refuses
go test ./internal/seed/ ./internal/postgres/ exit=0

  make seed: seed: this database already has a traveller — id 2ee2984e…,
  arc@travellog.test.
      the database: postgres://travellog:xxxxx@127.0.0.1:5466/travellog?sslmode=disable
      the traveller: 2ee2984e-e5ce-4014-bd35-30dee48dd158 <arc@travellog.test>
```

**The arc ENDS by registering a traveller** — `arc@travellog.test`, at A3, which
is the whole basis of everything it asserts afterwards. So a seed against the
same project meets DEC-97's predicate and refuses, correctly, every time.

**The two commands belong to two stacks, and DEC-92 already says so**: `make
slice` under its own `COMPOSE_PROJECT_NAME`, `make seed` against the one holding
the log. The reorder fixes the sentence it was written for; it does not make the
five lines a script. `scripts/slice-arc.sh`'s A23 is where that is written down
in the place somebody will be standing.

### THE RESTORE REHEARSAL, RUN ONCE, AT `c3699fd`

A backup nobody has restored is not a backup. DEC-92 asks for one restore
rehearsal with its output recorded here; this is it, it was run rather than
described, and the proof is not a row count.

```
$ make backup
wrote backups/travellog-20260825T071753Z.dump (44441 bytes)
kept: 1 of 7

$ docker exec … psql -U travellog -d postgres -c "CREATE DATABASE rehearsal;"
$ docker exec -i … pg_restore -U travellog -d rehearsal --exit-on-error --verbose < backups/travellog-20260825T071753Z.dump
… pg_restore: creating FK CONSTRAINT "public.walks walks_trip_fk"
restore exit=0
```

**PROOF ONE — every table's CONTENT, not its count.** `md5(string_agg(x::text,
'|' ORDER BY x::text))` per table, run against both databases:

```
                      source                              restored
cities             12  a39a12d461771ad2545e22f481196098   (identical)
media_objects       2  c02cf41492fdf16b3c3454db8c3d9995   (identical)
photos            284  a7c273b31011fb326804ce9a73218e67   (identical)
places             17  a240bc997c58ec3efd668a57edfcdd26   (identical)
schema_migrations   3  ec02848f1a7786e8aae1e07842778cad   (identical)
sessions            1  a8f51ac52d4ed497443948f8cfc2b446   (identical)
share_links         1  142dd6400b242d7a61a0c617a1649a74   (identical)
travellers          1  5a41edbf96030ffa894aac42cab71a9b   (identical)
trip_cities        18  d14f4e1708a5c533ec4d2e339ae16415   (identical)
trips               7  d9533c75a7621e4db79cad1dca43ea61   (identical)
visits             49  d5cbcab797ec1bb2bb64390d86db3016   (identical)
walks               2  c3f36fffd568241c51f43f7846913ad2   (identical)
```

**AND THE FIRST DRAFT OF THAT COMPARISON PASSED VACUOUSLY**, which is worth more
than the result. It built the digests with `RAISE NOTICE` inside a `DO` block,
whose output the capture missed entirely — so `diff` compared two empty files
and printed *"IDENTICAL: every table's content digest matches"*. The comparison
now refuses to report anything unless it covers exactly twelve tables. Rule 9,
in the shape it actually arrives in.

**PROOF TWO — the restored copy is a READABLE LOG through the real code.** A
throwaway program opening each database through `postgres.LogbookStore` and
`logbook.Emit`, which is the same path `GET /v1/logbook` takes:

```
source    traveller=43b6dfca… version=1 bytes=95577 sha256=16771d3dc70b08a8… trips=7 photos=284
restored  traveller=43b6dfca… version=1 bytes=95577 sha256=16771d3dc70b08a8… trips=7 photos=284
```

95,577 is also what the running server answered for the same log, so the
restored copy emits the same bytes the live one serves.

**THE NEGATIVE CONTROL, in the same run**, because a comparison that cannot fail
is not a comparison. One caption changed in the restored copy:

```
UPDATE photos SET caption='not what was dumped' WHERE id='ph-0';
restored  bytes=95549 sha256=c6daa0cd4a3757a9…      (source: 95577 / 16771d3d…)
photos digest 3209e0b39d802808ecb2eba694c20e95      (source: a7c273b31011fb32…)
```

### DEC-70's HONEST LIMIT, MEASURED AT 284 ROWS

DEC-70 withdrew DEC-63's proof method and offered two replacements: (a) a
catalog leg, and (b) `SET enable_seqscan=off`. **OE-13 is right that (b) is
vacuous and R4 does not use it.** What R4 measured instead is which indexes the
planner actually chooses during a full trip cascade, which is a real
reproducible number:

```bash
psql "$DSN" -c "SELECT pg_stat_reset();"
psql "$DSN" -c "BEGIN; DELETE FROM trips WHERE traveller_id='<tid>' AND id='autumn-crossing'; ROLLBACK;"
psql "$DSN" -c "SELECT relname, indexrelname, idx_scan FROM pg_stat_user_indexes
                WHERE schemaname='public' AND idx_scan > 0 ORDER BY 1;"
```

Reproduced twice, identical both times:

```
photos.photos_asset_idx        = 6
share_links.share_links_trip_idx = 1
trip_cities.trip_cities_ordinal_uq = 1
trips.trips_pkey               = 1
visits.visits_trip_idx         = 1
walks.walks_trip_idx           = 1

photos_trip_idx  = 0      <- the index DEC-63 mandates for THIS cascade
photos_place_idx = 0
photos_visit_idx = 0
photos_city_idx  = 0
```

**THE THREE MANDATED INDEXES ON `photos` WERE CHOSEN ZERO TIMES, AND THE ONE
THE PLANNER DID REACH FOR IS `photos_asset_idx` — SIX TIMES.** That index has
nothing to do with a trip; it leads on `traveller_id`, which at one traveller
and 284 rows is enough. This is OE-13's argument made concrete: any index
leading on `traveller_id` serves, so an assertion of the form "an index was
used" cannot tell the right index from the wrong one. **The catalog leg is the
sole load-bearing proof**, and none of these zeroes is a reason to delete an
index — they are the correct answer at this size and the wrong one the day the
log grows.

**AND DEC-70's OWN SENTENCE IS FALSE, though its conclusion holds.** It says RI
checks *"NEVER appear in EXPLAIN, not even under ANALYZE"*. Executed:

```
$ EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) DELETE FROM trips WHERE traveller_id='…' AND id='autumn-crossing';
 Delete on trips (actual time=0.431..0.431 rows=0 loops=1)
   ->  Index Scan using trips_pkey on trips (actual time=0.076..0.077 rows=1 loops=1)
 Trigger for constraint trip_cities_trip_fk on trips:  time=0.745 calls=1
 Trigger for constraint visits_trip_fk on trips:       time=0.589 calls=1
 Trigger for constraint photos_trip_fk on trips:       time=1.747 calls=1
 Trigger for constraint walks_trip_fk on trips:        time=0.783 calls=1
 Trigger for constraint share_links_trip_fk on trips:  time=0.627 calls=1
 Trigger for constraint photos_visit_fk on visits:     time=0.609 calls=5
 Execution Time: 5.664 ms
```

Six trigger lines, five of them the trip's own cascades and one the SET NULL
that fires when its visits go. Which cascades fired and what each cost is
visible; the conclusion DEC-70 drew from its false premise — that you cannot
assert the PLAN changed, because the plan inside the trigger is not shown —
still stands.

### The numbers this step measured, each with the command beside it

| what | value | command |
|---|---|---|
| the emitted log, through the running server | **95,577 bytes** in 0.023 s | `curl -sS -o /dev/null -w '%{size_download}' -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8085/v1/logbook` |
| the same, gzipped | **6,810 bytes** | the same with `-H 'Accept-Encoding: gzip'` |
| the ETag on a seeded traveller's first read | `W/"2-1"` | `curl -sSI …` |
| the ten-table load | **32 ms** | `make seed` prints it |
| the whole schema on disk at fixture scale | **835,584 bytes** (816 kB) | `SELECT sum(pg_total_relation_size(c.oid)) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r'` |
| the custom-format dump | **44,441 bytes** | `make backup` |
| the largest INSERT the load builds | **3,976 parameters** (284 photographs x 14 columns) | arithmetic, in `internal/seed/generate.go` |

**THE SIZE PREMISE, THIRD TIME OF ASKING.** plan-v7 says 99,271 bytes and R1
measured 95,586 through this build. R4 measures **95,577** through the running
server — **9 bytes fewer than R1**, and the nine are accounted for exactly:
`shareLinkId` is `null` here where R1's emit test carried the client's
`"kyoto-9f2a"` (8 bytes), and `shared` is `true` where R1's was `false`
(1 byte). DEC-85 is what makes the difference, and it is the whole of it.

---

## R5 — D3's cascade, the share writes, and two mutations that proved nothing

Run at `9c6b83d` and at this working tree, against postgres:17.11 on
127.0.0.1:5474 and 5475 under `travellog-r5` and `travellog-r5-seed`. The live
stack on 8080/5434 was not written to.

### Every mutation run in this step, with the leg that had to stay green

| mutation | reddens | stays green |
|---|---|---|
| backfill 0004 from `trip_id` instead of `token` | the backfill leg, on the digest | every other 0004 leg |
| keep the `token` column beside `token_hash` | `TestThePlaintextShareTokenColumnIsGone`, naming both columns | the catalog legs |
| base64-decode inside `HashShareToken` | the NEGATIVE half of the hashing leg | its positive half |
| touch `last_used_at` on every request | the granularity leg's FIRST half | its second |
| never touch it | the granularity leg's SECOND half | its first |
| `TouchSession` back inside `WithTravellerLock` | the rule leg AND the behavioural one | the version legs |
| the registration refusal moved after the hash | the Argon2-count leg **alone** | the closure legs |
| the registration refusal deleted | the closure legs in **two** packages | everything else |
| the CRUD reflex, children before the parent | 3 legs: pins 17→12, gamcheon by name, filing 64→16 | the dangling check, **both ways** |
| the share write becomes whole-state | 2 legs: the one-switch leg and the empty-body leg | the reset legs |
| the share existence check always passes | the unknown-trip leg, on `NewShareLink`'s FK violation | the handler's 404 legs |
| **`SET col = DEFAULT` instead of the three literals** | **nothing** | **everything** |
| **the revoke deleted from `NewShareLink`** | **nothing, before a leg was added for it** | **everything** |

### THE TWO THAT PROVED NOTHING, AND WHAT WAS DONE ABOUT EACH

**1. The plan's own share-reset mutation is green.** R5's test strategy names
`SET col = DEFAULT` and predicts it reddens on `sharePhotos` and `shareNotes`.
Run:

```
$ go test ./internal/postgres/ ./internal/httpapi/ -run 'StoppingSharing|StopThen'
ok  	travellog/internal/postgres	0.844s
ok  	travellog/internal/httpapi	0.385s
```

`UPDATE … SET share_photos = DEFAULT` is legal SQL, it reaches the column
default, and after 0002 that default is `true` — the correct answer. DB-MAJ-4
predicted exactly this and its replacement is what reddens:

```
$ # the reset dropped entirely; only share_links is touched
--- FAIL: TestStoppingSharingResetsTheThreeFlagsToTheClientsDefaults
    the flags read [false false true] after the stop, want [true true false].
```

**2. The CRUD reflex is green in one of its two orderings.** Placed AFTER the
trip delete the subquery matches nothing, because the visits have already
cascaded:

```
$ go test ./internal/seed/ -run 'TestD3|AfterTheCascade'
ok  	travellog/internal/seed	1.390s
```

Placed where a CRUD implementation writes it — children before the parent —
it reddens three:

```
--- FAIL: TestD3KeepsEveryPinAndTakesOnlyTheTripsOwnVisits
    places 17 -> 12, want 17 -> 17.
    visits 49 -> 3, want 49 -> 44
--- FAIL: TestD3KeepsThePinWhoseOnlyVisitsWereOnTheDeletedTrip
    gamcheon is gone.
--- FAIL: TestAfterTheCascadeNoPhotographNamesAVisitThatIsGone
    16 surviving photographs still name a place, want 64.
```

**The dangling-reference query answers 0 under both orderings**, which is the
whole reason the filing count sits beside it.

**3. The mint's own revoke was guarded by nothing** until a leg was added for
it. Deleting it left every leg in `share_store_test.go` green, because
stop-then-new revokes first. With
`TestNewLinkOnATripThatIsAlreadySharedKillsTheOldOne` in place:

```
--- FAIL: TestNewLinkOnATripThatIsAlreadySharedKillsTheOldOne
    NewShareLink over a live link: postgres: minting a link for kyoto-in-may:
    ERROR: duplicate key value violates unique constraint "share_links_one_live"
```

### AND THE PLAN'S NAMED FAILING TEST CANNOT SEE ITS OWN MUTATION

`TestStoppingSharingDisarmsTheSwitchesForTheNextLink` is a handler leg over a
fake store; the mutation is in the store. With the reset deleted:
`internal/httpapi` is **ok**, `internal/postgres` is **red**. Both legs are
kept and the second is the guard.

### D3's cascade, measured row by row against the sheet

Through `LogbookStore.DeleteTrip` on the client's own seeded log, counted
before and after — and again end to end through the running container at
127.0.0.1:8096:

| D3's line | before | after |
|---|---|---|
| "N photos and their notes" — deleted | photos 284 | **188** |
| "N recorded walks" — deleted | walks 2 | **1** |
| "N pins in Busan, Kyoto and Seoul" — **kept** | places 17 | **17** |
| the trip's own visits | visits 49 | **44** |
| the itinerary | trip_cities 18 | **13** |
| "The shared link stops working" | share_links 1 | **0** |
| the trip | trips 7 | **6** |
| the cities — never mentioned, never touched | cities 12 | **12** |

```
$ curl -X DELETE …/v1/trips/autumn-crossing | jq '.logbook.places[]|select(.id=="gamcheon")'
{"id":"gamcheon","cityId":"busan","name":"Gamcheon","visits":[]}
```

`gamcheon` is the fixture's ONE place whose every visit was on that trip.

### The 304's round trips, counted rather than timed

`pg_stat_statements` is not preloaded in this compose stack, so the instrument
is `log_statement='all'` between two marker queries:

```
SELECT s.id, s.traveller_id, s.token_hash, …
begin isolation level repeatable read read only
SELECT logbook_version FROM travellers WHERE id = $1::uuid
rollback
-> 4 statements, 0 pg_advisory_xact_lock
```

With `last_used_at` pushed ten minutes back the same request costs **5** — one
extra `UPDATE sessions SET last_used_at`, still no transaction and still no
lock. The performance lens measured **9** and named the five that go; 9 − 5 = 4.

**A `grep 'statement:'` counts 2 of those 4.** pgx uses the extended protocol,
so the parameterised statements log as `execute <unnamed>:`. An instrument that
under-reports by half would have made this look better than it is.

### The acceptance check, run verbatim, with exit codes

```
$ psql … -c "SELECT count(*) FROM photos p LEFT JOIN visits v
             ON (p.traveller_id,p.visit_id)=(v.traveller_id,v.id)
             WHERE p.visit_id IS NOT NULL AND v.id IS NULL"
0
exit=0

$ psql … -c "\d share_links" | grep -c 'token_hash'
4
exit=0
```

**That one cannot pass as written.** `\d` prints the column, the primary key,
the unique index and the CHECK, and every one mentions `token_hash` — correctly.
The narrowed form:

```
$ psql … -tAc "SELECT count(*) FROM information_schema.columns
               WHERE table_name='share_links' AND column_name='token_hash'"
1
exit=0

$ psql … -c "SELECT count(*) FROM share_links WHERE token_hash IS NULL"
0
exit=0

$ curl -o /dev/null -w '%{http_code}' -X POST -d '{"email":"b@c.d","passphrase":"…"}' \
    http://127.0.0.1:8096/v1/auth/register
409
exit=0
$ # and the SAME address, for comparison
409   {"code":"conflict"}   — byte-identical to the stranger's
```

### The arc

`make slice` green from cold under `travellog-r5`, every phase, exit 0. The
seven new steps:

```
--- A5b: POST /v1/auth/register, a DIFFERENT address — closed, and indistinguishable
     ok  the stranger's refusal, byte for byte against the same-address refusal
--- A24: PUT /v1/trips/arc-share            ok  shared = false (DEC-91)
--- A25: PUT …/share, one switch            ok  the other two, not touched
--- A26: DELETE …/share                     ok  true|true|false, read out of the ROW
--- A27: POST …/share                       ok  shareCoordinates = false on the NEW link
                                            ok  rows of share_links holding the plaintext = 0
                                            ok  share_links.token — GONE = 0
--- A28: PATCH /v1/me                       ok  the stored name after a refused write = Matt
--- A29: DELETE /v1/trips/arc-share         ok  the SECOND DELETE = 200, same ETag
--- A30: DELETE /v1/auth/session            ok  the revoked token = 401
```

**What the arc cannot say, and it is stated rather than left as a gap.** D3's
"pins are KEPT" row needs a PLACE, and nothing in this build creates one until
R6's `PUT /v1/places/{id}`. The counts are proved at fixture scale in `go test
./internal/seed/`; what the arc proves is that the route is mounted and answers
the whole envelope. R6 is where this step grows its pin.

## R6 — cities and places, and a named failing test that could not see its own mutation

Run at `1270e8c` and at this working tree, against postgres:17.11 on
127.0.0.1:5476, 5477 and 5478 under `travellog-r6`, `travellog-r6arc` and
`travellog-r6seed`. The live stack on 8080/5434 was not written to.

### Every mutation run in this step

Each restored by FILE COPY, and each checked to have actually changed the file
before the suite ran — a mutation that did not change the file is a green suite
proving nothing.

| mutation | reddens | stays green |
|---|---|---|
| the visits write as **delete-then-insert** | 4 legs in 2 packages: the two no-op re-sends, the reorder, the occupied-occasion guard | every count leg; the dangling check **both ways** |
| the ordinal offset **downward** | 6 legs, on `visits_ordinal_ck` — a red for a DIFFERENT reason | everything else |
| the offset is the plan's **fixed `+ 1000`** | **1 leg**: the 1,100-occasion reorder | all 872 others |
| D2's delete branch **drops the place first** | 2 legs, both on a WHOLE-LOG count | the plan's own assertion (see below) |
| the two branches collapse: photographs **always** go | 2 legs, both keep-branch | the delete-branch legs |
| `?photos` **defaults to keep** | 3 legs in 2 packages | the two-branch legs |
| the attached city is **prepended** | 3 legs | the bare-city legs |
| `trip_cities` read stops being ordered | 4 legs, 3 of them older than R6 | the count legs |
| the **occupied-occasion** guard deleted | 2 legs | everything else |
| the **another-place** guard deleted | 1 leg | everything else |
| the Service's **disposition** guard deleted | 1 leg | every handler leg |
| `visits: []` **accepted** | 3 legs in 3 packages | the omitted-key legs |
| the route answers the **bare `Place`** | 2 legs: the emit leg and the AST sweep | every other leg in the step |

### THE ONE THAT SAID SOMETHING: the plan's own named failing test is GREEN

R6's test strategy writes `TestRemovingAPlaceAndItsPhotographsActuallyRemoves
Them` out in full and names the mutation it exists for. Its only assertion about
the photographs is `countPhotos(t, db, place) != 0`. Transcribed literally and
run against that exact mutation:

```
### CORRECT CODE:       photographs left in the whole log: 0   --- PASS
### UNDER THE MUTATION: photographs left in the whole log: 2   --- PASS
```

With the place deleted first, `photos_place_fk … ON DELETE SET NULL (place_id)`
clears `place_id`, so a count scoped by `place_id` counts **0** while both
photographs are still there. What ships asserts `SELECT count(*) FROM photos`
beside it, and the fixture-scale leg asserts `284 → 254`.

**An assertion scoped by the column the mutation nulls cannot see that
mutation.** That is the general form, and it is the second time this project has
found a plan-named leg blind to its own mutation — R5 found the first.

### THE ONE THE PLAN NAMES THAT CANNOT BE CONSTRUCTED

"Clear `place_id` but not `visit_id` on the keep branch" has no source to
mutate. The keep branch is a single `DELETE FROM places`; both columns are
cleared by foreign keys, and there is no Go statement between them. The
four-field leg still guards the claim — a migration touching `photos_visit_fk`
reddens it — but no source mutation can.

### The visits contract, measured in four states

`fushimi-inari` in the client's own log: **28 occasions, 30 photographs, 3
trips.**

| the body carries | occasions | filings there | whole-log filings |
|---|---|---|---|
| `visits` **omitted** | 28 | 30 | 95 |
| the same 28 **re-sent unchanged** | 28 | 30 | 95 |
| the same 28 **reversed** | 28 | 30 | 95 |
| `visits: []` | 422, 28 | 422, 30 | 95 |
| *(delete-then-insert of the same 28)* | **28** | **0** | **65** |
| *(the empty array, obeyed)* | **0** | **0** | **65** |

The dangling-reference check answers **0** on every row of that table.

### D2's two branches at fixture scale

| | before | `?photos=delete` | `?photos=keep` |
|---|---|---|---|
| photographs | 284 | 254 | 284 |
| places | 17 | 16 | 16 |
| occasions | 49 | 21 | 21 |
| walks | 2 | 2 | 2 |
| cities | 12 | 12 | 12 |
| naming a place | 95 | 65 | 65 |
| captions | 2 | **1** | **2** |

### The acceptance check, run verbatim and reported both ways

```
make check                                          exit=0
make check   (TEST_DATABASE_URL set)                exit=0
make slice   (travellog-r6arc, 8097/5477/9017)      exit=0   196 ok lines
make seed    (travellog-r6seed, 8098/5478/9018)     exit=0
go vet ./...                                        exit=0
python3 scripts/check-plan.py docs/plan-v7.json     exit=0
```

**Two of the three inline lines cannot pass as written, and both are reported
rather than papered over** — this is the plan's own rule 10, landing on the plan
for the fourth step running.

1. `psql -c "SELECT count(*) FROM photos WHERE place_id IS NOT NULL AND
   visit_id IS NULL"` answers **0** before and after, as promised. But the
   sentence beside it — "after re-sending every place unchanged" — cannot be
   performed: **9 of the 17 places are wishlist places**, so the emitted
   document carries `"visits": []` for them and sending it back is the 422 the
   ruling asks for. 8 answered 200; the count is 0 either way.
2. `DELETE FROM cities WHERE id='kyoto'` refuses, and names
   **`trip_cities_city_fk`** rather than the `places_city_fk` the plan predicts:
   the itinerary is checked first. With the itinerary, the photographs and the
   walks cleared inside a transaction that rolls back, it names `places_city_fk`.
   Both were run; the city survives both.
3. The `curl` line passes verbatim — `.visits` is `[]` — **because `tofuku-ji`
   already exists in the seeded log**, so it is an update and `coordinates` is
   leave-alone. Against a genuinely new id the same body is a 422 on
   `coordinates`, which is NOT NULL in the schema.

### A leg older than this step is flaky at a measured 1 in 64

`TestAuthenticateRefusesEveryShapeOfWrongToken` (VS6) builds its
"one character changed" case as `"Z" + issued.Token[1:]`, which is the issued
token whenever the first character is already `Z`. Measured over 64,000
tokens: **1,037 (1.620%)**, against 1/64 = 1.563%. It fired twice inside full
`go test ./...` runs during this step's mutation work and is nothing to do with
R6.

## What is still guarded by nothing

Carried forward so the list does not shorten by silence, with what VS8 moved
out of it.

**Moved to a named leg by VS8:**

- the gate's parse-error class → `slice-arc.sh gate`, G2
- postgres's healthcheck against a real TCP probe → `healthcheck`, H1/H2
- postgres's compose healthcheck budgets → `healthcheck`, H3
- `make test-db`'s derivation → `testdb`, T1–T3
- paths named in comments, and the `slice` target's own exit code → `record`
- the named volume **end to end through the API** → `arc`, A14/A15 (the image
  tier's `TestTheNamedVolumeSurvivesDownAndUp` proves the volume; this proves
  the log)
- `postgres:17` → `18`'s moved `PGDATA` → not asserted, but **caught**: MU-A1
  is that failure exactly, and A15 is what sees it

**Still guarded by nothing:**

- ~~**`deploy/.env.example` and `deploy/docker-compose.yml` against
  `config.Load`'s variable list.**~~ **CLOSED at `d5be39c`** by
  `internal/config/deploy_files_test.go`, and this entry was stale from that
  commit until R2 found it. Both halves are asserted: every variable the
  package reads is set on the api service, and every `${VAR}` compose
  interpolates is documented in the template. R2's nine new variables were
  added test-first through it — the leg went red naming all nine.
- **The four unimplemented lists have no round trip through storage.** `PutTrip`
  writes trips and nothing else, so the six read queries' column ordering, the
  visits nesting and the `jsonb_array_elements` unnest are guarded by their
  scans compiling. `make seed` (DEC-75) is what closes it, and the leg to write
  first when it lands.
- **`ORDER BY id` is asserted for `trip_cities` and nothing else.** The arc's
  log holds one trip, which is all a trip write can make.
- **The 1 MiB body ceiling against a real payload.** The largest body the arc
  sends is one trip. The whole-log **read** has no ceiling at all.
- **The emitter version is a constant a human must remember to bump.** DEC-49's
  stated design, and worth naming as the risk it is.
- **The Argon2 parameters as a CHOICE**, and `DefaultSessionTTL`'s value.
  Both are asserted as numbers; nothing says they are the right numbers.
- **The rate limiter's composition.** `httpx.ClientKey` has legs; nothing
  asserts the limit is per client ADDRESS, and every request in every leg here
  arrives from loopback. The leg that settles it arrives with Caddy and
  `X-Forwarded-For`.
- **`-race` is not in `make check`.** Run
  `go test -race -count=5 ./internal/httpx/` by hand when touching the limiter.
- **The image on `linux/amd64`.** Everything has run on arm64.
- **THE BACKUP IS NOT OFF-BOX, NOT SCHEDULED, AND DOES NOT INCLUDE THE BUCKET.**
  `make backup` writes into `backups/` on the same machine as the volume it is
  protecting, and nothing runs it. A database restore without a bucket restore
  is a log every reference of which resolves, pointing at nothing, with no error
  anywhere: 284 photographs, six trip covers, nine city covers and nine place
  covers — 308 references — all addressing two objects that are not there. `DEF-07` owns the media
  backup and `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries the trigger.
- **`make backup`'s ROTATION, beyond one run.** The restore rehearsal above
  produced exactly one dump, so the recipe's `tail -n +8` branch has been read
  and not executed. A human with eight days.
- **THE SEED'S `-skip-media` FLAG.** It writes the `media_objects` rows without
  the bytes, which is a log whose covers all 404 at mint. Nothing asserts what
  it does, and nothing in `make seed` passes it. It exists so a database-only
  load is possible on a box with no bucket, and saying so is the whole of its
  guard.
- **`make seed`'s SUCCESS PATH.** The arc's A23 runs the command and proves the
  REFUSAL. The LOADING half is guarded by having been run by hand, with the
  output in this file, and it cannot join the arc as it stands: the arc
  registers a traveller at A3, so a successful seed and the arc are mutually
  exclusive in one project by construction. The generated passphrase, the two
  uploads and the 412 branch are all on that side of the line.
- **The `/dev/tty` half of the image tier's skip notice, against a real
  terminal.** A human with a shell.

---

## Running any of this again

```bash
make check                 # the gate                                 (~7s)
make test-db               # prints the export line make check needs for the DB tier
make test-s3               # prints the export line the integration tier needs
make test-image            # the scratch image and the named volume   (~45s warm)
make slice                 # all five phases below, 80 assertions      (~2m)
scripts/slice-arc.sh arc   # one phase; also record, gate, testdb, healthcheck

# the integration tier — real MinIO, behind a build tag AND behind the variable
TEST_S3_ENDPOINT=... go test -tags integration ./internal/media/ -count=1
```

**THE LEG COUNTS ARE RE-DERIVED, NOT CARRIED.** This block said "462 legs" and
was three commits stale before anybody noticed:

```bash
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 872 at R6
                       go test ./... -count=1 -v | grep -c -- '--- PASS'   # 649, no database
TEST_S3_ENDPOINT=...   go test -tags integration ./internal/media/ -count=1 -v \
                         | grep -c -- '--- PASS'                           # 39 = 27 unit + 12 integration
```

The **223-leg** gap between the first two is what `TEST_DATABASE_URL` buys, and
the DB tier **skips and says so** without it. It was 133 at `90f6a68`, R3
measured 154, R4 measured 164, R5 measured 196, and R6 adds 27 more — the two
new stores and the fixture-scale visits and D2 legs, every one of which needs a
real database because a foreign key that fires on a DELETE is not a thing a twin
has. R5's own 32 were the D3 cascade legs, the share store and the two
revocations, against a partial unique index and a column default.

`make slice` **destroys the named volume** — `docker compose down -v` is its
first step, because a 201 against a database that already held the row proves
nothing. Since R2 it destroys **two**: `<project>_pgdata` and
`<project>_miniodata`.

**AND SINCE R4 IT REFUSES TO DO IT TO THE LIVE PROJECT** (DEC-92). Before R4
that volume held nothing anybody would miss; R4 is the step that puts a record
in it. Run it somewhere else, which is one variable — measured green at
`cc6b1ca`, **118 assertions, exit 0**, with `travellog_pgdata` untouched
throughout:

```bash
COMPOSE_PROJECT_NAME=travellog-r4arc API_PORT=8086 POSTGRES_PORT=5465 \
  MINIO_PORT=9006 S3_PUBLIC_BASE_URL=http://127.0.0.1:9006 make slice
```

Or say you mean it: `SLICE_DESTROY_VOLUME=1 make slice`. **Back up first** —
`make backup` is the target, and it is one command.
