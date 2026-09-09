## VS2 — config, the redactor, the pool, and a /healthz that means something

**TEST-FIRST, not backfill** (agent-graph-spec-V4 §6.7). Every leg below was
written and watched to fail before the code it names existed. VS1-BACKFILL
above is labelled the other way for the opposite reason, and the two labels are
kept apart deliberately: `cmd/api/main_test.go` says BACKFILL in its header and
`cmd/api/healthz_test.go` is a separate file precisely so one label cannot bleed
into the other.

**43 top-level test functions, 60 legs counting subtests** — re-derived, not
remembered: `go test ./cmd/api/ ./internal/config/ ./internal/logging/ -v
-count=1 | grep -c -- '--- PASS'`. **32 of the 43 are new at VS2**; the other
eleven are VS1's backfill, unchanged.

### The beat that made the red worth reading

A test-first Go package fails first as `no non-test Go files` — a build error,
which is the right reason and is nearly useless as evidence: one message for
twenty legs. So each package got a **signature-only skeleton** as its own beat,
and the assertion-level red came off that. It is worth writing down because the
skeleton was not neutral in two of the three cases — it *was* the mutation the
step already names:

- `internal/logging`'s skeleton is the real `slog.NewJSONHandler` with **no
  redactor**, which is VS2's stated mutation proof ("the mutation is deleting
  the redactor") arriving as step 2 rather than step 5.
- `cmd/api`'s skeleton is `newMux(db pinger)` **taking the pinger and ignoring
  it**, still answering a constant — which is VS2's other stated proof ("make
  healthz return a constant and the 503 leg reddens"), and it is also exactly
  what VS1 shipped. VS1's placeholder was written to be this pre-state and it
  worked as intended.

### What the red actually said

Quoted rather than summarised. Every one of these was observed.

**config, against the skeleton** — the leg the step names, and it is the only
leg in the file that can tell "reports a problem" from "reports the problems":

```
--- FAIL: TestLoadReportsAllThreeMissingVariablesAtOnce (0.00s)
    config_test.go:91: Load() = nil error with three variables unset, want an error
--- FAIL: TestLoadNamesEverySevenWhenTheEnvironmentIsEmpty (0.00s)
    config_test.go:115: Load() = nil error with an empty environment, want an error
--- FAIL: TestLoadRejectsInvalidValuesAndNamesTheVariable/max_open_is_zero (0.00s)
    config_test.go:231: Load() = nil error with DB_MAX_OPEN_CONNS="0", want an error
        (database/sql reads 0 as UNLIMITED, which removes the ceiling DEC-21 sizes Argon2 against)
```

**the redactor, against the redactor-free skeleton** — the secret in the clear,
three ways, and the third is the one a naive redactor misses:

```
--- FAIL: TestRedactsAnAttributeAddedByWith (0.00s)
    logging_test.go:204: the secret reached the output through With:
        {"time":"…","level":"INFO","msg":"authenticated","token":"s3kr1t-QZmVsaXg-do-not-log-me"}
--- FAIL: TestRedactsInsideAGroup (0.00s)
    logging_test.go:187: the secret reached the output from inside a group:
        {"…","msg":"request","headers":{"authorization":"Bearer s3kr1t-QZmVsaXg-do-not-log-me"}}
--- FAIL: TestRedactsRegardlessOfTheValueType (0.00s)
    logging_test.go:225: the secret reached the output inside a struct value:
        {"…","token":{"raw":"s3kr1t-…-do-not-log-me","bytes":"czNrcjF0LVFabVZzYVhnLWRvLW5vdC1sb2ctbWU="}}
```

**That third one is the finding, not the ceremony.** A redactor written for
`slog.KindString` would have passed the first two legs and written the struct
out in full — and the JSON handler base64s the `[]byte` field on the way, so
the secret leaves **in two encodings from one attribute**. The fix is to
replace the whole value rather than rewrite a string.

**/healthz, against the constant handler:**

```
--- FAIL: TestHealthzAnswers503WhenTheDatabasePingFails (0.00s)
    healthz_test.go:68: status = 200, want 503
--- FAIL: TestHealthzPingsOnEveryRequest (0.00s)
    healthz_test.go:149: after 1 requests the database was pinged 0 times, want 1
--- FAIL: TestProbeReportsUnhealthyWhenTheDatabaseIsDown (0.00s)
    healthz_test.go:185: probe = 0 against a mux whose database is down, want 1
```

**the two sweeps, before anything satisfied them** — both fail in the
"nothing found" direction, which is the direction a vacuous check cannot:

```
--- FAIL: TestInternalConfigIsTheOnlyPackageThatReadsTheEnvironment (0.00s)
    sweep_test.go:134: files reading the environment = [], want exactly [internal/config/config.go]
--- FAIL: TestPgxIsImportedExactlyOnceBlankAndInMain (0.00s)
    imports_test.go:105: pgx imports = [] (0), want exactly 1
```

### The thirteen mutations, and why there were thirteen

VS1-BACKFILL's lesson was that **eight mutations covering eleven legs looks
like coverage and is not**. So the legs were tracked against the reds rather
than assumed: after the test-first beats, **twelve legs had still never been
red** — they had passed against a skeleton that happened to satisfy them, which
is precisely "a test that passes before the implementation exists is testing
something else". Each got its own mutation. Every one changed the file (byte
counts recorded), every one reddened, and the restore afterwards was verified
**byte-identical by sha256** rather than by assumption.

| # | mutation | leg that went red | what it said |
|---|---|---|---|
| M-A | `idle > open` → `idle >= open` | `TestLoadAcceptsIdleEqualToOpen` | `4 exceeds DB_MAX_OPEN_CONNS=4 … with idle 4 == open 4, want no error` |
| M-B | `DB_MAX_IDLE_CONNS` floor 0 → 1 | `TestLoadAcceptsZeroIdleConnections` | `0 is below the minimum of 1 … want no error` |
| M-C | `":"+cfg.Port` → `":"+os.Getenv("PORT")` | env sweep | `files reading the environment = [cmd/api/main.go internal/config/config.go]` |
| M-D | `NewJSONHandler` → `NewTextHandler` | `TestNewWritesOneJSONObjectPerRecord` | `output is not JSON: invalid character 'i' in literal true` |
| M-E | drop `Level: level` | `TestNewHonoursTheLevel` | `no Debug record was written at LevelDebug` |
| M-F | add `"email"` to `secretKeys` | `TestLeavesOrdinaryKeysAlone` | `email = [redacted], want it untouched` |
| M-G | add `"msg"` to `secretKeys` | `TestLeavesSlogsOwnKeysAlone` | `msg = [redacted], want the message` |
| M-H | `Content-Type` only on the ok branch | `TestHealthzStaysJSONWhenTheDatabaseIsDown` | `Content-Type = "", want "application/json"` |
| M-I | put the driver error in the body | `TestHealthzDoesNotEchoTheDatabaseError` | `{"status":"unavailable: dial tcp 127.0.0.1:5432: connect: connection refused"}` |
| M-J | healthy `StatusOK` → `StatusAccepted` | `TestHealthzStillAnswers200WhenTheDatabaseIsUp` | `status = 202, want 200` |
| M-K | log on the healthy path too | `TestHealthzIsSilentWhenTheDatabaseIsUp` | `a healthy probe wrote a log line` |
| M-L | blank pgx import in `config.go` too | pgx sweep | `pgx imports = [… main.go …][… config.go …] (2), want exactly 1` |
| M-M | `_ "…/stdlib"` → `pgxdriver "…/stdlib"` | pgx sweep | `pgx imported as a NAMED import … spec L20 says solely as a blank import driver` |

**M-L and M-M matter more than their size.** The step's acceptance check is
`grep -rn 'jackc/pgx'` returning one line, and **one line is equally satisfied
by a named import followed by a direct call into the package** — which is what
"solely as a blank import driver" forbids. A grep can see the line and not the
underscore. M-M is the leg that can.

### THE MUTATION-HARNESS INCIDENT, recorded because it destroyed work

The first harness reverted each mutation with `git checkout -- <path>`. Against
a tree where **the implementation is not yet committed**, that is not a revert:

- for the two **untracked** files (`internal/config/config.go`,
  `internal/logging/logging.go`) `git checkout --` matched no pathspec and did
  nothing, so **mutations accumulated** — by M-F the logging package carried
  three at once and the red it produced was M-D's, not M-F's;
- for the one **tracked** file (`cmd/api/main.go`) it restored the file to
  `f6705e6`, i.e. **VS1's placeholder**, deleting the whole VS2 implementation
  mid-run. The five later mutations then failed to match their patterns and
  were skipped, and two more "reddened" on a build error that was the harness's
  doing.

Nothing was lost permanently — the file was rewritten — but **six of the
thirteen results were contaminated and the entire sweep was re-run** with
file-copy snapshots and an explicit byte-difference assertion per mutation. The
rule that comes out of it is narrow and worth keeping: **a mutation harness
must snapshot and restore by file copy, never through git, unless the tree is
committed first.** The project's own standing rule already says a mutation that
does not change the file proves nothing; the twin is that **a revert that does
not restore the file poisons every mutation after it**, and it is the quieter
of the two because the suite still goes red.

### THE OPEN QUESTION VS1 LEFT — ANSWERED, and the plan's guess was wrong

VS1 measured that with **zero dependencies** `go mod edit -go=1.25` left `go
build` and `go vet` both exiting 0, so the `go 1.25.0` literal was a guard with
no teeth and its acceptance check was a pure artefact check. VS2 adds the first
real dependency. Re-run at this commit, Go 1.26.5 darwin/arm64:

```
$ go mod edit -go=1.25 && grep '^go ' go.mod        -> go 1.25
$ go build ./...   ; echo $?  -> 1   go: updates to go.mod needed; to update it: go mod tidy
$ go vet ./...     ; echo $?  -> 1   go: updates to go.mod needed; to update it: go mod tidy
$ go test ./...    ; echo $?  -> 1   (its own status, not a pipeline's)
$ make check       ; echo $?  -> 2   fails at the first line, `go build ./...`
$ go mod tidy && grep '^go ' go.mod -> go 1.25.0     # tidy raises it back, silently
```

**The guard has teeth from VS2 onward.** Three things follow and the second is
a correction:

1. `grep '^go ' go.mod` stops being a pure artefact check. The directive is now
   load-bearing and the gate reddens on its first command.
2. **The floor is forced by pgx, NOT by x/crypto.** The slice plan's
   `expected_to_contradict` predicted "the floor may be forced by x/crypto
   alone here", and that is wrong at VS2 for a simple reason: **x/crypto is not
   in the module graph yet** — it arrives with Argon2id at VS6. Measured, per
   dependency: `github.com/jackc/pgx/v5 v5.10.0` declares **exactly `go
   1.25.0`**; `golang.org/x/text v0.29.0` and `golang.org/x/sync v0.17.0`
   declare `go 1.24.0`; `puddle/v2` `go 1.19`; `pgpassfile` `go 1.12`. So a
   single dependency forces it, and the prediction should be recorded as
   **met early and by a different module than named**.
3. VS1's characterisation of the hazard survives intact: `go mod tidy` silently
   raises `go 1.25` to `go 1.25.0`, so the thing to guard is **re-editing the
   directive down after tidy has settled it**, not forgetting to tidy.

### What was decided, and what each was decided against

- **Nothing in config has a default; all seven are required.** The alternative
  is a defaulted `DB_MAX_OPEN_CONNS` — a pool size nobody chose, silently in
  force on a VPS. The precedent is the client's own `apiBaseUrlProvider`, which
  throws until overridden rather than carrying a placeholder. Defaults live in
  `deploy/docker-compose.yml`, where they are visible beside the thing they
  configure. **Consequence, stated rather than discovered: `make run` on a bare
  host now fails**, naming all seven at once. `make up` is the supported path.

- **`config.Load` refuses `DB_MAX_IDLE_CONNS > DB_MAX_OPEN_CONNS`, and that is
  a measurement rather than fastidiousness.** In
  `$(go env GOROOT)/src/database/sql/sql.go`, `SetMaxIdleConns` ends with
  `if db.maxOpen > 0 && db.maxIdleConnsLocked() > db.maxOpen { db.maxIdleCount
  = db.maxOpen }`. The clamp is **silent and unobservable afterwards** —
  `sql.DBStats` carries `MaxOpenConnections` and has **no idle counterpart**,
  so nothing in the running process can report that the number it was handed
  was discarded. spec L21 asks for the pool to be configured *explicitly*, and
  a call whose argument the runtime overrides is the opposite of that. Config
  is the only place the disagreement is visible, so it is refused there. The
  floors on the other three are the same shape: `DB_MAX_OPEN_CONNS=0` means
  **unlimited** to `database/sql` and removes the ceiling DEC-21 sizes Argon2
  against; `ARGON2_MAX_CONCURRENT=0` is a zero-capacity semaphore, which blocks
  the first login **forever** instead of refusing it — and DEC-48 rejects
  queueing precisely because it converts memory exhaustion into timeout
  exhaustion.

- **THE `-addr` FLAG IS GONE.** spec L30 says configuration — naming ports
  specifically — is read "strictly via `os.Getenv()`", and a flag setting the
  port is a second configuration path for the same value. Two paths is exactly
  how the probe ends up asking a different port than the server answers on,
  which VS1 flagged as a latent hazard when it recorded that `probe` derives
  its port from `-addr`. So `-healthcheck` now loads the config too. That looks
  heavy and is not: `HEALTHCHECK` inherits the container's environment, and a
  config the server could not load is a server that is not running. **Verified
  in the real image** — `docker compose exec api /api -healthcheck` returns 1
  against a stopped database, which it could only do having loaded all seven.

- **The startup ping is fatal, not degraded.** `sql.Open` does not connect, so
  without it a wrong DSN is discovered at the first request and discovered as a
  503. Compose already gates the api on `postgres: service_healthy`, so a
  failure here is a real misconfiguration. The alternative — come up and serve
  503 forever under `restart: unless-stopped` — was declined because it makes a
  broken DSN indistinguishable from a database that has merely gone away, which
  is the one distinction /healthz exists to draw.

- **The redactor matches a SUBSTRING of the lowercased key, and decides on the
  key alone.** `access_token`, `sessionToken` and `authorization_header` are
  the spellings a call site actually produces; a redactor that knows only the
  exact word `token` fires on the one name nobody types. The cost of the wider
  net is a redacted `tokenizer_config`, which is nothing.
  `TestLeavesOrdinaryKeysAlone` is the leg that stops the net becoming
  everything — without it, a redactor could pass every leak test by replacing
  all values, which logs nothing and looks safe.
  **`password` is deliberately not on the list**: DEC-61 settles the field's
  name as `passphrase` and this project has no `password` anywhere. Adding a
  fourth is one entry and one table row.

- **`/healthz` logs the driver error and never shows it.** The two are one
  decision and are asserted as a pair (`TestHealthzLogsTheDriverErrorItRefusesToShow`).
  /healthz is the one route reachable unauthenticated, and a pgx connect error
  names hosts, ports and database names — measured, from the live stack:
  ``failed to connect to `user=travellog database=travellog`: hostname
  resolving error: lookup postgres on 127.0.0.11:53: no such host``. DEC-12
  states the rule for the API's error envelope; /healthz predates that envelope
  (VS3 builds it), so it is written here directly.

### The acceptance check, run against the real stack

Not a unit test — the step's check is a property of the running container.

```
### Postgres UP
HTTP 200   {"status":"ok"}
{"time":"2026-08-23T04:57:08.476Z","level":"INFO","msg":"database ready","maxOpenConns":8,"maxIdleConns":4}
{"time":"2026-08-23T04:57:08.477Z","level":"INFO","msg":"listening","addr":":8080"}

### docker compose stop postgres
HTTP 503   {"status":"unavailable"}
{"…","level":"ERROR","msg":"healthz: the database did not answer","err":"failed to connect …"}

### the container's own verdict, polled
t+5s … t+40s: Up About a minute (healthy)
t+45s:        Up About a minute (unhealthy)     <- FLIPPED
$ docker compose exec api /api -healthcheck  ->  healthcheck: 503 Service Unavailable, exit=1

### docker compose start postgres
recovered at t+6s: HTTP 200 {"status":"ok"}    and the container returns to (healthy)

### the config failure path, in the shipped scratch image
$ docker run --rm --entrypoint /api -e DATABASE_URL=x -e PORT=8080 travellog-api
config: 5 problems with the environment:
  LOG_LEVEL: not set
  DB_MAX_OPEN_CONNS: not set
  DB_MAX_IDLE_CONNS: not set
  AUTH_RATE_LIMIT_PER_MIN: not set
  ARGON2_MAX_CONCURRENT: not set
exit=2
```

**Two things in that are worth keeping.** The container reports *healthy* for
**45 seconds** after the database goes, because `HEALTHCHECK` is
`--interval=5s --retries=12`; the 503 is immediate and Docker's verdict is not.
That is the configured behaviour rather than a defect, but anyone reading
`docker compose ps` inside that window will see a healthy container in front of
a dead database, and it is better written down than rediscovered. And the
config failure runs **in `scratch`** — the one-error report reaches stderr from
an image with no shell.

### Divergences from VS2's step text, each deliberate

- **"run migrations" is a seam, not a runner.** The migration runner is VS4
  (`internal/store/migrate.go`) and `make migrate` still exits 1. `run()` carries
  a one-line comment where the call goes. The step text is a straight
  inheritance from the parent plan's S03, which sat next to a different S05.
- **The URL-path redactor is NOT here.** The parent plan's S03 asks for two
  redactors — the attr one and one rewriting `/l/{token}` to `/l/[redacted]` —
  but the slice defers the public share path entirely, so there is no `/l/`
  route to redact and no access log to redact it in (VS3 builds the middleware).
  It belongs to the step that adds the route, and DEC-10 already carries the
  reason it must exist. **VS2's own step text names only the attr redactor**,
  so this is the slice being consistent rather than the slice being short.
- **The acceptance check's two greps were implemented as AST sweeps instead.**
  `grep -rn 'os.Getenv' --include='*.go' .` matches its own source the moment
  anyone writes the check down, matches comments, and **cannot see
  `os.LookupEnv`**, which is a one-word bypass reading the same environment.
  The sweep walks every non-test file's AST, catches `Getenv`, `LookupEnv`,
  `Environ` and `ExpandEnv`, and asserts the caller set is **exactly**
  `internal/config/config.go` — so it fails when a second file starts reading
  the environment *and* when config.go stops. **Test files are excluded, and
  that is a decision**: a helper reading `TEST_DATABASE_URL` is not application
  configuration, and VS5's `internal/store/testdb` is specified to do exactly
  that.

### What VS2 leaves guarded by nothing

Unchanged from VS1 and repeated so the list does not shorten by silence: the
Dockerfile's CA bundle, the `time/tzdata` import (VS1 measured that no Go test
can see it — macOS has zoneinfo on disk, so the embedded database is never
reached), the numeric `USER 65532:65532`, and the named volume. New at VS2 and
also guarded by nothing: **`deploy/.env.example` and
`deploy/docker-compose.yml` are not checked against `config.Load`'s variable
list.** Delete `ARGON2_MAX_CONCURRENT` from the compose file and the whole of
`make check` stays green while the container refuses to start — the parent
plan's S23 specifies the test that parses both files, and it does not exist
yet. The stack-level evidence above is the only thing standing in for it.

---

