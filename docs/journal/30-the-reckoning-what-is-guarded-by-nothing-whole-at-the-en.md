## THE RECKONING: what is guarded by nothing, whole, at the end of the slice

Every step from VS2 has ended with such a list and nobody has ever read them
together. This is all of them in one place, with a verdict on each. **The
sections above are not edited** — a closed entry keeps its own step's wording
and is struck here, because a record that rewrites itself is a record nobody
can date.

### CLOSED SINCE THEY WERE WRITTEN

| entry | step | closed by |
|---|---|---|
| `deploy/.env.example` against `config.Load`'s list | VS2 | `internal/config/deploy_files_test.go`, `d5be39c` |
| the gate's parse-error class, the healthchecks, `make test-db`'s derivation, paths named in comments | VS3/VS4 | `slice-arc.sh`'s `gate`, `healthcheck`, `testdb`, `record` phases |
| `internal/seed` has no test files | VS6 | R4 |
| the four unimplemented lists have no round trip through storage | VS7 | R4's `make seed` and the whole-log round trip |
| the route table's `Mutating` flag is read by no lib code | VS7 | deleted with the field (OE-10, R3) |
| D3's "pins are kept" row, in the container | R5 | R6's A29 |
| **`docs/PUBLIC-ENVELOPE.md` — "guarded by somebody reading it"** | **R5** | **R8. The allowlist is typed into a leg and asserted three ways: the response, the golden, and the specification.** |

### STILL OPEN, AND THE LARGEST CLASS IS ONE CLASS

**TWENTY-FIVE SHIPPED DEFAULT VALUES ARE PINNED BY NOTHING, AND THAT NUMBER
HAS NEVER BEEN STATED WHOLE.** VS8-SEC recorded it for two variables, R1 made
it three, R2 made it four and said so, R3 added `MEDIA_MAX_BYTES`'s deployed
value, and nobody ever counted the class. Derived rather than remembered:

```bash
grep -oE '\$\{[A-Z_0-9]+:-[^}]*\}' deploy/docker-compose.yml | sort -u | wc -l   # 25
```

Every `${VAR:-default}` in the file that actually starts the stack, deduplicated
because five of them appear on two services. What they are:

| kind | how many | examples |
|---|---|---|
| configuration the binary reads | **14** | `REQUEST_TIMEOUT`, both presign lifetimes, all three rate ceilings, `MEDIA_MAX_BYTES` |
| credentials shared by two services | **5** | `POSTGRES_PASSWORD`, `MINIO_ROOT_PASSWORD` |
| container memory ceilings (DEC-95) | **3** | `API_MEM_LIMIT` |
| published host ports | **3** | `API_PORT` |

`config.Load` bounds many of the first fourteen and
`internal/config/deploy_files_test.go` asserts every variable the package reads
is PRESENT on the api service and documented in the template. **No leg asserts
what any of the twenty-five IS.**

**Measured rather than reasoned, at this working tree.** Three were mutated at
once — the public revocation window from 15 minutes to **seven days**, the
public read's ceiling to **1/min**, and the api's memory ceiling to **16m** —
and:

```
make check exit=0, 0 FAIL lines
```

A seven-day presign default makes four sentences of client copy a lie and the
gate is green. That is the tier this whole class lives in, and the only thing
standing in it is `deploy/.env.example`, which is prose.

The rest, by kind:

- **CONSTANTS ASSERTED THROUGH THEMSELVES — now seven.** `migrateLockTimeout`,
  `IdleInTransactionTimeout`, `TouchInterval`, `MinShareTokenBytes`,
  `MaxNoteBytes`, `MaxCaptionBytes`, `maxVisitsPerStatement`. Every leg reads
  the constant, so each is self-consistent by construction and no number is
  defended by anything. (R8 adds none: the public read introduced no constant
  of its own — its one number, `PUBLIC_RATE_LIMIT_PER_MIN`, is configuration
  and joins the twenty-five above.)
- **NOTHING RECLAIMS A MEDIA OBJECT** (OE-12), and nothing backs the bucket up.
  Recorded at R2, R3 and R4 and still true: a successful upload that is never
  committed stays in the bucket for ever, and a database restore without a
  bucket restore is 308 references that all resolve, pointing at nothing, with
  no error anywhere. `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries both.
- **THE LIMITER'S KEY IS `RemoteAddr`.** Recorded at VS8-SEC and now
  load-bearing in a third place: R8's public ceiling is per address, so behind
  a proxy it becomes one bucket for the whole internet. The honest sentence is
  in `internal/config/config.go`, in `deploy/.env.example` and in
  `internal/httpx/ratelimit.go`. **Caddy is deferred a second time** and it is
  item 1 of `next_slice`.
- **REAL S3.** Every S3 code asserted anywhere here is MinIO's.
- **`-race` is not in `make check`**, and the image has only ever run on arm64.
- **THE CLIENT'S HALF OF EVERY CONTRACT.** `docs/CLIENT-PREREQUISITES.md` is
  guarded by somebody in the other repository reading it. R6 recorded it for
  `visits`, R7 for the caption, and R8 adds two more — the fifteen-minute wall
  and `log_image.dart`'s silent plate.
- **THE THREE THINGS ONLY A HUMAN WITH A DEVICE CAN CHECK**:
  `make backup`'s rotation beyond one file, `make seed`'s success path inside
  the arc (impossible by construction — the arc registers a traveller, so a
  successful seed and the arc are mutually exclusive in one project), and the
  `/dev/tty` half of the image tier's skip notice.

### What R8 leaves guarded by nothing

- **THE ENVELOPE AGAINST A REAL READER.** There is no share PAGE — `next_slice`
  says so — so nothing has ever rendered this document. Whether fifteen minutes
  is long enough to read a trip, and what a browser does with a `no-store`
  response full of cross-origin image URLs, are questions a human with a
  browser answers and nothing here does.
- **THE TIMING HALF OF PD-12.** The work is COUNTED and the counts are equal;
  nothing measures the two paths on a clock. That is deliberate — a timing
  assertion on a shared runner is a flake — but it means a future difference
  that costs no store call and no mint (a log line, a different error path)
  would not be seen.
- **`PUBLIC_RATE_LIMIT_PER_MIN`'s VALUE, and its BINDING.** 120 is untuned, it
  is one of the twenty-five above, and behind a proxy it does not bind at all.
- **THE ENVELOPE'S SIZE — MEASURED ONCE, PINNED BY NOTHING.** The fixture-scale
  legs read the SOURCE rows and no leg looks at the bytes. Measured by hand
  against the seeded stack, `GET /l/kyoto-9f2a` on `autumn-crossing`:

  ```
  61,503 bytes   96 photographs, 5 places, 5 cities, 1 walk
  41,664 chars   the photographs' URLs
     434 chars   the cover's URL — STO-MIN-12 predicted 394
  ```

  **Sixty-eight per cent of the public envelope is presigned URLs**, which is
  the number to reach for when somebody asks whether this route needs its own
  gzip leg. Nothing asserts any of it, and the numbers move with the signer.
- **THE `Vary: Accept-Encoding` ON THIS ROUTE AGAINST A REAL CACHE.** Same as
  R1's entry, and sharper here because this is the one response an
  intermediary is allowed to see at all.
- **A SECOND TRAVELLER.** Every leg in this step is one traveller's log. The
  digest index is global and the row carries the traveller, so a token
  resolving to somebody else's trip is refused by construction rather than by a
  leg — and DEC-86 closes registration after the first, so a second traveller
  cannot exist to test it against.

### Commands, not numbers

```bash
# the legs, and what the database variable buys, at this commit
env -u TEST_DATABASE_URL go test ./... -count=1 -v | grep -c -- '--- PASS'  # 713
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'     # 972

# the counts this step moved, each re-derived rather than incremented.
# THE ROUTE GREP IS ANCHORED — the unanchored form R6 used answers one too
# many, because the sentence documenting it matches.
grep -cE '^\t\t\{http\.Method' internal/httpapi/routes.go          # 22 (+ /healthz = 23)
ls migrations/*.up.sql | wc -l                                    # 4 migrations
grep -cE '^\tCode[A-Za-z]+ +Code = ' internal/httpx/errors.go      # 13 codes
grep -cE '^\s*assert_(eq|contains) ' scripts/slice-arc.sh          # 284, was 230

# the shipped default values no leg pins — the whole class, in one command
grep -oE '\$\{[A-Z_0-9]+:-[^}]*\}' deploy/docker-compose.yml | sort -u | wc -l  # 25

# the arc, under its own project, so a live volume is untouched
COMPOSE_PROJECT_NAME=travellog-r8arc API_PORT=8091 POSTGRES_PORT=5471 \
  MINIO_PORT=9011 S3_PUBLIC_BASE_URL=http://127.0.0.1:9011 make slice

# the seed, on a SECOND stack, because the arc ends holding a traveller
COMPOSE_PROJECT_NAME=travellog-r8seed API_PORT=8092 POSTGRES_PORT=5472 \
  MINIO_PORT=9012 S3_PUBLIC_BASE_URL=http://127.0.0.1:9012 make seed

# the public read against the seeded log, which is the acceptance check's own
# jq line. The seeded token is the CLIENT's — ten characters with a hyphen —
# and that is why the handler does not validate what it is handed.
curl -sS http://127.0.0.1:8092/l/kyoto-9f2a |
  jq '[.places[] | select(.id=="fushimi-inari") | .days | length]'   # [1], not [28]

# the counts the DoD asks for and this file has not carried before
psql "$SEED_DSN" -tAc "select count(*) from information_schema.tables
                       where table_schema='public'"                 # 12 tables
psql "$SEED_DSN" -tAc "select count(*) from pg_constraint where contype='f'"  # 26 keys

# the standing guard, on the seeded log
psql "$SEED_DSN" -f scripts/no_dangling_references.sql   # six zeroes, and 95

# the fixture numbers the whole row-rule argument rests on
psql "$SEED_DSN" -c "SELECT count(*), count(DISTINCT trip_id) FROM visits
                     WHERE place_id='fushimi-inari'"                # 28 | 4
psql "$SEED_DSN" -c "SELECT count(*) FROM photos
                     WHERE trip_id='autumn-crossing' AND lat IS NOT NULL"  # 31 of 96
```

### THE DEFINITION OF DONE, LINE BY LINE

**R8 is the step where all of it has to be true**, so all forty-one lines were
run rather than read. Three verdicts: **MET**, **MET OTHERWISE** (the thing is
true and something other than what the line names is what makes it true), and
**NOT MET**. Numbering is `definition_of_done`'s own order.

| # | line | verdict |
|---|---|---|
| 1 | `make check` green, leg count re-derived at the final commit | **MET** — 972 with a database, 713 without, both re-derived above |
| 2 | `make slice` green from cold, an arc assertion for EVERY route | **MET** — every phase green, 316 ok lines; R8's route is A41-A46 |
| 3 | compose brings up three healthy services, timeouts under intervals | **MET** — the arc's `healthcheck` phase |
| 4 | `make seed` loads the client's log and REFUSES a non-empty database | **MET** — run on its own stack, exit 0; the refusal is arc A23 |
| 5 | no ordered-child write is a delete-then-insert | **MET** — R6 |
| 6 | a dangling check is not a filing check | **MET** — six zeroes and **95** on the seeded log |
| 7 | the whole-log round trip through PostgreSQL, DECODED and compared | **MET** — R4 |
| 8 | visits come back NEWEST FIRST | **MET** — and R8's `days` inherits it by using the same `ordinal` order rather than re-deriving it |
| 9 | every one of the 18 routes has table-driven httptest legs | **MET OTHERWISE** — the coverage is real and its SHAPE is not one table: four table-driven sweeps run over `Routes()` (mux, ceiling, budget, missing credential) and the happy path and unknown-id asymmetry live in each handler's own file. There is no single 18×3 table and there never was |
| 10 | every bare-entity route goes through an `EmitX` | **MET** — and R8's answer is `EmitPublic`'s own return value, listed in `bareBodies` with that argument |
| 11 | D2's four promises and D3's seven rows, on surviving row counts | **MET** — R5, R6 |
| 12 | a server-side `expectNoDanglingReferences` | **MET** |
| 13 | **the public envelope is filtered by ROW as well as by KEY** | **MET** — two legs at a purpose-built fixture and two at fixture scale, and the pair M6/M7 proves neither can substitute for the other |
| 14 | the checksum is actually signed | **MET** — `grep -rn 'PresignedPutObject\|\.Presign(' internal/media/` still returns nothing |
| 15 | capability responses carry both headers; media refuses a bad type and size | **MET** |
| 16 | 0002 and 0003 forward-only with down siblings; 0001 unedited | **MET** — and **0004 makes it four**, which the line predates |
| 17 | `docs/CLIENT-PREREQUISITES.md` written for the other repository | **MET** — §R8.1–R8.5 added |
| 18 | `docs/PUBLIC-ENVELOPE.md` exists and was written in R5 | **MET**, and R8 is what makes it true rather than aspirational |
| 19 | `docs/EVIDENCE.md` gains one table per step, with the commit | **MET** — R8's names all six |
| 20 | `CLAUDE.md` appended in the SAME COMMIT as the code (DEC-23) | **NOT MET, AND IT HAS NOT BEEN SINCE R1.** This section is one commit at the end of the branch, exactly as R1 through R7 wrote theirs. R1's own record says so in its heading — *"Seven commits and one section"* — so the rule has been broken identically eight times and named each time. It is recorded here as a standing divergence rather than pretended away: the record commit is the last of the branch and the branch is one unit of work |
| 21 | every count in the final record re-derived by a command beside it | **MET** — routes, migrations, error codes, arc assertions, tables (**12**), foreign keys (**26**), leg counts, and the twenty-five default values |
| 22 | the capability never reaches a log, at the four sites | **MET, AND THE COUNT WAS WRONG** — it is FIVE call sites, because `RateLimitBy` writes two lines. Each leg carries a positive control and both directions were run |
| 23 | every capability response carries both headers, by PRESENCE | **MET** — on the 200 **and** on the 404, because the middleware is applied from the table above the handler |
| 24 | the public read has its own limiter and its own number | **MET** — its own INSTANCE, and the honest proxy sentence is in `config.go`, `.env.example` and `ratelimit.go` |
| 25 | revoked and unknown do the same number of store calls, COUNTED | **MET** — `{lookups:1 reads:0 mints:0}` both, with a positive control, plus byte-and-header identity in the container |
| 26 | the content-type allowlist written out in Go and in the schema | **MET** |
| 27 | the signed length fails the way the signed checksum does | **MET** — R2/R3 |
| 28 | 0002 backfills, its down file is not an inverse, the runner sets `lock_timeout` | **MET** — R1 |
| 29 | every `DEC-` resolves; no plan decision claims one | **MET** — `check-plan.py` exits 0 at this tree |
| 30 | absent means leave alone, on every write type | **MET** — R1 for trips, R6 for places, R7 for photographs and walks; R8 adds no write |
| 31 | the count that must not fall | **MET** — **95** on the seeded log, and R8 adds no route that can move it |
| 32 | the begin response tells the client which headers to replay | **MET** — R3, with arc A17 |
| 33 | the bucket exists, proved by a round trip | **MET** — arc A2b |
| 34 | a content address is write-once at the bucket | **MET** — arc A19 |
| 35 | no no-transaction migration ships without a re-runnable proof | **MET** — R1 |
| 36 | a request has a bound, a statement has a bound, a dependency answers 503 | **MET** — R1, and R8's own failure mapping routes a dependency outage the same way |
| 37 | the read is compressed and the response is complete | **MET** — R1. **`GET /l/{token}` is compressed by the same middleware and has no gzip leg of its own**, which is in this step's guarded-by-nothing list |
| 38 | every 500 emits exactly one error line with the requestId | **MET** — `logPublicFailure` is R8's, and `durationUs` is unchanged |
| 39 | the log is backed up and the backup has been restored once | **MET** — R4, with the rehearsal in `docs/EVIDENCE.md` |
| 40 | the emitted trip carries `shared` | **MET** — R1, arc A27 and A41 |
| 41 | CLIENT-PREREQUISITES carries the copy sentences, the error codes and the `LogbookSource` mapping | **MET** |

**One NOT MET (20), one MET OTHERWISE (9), one MET with a corrected number
(22), and thirty-eight met as written.**

