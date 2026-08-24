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

- **`deploy/.env.example` and `deploy/docker-compose.yml` against
  `config.Load`'s variable list.** Delete `ARGON2_MAX_CONCURRENT` from the
  compose file and `make check` stays green while the container refuses to
  start. The parent plan's S23 is the test that would close it.
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
make check                 # the gate: 462 legs, 16 skips             (4.4s)
make test-image            # the scratch image and the named volume   (~45s warm)
make slice                 # all five phases below, 76 assertions      (1m26s)
scripts/slice-arc.sh arc   # one phase; also record, gate, testdb, healthcheck
```

`make slice` **destroys the named volume** — `docker compose down -v` is its
first step, because a 201 against a database that already held the row proves
nothing.
