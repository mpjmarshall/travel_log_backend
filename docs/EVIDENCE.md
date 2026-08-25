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
- **`internal/seed` has no test files.**
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
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 625 at 90f6a68
                       go test ./... -count=1 -v | grep -c -- '--- PASS'   # 492, no database
TEST_S3_ENDPOINT=...   go test -tags integration ./internal/media/ -count=1 -v \
                         | grep -c -- '--- PASS'                           # 39 = 27 unit + 12 integration
```

The 133-leg gap between the first two is what `TEST_DATABASE_URL` buys, and
the DB tier **skips and says so** without it.

`make slice` **destroys the named volume** — `docker compose down -v` is its
first step, because a 201 against a database that already held the row proves
nothing. Since R2 it destroys **two**: `<project>_pgdata` and
`<project>_miniodata`. It also runs under whatever `COMPOSE_PROJECT_NAME` is
set, because A14's volume name is now derived rather than written — so it can
be run beside a live stack instead of against it.
