# travellog

The Go + PostgreSQL backend for the Travel Log **logbook** client
(`/Users/mattmarshall/Documents/Flutter/Travel Log` — **READ-ONLY**; nothing in
this repository writes there). One binary, `cmd/api`. Standard library first:
`net/http`'s own `ServeMux`, `encoding/json`, `database/sql`, `log/slog`,
`regexp`, `context`.

**This repository is being built as a VERTICAL SLICE first** (DEC-55, from
agent-graph-spec-V4 §1): one authenticated traveller writing one trip to
Postgres through the real HTTP stack in Docker, reading it back through the one
conditional read, and still having it after the stack restarts. Eight steps,
four tables, five routes. When the slice is green and checked in, the remaining
nineteen steps of the parent plan are **re-planned with the slice as
`entry_state` — not resumed.**

This file is written **step by step, in the same commit as the code** (DEC-23).
A record written while the reasoning is fresh is trustworthy; one reconstructed
at the end is how stale counts and wrong justifications get in. The client's own
CLAUDE.md carries four counts that went wrong from exactly that.

**Standing instruction:** when a claim here is corrected, record **what was
wrong and how it was measured**, not only the corrected fact. The correction is
usually worth more than the fact that replaced it.

---

## The gate

```bash
make check
```

`go build ./...` → `go vet ./...` → `gofmt -l .` (must print **nothing**) →
`go test ./...`. **There is no CI. This is the only gate, and it is manual.**

**Why `go vet` is mandatory rather than a nicety.** The module directive is
written literally as `go 1.25.0` (DEC-27), and vet is one of the two commands
that enforce the language floor. It is in the chain because a build can be
served from cache while vet re-resolves the module graph; keeping both means the
floor is checked by two independent paths rather than one.

**Why `gofmt -l .` is inspected rather than chained.** `gofmt -l` exits **0**
whether or not it lists a single file. A bare `gofmt -l . && …` is therefore a
check that cannot fail — the class the parent plan calls out at DEC-28. The
Makefile captures its output and fails on non-empty.

**`make migrate` and `make slice` exit 1 today**, naming the step that
implements them (VS4 and VS8). A target that exits 0 having done nothing is
indistinguishable from one that succeeded, and that is how a missing step gets
counted as a passing one.

**`make check` leaves `./api` behind, and that is `go build ./...`, not a bug in
the Makefile.** With a single main package in the pattern, `go build ./...`
writes the executable into the **current directory**. Measured at VS1 — `git
add -A` staged a 9 MB binary. The command is kept literal, because the gate is
specified as those four commands in that order, so `/api` is git-ignored with
the reason written beside it. Do not "tidy" that line away without changing the
gate first.

---

## The spec, and the divergences from it

The governing spec is `/Users/mattmarshall/Desktop/go_backend.md`, 32 lines,
`sha256 e546f63d814bb79f1dd64856554a70051e50a63552d086466bfd6fe518eebceb`
(verified at VS1 against the value the constraints were checked against).

Three divergences are named, and **two of them are different kinds of thing.**
Filing them the same way would put a false entry in the document a later reader
trusts most.

| # | What | Diverges from | Status at VS1 |
|---|---|---|---|
| 1 | Dockerfile stage 1 is `golang:1.26`, not `golang:1.22` (DEC-09) | **the spec**, L31 | shipped |
| 2 | `go.mod` directive is `go 1.25.0` (DEC-27) | **a human ruling**, not the spec | shipped |
| 3 | A fourth compose service, MinIO (DEC-39) | **the spec**, L32 | **not in the slice** |

- **Divergence 1 is doubly forced.** A `golang:1.22` stage could not compile a
  module whose floor is `1.25.0`. Stage 2 is `scratch` exactly as L31 mandates.
- **Divergence 2 is NOT a spec divergence.** L17 says "Go 1.22 or higher", and
  1.25.0 satisfies it. It diverges from ruling 4's intent that the shipped
  binary and the development toolchain match — and raising the floor serves
  that intent rather than defying it.
- **Divergence 3 is not reached in the slice.** No object storage, no media.

**The slice is also two services where L32 asks for three.** Caddy is
**deferred, not declined** — see "Inherited unfinished" below.

---

## The `go 1.25.0` literal — and what VS1 actually measured

The directive must read **`go 1.25.0`**, not `go 1.25`. That is DEC-27, and
VS1 re-measured it rather than carrying it. **The measurement corrects the
plan's attribution in one place and sharpens it in another.**

Measured 22 August 2026, Go 1.26.5 darwin/arm64:

```
# (a) this module, at VS1, with ZERO external dependencies
$ go mod edit -go=1.25 && go build ./... ; echo $?   -> 0
$ go vet ./...                          ; echo $?   -> 0

# (b) a throwaway module importing golang.org/x/crypto/argon2
$ go mod tidy            # RAISES the directive from `go 1.25` to `go 1.25.0`
$ go mod edit -go=1.25   # put it back, i.e. pin it against tidy's advice
$ go build -o /dev/null ./... ; echo $?  -> 1
  go: updates to go.mod needed; to update it:
  	go mod tidy
$ go vet ./...                ; echo $?  -> 1
  go: updates to go.mod needed; to update it:
  	go mod tidy
$ go mod edit -go=1.25.0 && go build … && go vet …   -> 0, 0
```

Three things follow, and the first is a correction:

1. **At VS1 the literal is not yet forced by anything.** The slice has no
   external modules until VS2, and with none, `go 1.25` builds and vets
   cleanly. So VS1's acceptance check `grep '^go ' go.mod` is a **pure
   artefact check** — it can only fail if somebody edits the file, never
   because the code is wrong. It is recorded as such rather than presented as
   evidence about the build. The check acquires teeth at VS2, when pgx and
   x/crypto arrive.
2. **The failure mode is more specific than "1.25 fails".** It is
   `golang.org/x/crypto v0.55.0` and `golang.org/x/sys v0.47.0` each declaring
   `go 1.25.0`, against which the directive comparison treats `1.25` as
   strictly lower. The error is not a compile error — it is
   `go: updates to go.mod needed`, refusing to proceed.
3. **`go mod tidy` will fix it for you, which is the trap.** Tidy silently
   raises `go 1.25` to `go 1.25.0`. The failure only appears if somebody pins
   `1.25` back afterwards. So the thing to guard is not "run tidy" — it is
   "do not re-edit the directive down after tidy has settled it".

`expected_to_contradict` in the slice plan asks whether the floor is forced by
x/crypto alone once minio-go is out of scope. **VS1 cannot answer that** — it
has no dependencies at all. The question stands open for VS2/VS6, where
`grep '^go ' go.mod` after `go mod tidy` is a real check.

---

## Layout

```
cmd/api/main.go        the one binary
internal/              everything else (config, logging, httpx, auth, store, logbook, api)
migrations/            .up.sql / .down.sql, embedded and applied by internal/store
deploy/                Dockerfile, docker-compose.yml, .env.example
scripts/               slice-arc.sh (VS8)
docs/                  EVIDENCE.md (VS8)
```

Standard Go project layout, as go_backend.md L17 asks.

---

## The stack

Two services, `deploy/docker-compose.yml`:

- **`api`** — built from `deploy/Dockerfile`, published on **127.0.0.1:8080**
  only. Local-only target (DEC-21).
- **`postgres`** — `postgres:17`, named volume **`pgdata`**.

**The named volume is the point, and a line in a YAML file is not evidence it
works.** `pgdata` is the only thing standing between `docker compose down && up`
and an empty log, and its failure stays invisible until a redeploy. VS8's arc
restarts the stack and reads the trip back; that is the proof, and nothing
earlier is.

**The database port IS published, on 127.0.0.1:5434.** Two decisions in that:

- **Published at all**, against the parent plan's S22 stance, because the
  `internal/store` tests run on the **host** and reach the database through
  `TEST_DATABASE_URL`. Loopback-bound; a deploy publishes nothing.
- **5434, after two attempts.** 5432 is a developer Postgres on this machine
  (`lsof -nP -iTCP:5432 -sTCP:LISTEN`). 5433 — the obvious next one, and what
  VS1 wrote first — is held by an **unrelated Docker container**
  (`slab-postgres`), which `lsof` attributes to `com.docker` and which no
  inspection of this repository would have revealed. It failed at
  `docker compose up` with `bind: address already in use`, not at review.
  **The lesson is not "use 5434"** — it is that a published host port is a fact
  about one machine, so `POSTGRES_PORT` in `deploy/.env` is the knob and the
  default is disposable.

**Service naming.** The database service is `postgres`, which is also the host
in `DATABASE_URL`. The parent plan's S22 calls it `db`. The slice plan's own
text says "api and postgres", and the slice governs this step — but the two
artefacts disagree and the re-plan must settle it rather than inherit both.

### `scratch` and its four missing things

Stage 2 is `scratch`, per L31. Everything a normal base image supplies has to
be put there explicitly:

- **CA bundle** — copied from stage 1. No roots, no outbound TLS.
- **Timezone database** — `_ "time/tzdata"` in `cmd/api/main.go`. There is no
  `/usr/share/zoneinfo`, and nothing fails on a developer's machine.
- **A non-root user, NUMERIC**: `USER 65532:65532`. `scratch` has no
  `/etc/passwd`, so `USER nonroot` would fail to resolve at container start.
- **A health probe.** No shell, no curl. `HEALTHCHECK` invokes the binary's own
  `-healthcheck` flag. **That flag exists for this line** — it is not a
  convenience, and removing it leaves the container with no way to report
  health at all.

---

## What VS1 shipped that it was not asked for, and why

**`cmd/api/main.go`, a placeholder.** VS1's file list names `cmd/api/.keep`,
and its acceptance check is
`docker compose up -d && curl -sf localhost:8080/healthz`. **Those two cannot
both hold**: there is no Docker image of a Go binary that does not exist, and
no `/healthz` to curl. The check is the substantive one in an otherwise
artefact step, so the placeholder is what makes it runnable.

It is deliberately narrow:

- **It does not read `os.Getenv`.** VS2's acceptance check is that
  `internal/config` is the only file in the repository that does. A placeholder
  that has to be un-done to satisfy a later check is worse than one that is
  simply small. It takes `-addr` and nothing else.
- **`/healthz` answers a constant.** VS2 replaces it with a database ping so it
  can answer 503, and VS2's own mutation proof — "make healthz return a
  constant and the 503 leg reddens" — is written against exactly this. The
  placeholder is the pre-state that proof needs.
- **`.keep` was not created.** A `.keep` beside a real file is noise.

**`make up`, `make down`, `make logs`, `make fmt`.** Not in the step's named
target list (build, run, check, test-db, migrate, slice), and `test-db` and
`slice` both need the stack up. `fmt` exists because `make check` tells you to
run it.

---

## Inherited unfinished

Recorded as deferrals rather than allowed to read as simplifications.

- **Caddy, and `X-Forwarded-For` with it.** Two services, not three. The rate
  limiter (VS3) will key on `RemoteAddr` — **correct for a direct connection
  and wrong the moment a proxy appears.** The limiter-behind-proxy leg (two
  different `X-Forwarded-For` values, one `RemoteAddr`, separate buckets)
  belongs to the step that adds Caddy, and does not exist yet.
- **`make migrate`, `make slice`** — VS4 and VS8. Both exit 1 today.
- **The DEC-27 floor attribution** — see above; VS1 has no dependencies and
  cannot answer it.

---

## VS1 — what was decided, measured and declined

**Decided**

- Module path is bare `travellog` (DEC-20), matching sibling GOMX's bare
  `gomx`. One binary, not a library anyone imports.
- `git init` inside `/Users/mattmarshall/Documents/Go/travellog`.
  `/Users/mattmarshall/Documents/Go/` is not a repository and GOMX is not one
  either, so there was nothing to nest inside.
- The database port is published on loopback (see above).
- `make migrate` and `make slice` fail loudly rather than exiting 0.

**Measured**

- Go **1.26.5** darwin/arm64, Docker **27.4.0**, Compose **2.31.0-desktop.2**.
- `go 1.25` vs `go 1.25.0`: the full result is in the section above. The
  headline is that VS1 **cannot** reproduce the failure, because it has no
  dependencies.
- Ports 5432 **and** 5433 are both occupied on this machine — 5432 by a
  developer Postgres, 5433 by an unrelated Docker container. The stack is
  published on 5434.
- `sha256 /Users/mattmarshall/Desktop/go_backend.md` matches
  `e546f63d…eebceb`, the value the spec constraints were verified against.

**Declined**

- **`git status --porcelain` on GOMX**, which the parent plan's S01 names as an
  acceptance check. **GOMX is not a git repository** — `git status` there
  answers `fatal: not a git repository`. The check is unimplementable as
  written. What VS1 did instead: nothing under
  `/Users/mattmarshall/Documents/Go/GOMX` was opened for writing, and the new
  repository is a sibling directory, not a parent.
- **A `.keep` file**, and **`os.Getenv` in the placeholder** — both above.
- **Publishing the database on 5432 or 5433** — both occupied.

**Honest statement about VS1's checks.** Two of the three are **artefact
checks** and cannot fail for a reason about code: `grep '^go ' go.mod`, and
`docker compose config` showing two services and a named volume. Only the third
— the stack coming up and `/healthz` answering — exercises anything. The step's
own text says as much, and it is repeated here so a later reader does not count
three checks where there is one.

---

## VS1 mutation proof — the only guard VS1 has

Run on the VS1 tree immediately before the VS1 commit. `docs/EVIDENCE.md` (VS8)
collects these at stated commits; this one is recorded here because VS1 predates
that file.

**Target:** the `gofmt -l .` step of `make check`, which is the one thing in
this step that guards anything about the source.

**The mutation** (a diff, because a mutation that does not change the file is a
green suite proving nothing):

```diff
--- cmd/api/main.go
+++ cmd/api/main.go
@@ 137a138,141
+
+func  unformatted( ) {
+_ = 1
+}
```

**Result — it reddens:**

```
go build ./...
go vet ./...
gofmt -l reported unformatted files:
cmd/api/main.go
run: make fmt
make: *** [check] Error 1
```

Note **what the mutation proves and what it does not**: `go build` and `go vet`
both passed the badly-formatted file, which is the point. Formatting is not a
compile error, so without the gofmt step nothing in the gate would have noticed.
Reverted, `make check` exits 0.

---

## VS1-BACKFILL — the tests VS1 did not write

**This is BACKFILL, not test-first, and it is labelled as such deliberately.**
The project adopted test-first at agent-graph-spec-V4 §6.7 on the human's
instruction. VS1 predates that and shipped tests-after. A test cannot be
retroactively written first, so what follows is the substitute that yields the
same evidence in a different order, and the client project's own rule is the
precedent for saying so out loud: *"When tests must come after — porting,
integrating generated code — say so and name what gets backfilled."*

The substitute, per leg: **write the test, break its subject, watch it go red,
record the actual output, restore, watch it go green.** A test that has never
been red has never been shown to work. Nothing below is reported as proven that
was not observed failing.

Backfilled 22 August 2026, on the tree at `6b246a9` plus the extraction noted
below. Go 1.26.5 darwin/arm64. `testing` and `net/http/httptest` only — no
dependency added, consistent with the standard-library-first posture.

### The one thing that was moved, and why

`newMux() *http.ServeMux` was **extracted out of `serve`**. The body is
byte-for-byte what `serve` held and `serve` now calls it; nothing else in
`cmd/api/main.go` changed, and `git diff` at the backfill commit is that
extraction alone.

It was necessary rather than tidy. `serve` blocks until a signal, builds its own
`http.Server` and hands the listener to `ListenAndServe`, which does not report
the bound port — so with `-addr :0` a test cannot learn where to connect, and
with a fixed port it is a fact about one machine. The only way to reach the
handler *without* the extraction is to send `SIGTERM` to the test process
itself, which is process-global state in a package that will grow more tests.
`newMux` is what `httptest.NewServer` takes.

**`probe` needed no change at all** — it already takes an `addr` and returns an
int, which is the shape a test wants. That is worth recording as a fact about
VS1 rather than about the backfill: the half that was hardest to write a test
for is the half that was written inline, and the half that was already a
function was free.

### What has behaviour, and what does not

`cmd/api/main.go` is the only file in the repository with a unit under test.
Every other VS1 artefact gets an explicit `test_strategy: "none"` with its
reason, rather than a silence a later reader has to interpret — and rather than
a test asserting a file exists, which is the artefact-check class VS1's own
report already owned up to.

| file | `test_strategy` |
|---|---|
| `cmd/api/main.go` | **11 legs**, `cmd/api/main_test.go`, all reddened below |
| `go.mod` | `none` — a two-line directive file. `grep '^go '` is the pure artefact check VS1 already recorded as proving nothing; it acquires teeth at VS2 under `go mod tidy`, and that is VS2's leg to write |
| `Makefile` | `none` in Go — `make check` **is** the gate, and a test invoking the gate from inside the gate is circular. VS1's own gofmt mutation proof is the guard, and it is above. **VS1-FIXES qualifies that**: one mutation is not a class, and the class it did not cover — a file gofmt cannot PARSE — walked straight through the gate. Three shell legs now exist for this step and they belong in VS8's `scripts/slice-arc.sh` |
| `.gitignore` | `none` — no behaviour. `/api` is guarded by the fact that `make check` produces it every run |
| `deploy/Dockerfile` | **CORRECTED at VS1-IMAGE-TESTS: 13 legs**, `test/image/`, all reddened. It read `none` in Go, and that was right — the tier is not in Go's default scope, it is opt-in and needs Docker. See the judgement call below, and then the section that answers it |
| `deploy/docker-compose.yml` | **CORRECTED at VS1-IMAGE-TESTS: 3 legs**, `test/image/stack_test.go`. The named volume is proved by `down` then `up` NOW rather than at VS8 — VS8's arc is still the end-to-end version through the API, but the failure this catches destroys data and VS8 is six steps away |
| `deploy/.env.example` | `none` — documentation of knobs, read by nothing |
| `CLAUDE.md` | `none` — this file |

### The judgement call on the Dockerfile, and the measurement behind it

Two Dockerfile facts looked testable in Go and **only one of them is even
arguably so. Both were declined, and the tzdata one was declined on a
measurement rather than a hunch.**

**`_ "time/tzdata"` is NOT testable in `go test`, and a leg asserting it would
be a leg that cannot fail.** The obvious test is
`time.LoadLocation("Asia/Tokyo")` returning no error. Measured, on this machine,
with a throwaway module compiled twice — once with the blank import and once
without:

```
--- WITHOUT time/tzdata import ---
ZONEINFO=                        LoadLocation: Asia/Tokyo <nil>
ZONEINFO=/nonexistent-zoneinfo   LoadLocation: Asia/Tokyo <nil>
--- WITH time/tzdata import ---
ZONEINFO=                        LoadLocation: Asia/Tokyo <nil>
ZONEINFO=/nonexistent-zoneinfo   LoadLocation: Asia/Tokyo <nil>
```

Four identical answers. The reason is the load order: `ZONEINFO` is *prepended*
to the platform sources rather than replacing them, and the embedded database is
consulted **only after every filesystem source has failed**. Both
`/usr/share/zoneinfo` and `/var/db/timezone/zoneinfo` exist here, so the
embedded copy is never reached and the import is invisible to the test binary.
**This is the exact defect the import exists to prevent** — it fails only in
`scratch`, and nothing fails on a developer's machine, which is what
`cmd/api/main.go`'s own comment already says. A green leg here would be a
comforting lie. The guard remains a human with the image, and it is unticked.

**`USER 65532:65532` is not testable in Go either**, and the distinction matters
because it is a *different* reason. It is not invisible — it is simply not Go: a
test proving it would have to `docker build` and `docker run`, which is outside
`go test ./...` and outside `make check`. Grepping the Dockerfile for the string
is an artefact assertion dressed as a test, so it was not written.

### The eleven legs, and the ten mutations that reddened them

Every mutation below changed the file — verified by `git diff --numstat`, per
the client project's rule that a mutation which does not change the file is a
green suite proving nothing — and every one was reverted afterwards. Restored,
`make check` exits 0.

| # | mutation of `cmd/api/main.go` | leg that went red | actual output |
|---|---|---|---|
| M1 | body `{"status":"ok"}` → `{"status":"degraded"}` | `TestHealthzBodyDecodesToStatusOK`, `TestProbeAgreesWithTheRealMux` | `status field = "degraded", want "ok"` |
| M2 | handler `WriteHeader(StatusOK)` → `StatusServiceUnavailable` | `TestHealthzAnswers200`, `TestProbeAgreesWithTheRealMux` | `status = 503, want 200` |
| M3 | delete the `Content-Type` header line | `TestHealthzAnswersJSONContentType` | `Content-Type = "", want "application/json"` |
| M4 | route `"GET /healthz"` → `"/healthz"` | `TestHealthzRejectsNonGET` | `POST /healthz = 200, want 405` |
| M5 | delete `probe`'s `resp.StatusCode != StatusOK` block | `TestProbeExitsOneWhenTheServerAnswersNon200` | `probe = 0 against a 503, want 1` (and 500, and 404) |
| M6 | `probe`'s dial-error branch `return 1` → `return 0` | `TestProbeExitsOneWhenNothingIsListening` | `probe = 0 against a closed port, want 1` |
| M7 | `probe`'s bad-addr branch `return 1` → `return 0` | `TestProbeExitsOneOnAnAddrItCannotSplit` | `probe = 0 on a malformed addr, want 1` |
| M8 | `probe` requests `/` instead of `/healthz` | `TestProbeRequestsGETHealthz`, `TestProbeAgreesWithTheRealMux` | `probe asked for "/", want "/healthz"` |
| M9 | route `"GET /healthz"` → `"GET /"` | `TestUnknownPathIs404` | `GET / = 200, want 404` |
| M10 | `probe`'s final `return 0` → `return 1` | `TestProbeExitsZeroWhenTheServerIsHealthy`, `TestProbeAgreesWithTheRealMux` | `probe = 1, want 0 against a 200` |

**M5 is the load-bearing one and deserves its output in full.** It is the
healthy-while-dead mutation: a `probe` that ignores the status code reports a
healthy container in front of a server answering 503, which is precisely the
branch VS2 introduces when the database is down.

```
--- FAIL: TestProbeExitsOneWhenTheServerAnswersNon200 (0.00s)
    main_test.go:129: probe = 0 against a 503, want 1
    main_test.go:129: probe = 0 against a 500, want 1
    main_test.go:129: probe = 0 against a 404, want 1
FAIL
FAIL	travellog/cmd/api	0.275s
```

**M9 and M10 exist because eight mutations left two legs un-reddened.** After
M1–M8, `TestUnknownPathIs404` and `TestProbeExitsZeroWhenTheServerIsHealthy` had
still never failed — no mutation aimed at another leg happened to reach them —
so on the standing rule they were not yet proven and two more mutations were
written for them specifically. **Count the legs against the mutations rather
than assuming a suite that went red somewhere went red everywhere:** eight
mutations covering eleven legs looks like coverage and is not.

### What the attempt to test VS1 revealed about VS1

Three things, and none is a defect:

1. **No bug was found.** Ten mutations, eleven legs, and the code behaved
   correctly under every one. VS1 checked `-healthcheck` by hand and had it
   right; what was missing was the guard, not the correctness.
2. **`probe`'s shape was already testable and the handler's was not**, purely as
   a consequence of one being a function and the other an inline closure inside a
   blocking call. That is the whole cost of the extraction, and it is an argument
   for VS2 building `newMux`'s successor as a constructor from the start.
3. **`probe` hard-codes `127.0.0.1` and derives only the port from `-addr`.**
   Correct inside the container — there is nothing else there, and the comment
   says so — but it means the flag cannot probe a remote server, and
   `TestProbeAgreesWithTheRealMux` only works because `httptest` binds loopback
   too. Recorded so VS2 does not mistake the constraint for an oversight.

**What is still guarded by nothing:** the Dockerfile's four `scratch`
compensations (CA bundle, tzdata, numeric USER, HEALTHCHECK **wiring** — as
opposed to the flag it invokes, which is now guarded), the named volume, and
the stack coming up. The first set needs a human with a device; the second is
VS8's arc. This backfill moves `/healthz` and `-healthcheck` from *guarded by
nothing* to *guarded by a named leg*, and moves nothing else.

> **SUPERSEDED, VS1-IMAGE-TESTS, 23 August 2026.** Every claim in that
> paragraph is now guarded by a named leg in `test/image/`, and the sentence
> "the first set needs a human with a device" was wrong rather than
> conservative: it needed a container, which is a thing a test can have. The
> section at the end of this file is the replacement, including the one
> declined strategy that survives and the two findings the attempt turned up.

---

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

## VS1-IMAGE-TESTS — the infrastructure claims, exercised

**Written 23 August 2026**, against the tree at `ee543b9`. Docker 27.4.0,
Compose 2.31.0-desktop.2, Go 1.26.5 darwin/arm64, daemon linux/arm64.
Eighteen legs in `test/image/`, **every one of them observed failing** before
it was recorded as guarding anything. Standard library only — `testing`,
`os/exec`, `net/http`, `archive/tar`, `encoding/json`. No dependency added.

This closes the list VS1-BACKFILL left open: the CA bundle, the embedded
timezone database, the numeric `USER` (and whether that user can read and
execute the binary), the HEALTHCHECK **wiring**, the named volume, and the
stack coming up at all.

### How to run it, and why `make check` did not change

```bash
make test-image        # 45s green, on a warm image cache
```

`make check` is still `go build ./...` → `go vet ./...` → `gofmt` → `go test
./...`, and still **2.8s**. Nothing here runs inside it: the tier is gated on
`TRAVELLOG_IMAGE_TESTS=1` **and** on a Docker daemon answering, in the shape
`internal/store` will use for `TEST_DATABASE_URL`. `go test ./...` on a machine
with no Docker runs the package and skips every Docker leg — **0.5s**, green.

**A silent skip is a pass that lies, and the mechanism for not lying was
measured rather than assumed.** Measured: under a plain `go test ./...` a
package whose tests all pass or skip prints exactly one line, `ok <pkg> 0.5s`.
`t.Skip`'s message, `t.Log`, and anything `TestMain` writes to stdout **or**
stderr are all suppressed — they surface only under `-v` or when the package
fails. So the reason is written twice: through `t.Skip` (what `-v` and
test2json see) and to **`/dev/tty`**, which `go test` does not own and cannot
capture. With no controlling terminal the second write fails and is dropped,
which is why it is not the only one.

Two legs guard that, and they are the only two in the file that need no
Docker — deliberately, because "the machine with no Docker is told" is the one
claim a developer with no Docker can still break.

### Two compose projects, on their own ports, by decision

The tier runs under `-p travellog-imagetest` and `-p travellog-imagetest-vol`,
on host ports 18080/15434/15435. `-p` beats the `name: travellog` in the
compose file, so the volumes it creates and destroys are
`travellog-imagetest*_pgdata`. **A test that cannot run beside `make up` is a
test nobody runs**, and one that could `down -v` a developer's own `pgdata` is
worse than no test at all. The volume leg gets its own project because it must
call `down`, and a shared stack cannot be pulled out from under the other legs.

### The probe, and why the image is layered rather than exec'd into

`scratch` cannot be inspected from the inside: no shell, no `ls`, nothing to
exec but `/api`, which takes no argument that would report any of this. So a
~60-line Go program is cross-compiled for the daemon's platform (`GOOS=linux`,
`GOARCH` read from `docker version --format {{.Server.Arch}}`, `CGO_ENABLED=0`
— the first run produced `exec /probe: exec format error` from getting that
wrong), layered onto **the image under test** with a two-line Dockerfile, and
run. It inherits that image's filesystem and its `USER`, so what it reports is
a fact about the real image.

**It is a string constant in the test, not a package.** A second `main` package
in this module would make `go build ./...` — the literal first command of the
gate — drop a second binary in the working directory every run, which is the
thing `/api` already has a paragraph in `.gitignore` about.

### The tzdata measurement VS1-BACKFILL could not make

The backfill declined a tzdata leg **on a measurement**, and that was the right
call: on macOS, a program with and without `_ "time/tzdata"` gave four
identical answers, because the embedded database is consulted only after every
filesystem source fails and macOS has two. It wrote the measurement down and
recorded `test_strategy: "none"`.

Inside `scratch` the filesystem sources are gone, and the same experiment
separates. Same program, same image, one import different:

```
--- probe WITHOUT _ "time/tzdata", inside the runtime image ---
tokyo=err:unknown time zone Asia/Tokyo
zoneinfodir=missing
--- probe WITH _ "time/tzdata", inside the runtime image ---
tokyo=ok
zoneinfodir=missing
```

Both halves are legs. The **negative** one is not decoration: if a base image
ever supplied `/usr/share/zoneinfo`, the import in `cmd/api/main.go` would stop
being load-bearing and the positive leg would stop proving anything, and that
leg is what says so. The **shipped** binary is checked separately, by its
bytes: an embedded `zoneinfo.zip` keeps its entry names uncompressed, so
`Asia/Tokyo`, `America/New_York` and `Europe/London` are all in `/api`, along
with **598** `TZif` headers. Without the import: **zero**.

### TWO FINDINGS, and the first one contradicts something this file implied

**1. The stack coming up does NOT prove the numeric user can execute the
binary. A capability hides it.**

`COPY --chmod=700 /out/api /api` — root-owned, no permissions for anyone else —
against `USER 65532:65532`, and **the container starts and answers `/healthz`
normally**. The first draft of `stack_test.go` claimed that leg as the proof of
executability; the mutation left it green.

The reason: runc still holds the default capability set, `CAP_DAC_OVERRIDE`
included, at the moment it `execve`s the entrypoint, so the exec of a 0700 file
succeeds. The capabilities are then dropped **by that same `execve`**, because
a non-root euid inherits none without file capabilities. Confirmed from the
other side, which is what turns the explanation into a measurement:

```
$ docker run --rm --cap-drop=ALL <image-with-0700-binary>
docker: Error response from daemon: ... unable to start container process:
exec: "/api": permission denied: unknown
```

So a wrong-permission binary is a **latent** defect: invisible until somebody
hardens the deploy with `cap_drop`, at which point the container will not
start. The only leg in this tier that catches it is the probe's `open("/api")`,
which runs *after* the capabilities are gone and reports `permission denied` on
an image that boots fine. Both comments have been corrected in place.

**2. My own absence assertions were blind, and a mutation is what found them.**

`docker export` writes directory entries with a **trailing slash** and file
entries without. The four "this must not exist in the image" checks looked up
`usr/share/zoneinfo`, and the export held `usr/share/zoneinfo/`. A mutation
that copied the entire zone database into the runtime image left that
assertion **green**; only the layer count (2 → 3) went red, and it is the
reason the miss was noticed at all. Keys are normalised now. Two lessons, both
already in the client project's list: *an absence assertion is the easiest kind
to write so that it cannot fail*, and *a leg that reddens for a neighbouring
reason is what saves you when the aimed-at leg does not*.

### The twelve mutations, and the eighteen legs they reddened

**Ten of the twelve were applied to a COPY of the repository** under `/tmp`,
reached by `TRAVELLOG_REPO`, and not to the working tree. That is not
squeamishness about `git diff`: two other agents were writing in this
repository at the time, and `cmd/api/**` and `internal/**` were another
worker's. The two that had to be in-tree are the two whose subject is this
package's own source; both were reverted and `git diff` was checked clean.

| # | mutation | legs reddened | actual output |
|---|---|---|---|
| M1 | `USER 65532:65532` → `USER nonroot` | `TestRuntimeImageRunsAsANumericNonRootUser`, `TestTheStackComesUpAndAnswersHealthz` | `Config.User = "nonroot", want uid:gid …` and, from the daemon, `unable to find user nonroot: no matching entries in passwd file` |
| M2 | `USER 65532:65532` → `USER 0:0` | `TestRuntimeImageRunsAsANumericNonRootUser`, `TestTheContainerProcessRunsAsTheNumericUser` | `Config.User = "0:0", which is root`; `uid = 0: the container is running as root` |
| M3 | delete the CA bundle `COPY` | `TestRuntimeImageCarriesTheCABundle`, `TestTheCABundleGivesTheContainerARealRootStore`, `TestOutboundTLSVerifiesAgainstTheCopiedBundle`, `TestRuntimeImageIsScratchAndHasNothingToFallBackOn` | `x509.SystemCertPool() holds 0 roots`; `tls: failed to verify certificate: x509: certificate signed by unknown authority`; `1 layers, want 2` |
| M4 | `HEALTHCHECK … CMD ["/api", "-healthcheck"]` → shell form | `TestRuntimeImageHealthcheckInvokesTheBinarysOwnFlag`, `TestDockerReportsTheContainerHealthy` | `HEALTHCHECK is "CMD-SHELL" form, want CMD (exec)`; and the health log, which is the whole argument: `exec: "/bin/sh": stat /bin/sh: no such file or directory` × 5, `health status = "unhealthy" after 120s` |
| M5 | delete `HEALTHCHECK` entirely | same two | `the image declares no HEALTHCHECK …`; `the running container has no health state at all` |
| M6 | `COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo` | `TestScratchWithoutEmbeddedTzdataCannotResolveAZone`, `TestRuntimeImageIsScratchAndHasNothingToFallBackOn` | `a binary WITHOUT _ "time/tzdata" loaded Asia/Tokyo inside this image`; `/usr/share/zoneinfo is present`; `3 layers, want 2` |
| M7 | delete `_ "time/tzdata"` from the copy's `cmd/api/main.go` | `TestTheShippedBinaryEmbedsTheTimezoneDatabase` | `the shipped binary contains no "Asia/Tokyo"` … `holds 0 TZif headers, want the whole database (measured: 598)` |
| M8 | remove `pgdata:/var/lib/postgresql/data` from compose | `TestTheNamedVolumeSurvivesDownAndUp` | `no volume named "travellog-imagetest-vol_pgdata"`; and after `down` + `up`, `ERROR: relation "volume_probe" does not exist` — *the database directory did not survive the restart, and a redeploy would destroy every trip in the log* |
| M9 | compose `DATABASE_URL` host → `nosuchhost` | `TestTheStackComesUpAndAnswersHealthz` | `connect: connection refused`, over a restart loop of `the database did not answer within 10s: … lookup nosuchhost` |
| M10 | `ENTRYPOINT ["/api"]` → `CMD ["/api"]`, delete `EXPOSE` | `TestRuntimeImageEntrypointIsTheBinary` | `Entrypoint = [], want [/api]`; `ExposedPorts = map[], want 8080/tcp` |
| M11 | `COPY --chmod=700 /out/api /api` | `TestTheShippedBinaryIsReadableAndExecutableByAnyUser`, `TestTheShippedBinaryIsReadableByTheUserItRunsAs` | `/api mode 0700: the uid in USER is not the owner, so it needs o+rx`; `opening /api as uid 65532: err:open /api: permission denied`. **And see finding 1: the stack leg stayed green** |
| M12 | in-tree, reverted: `writeNotice` writes nothing; the skip reason drops the target name | `TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt`, `TestTheSkipReasonNamesTheMakeTarget` | `notice = "", want "hello from the skip\n"`; `skip reason … does not name "make test-image"` |

One leg is reddened by a **test-side** mutation rather than a subject-side one,
and it is labelled that way rather than counted with the rest:
`TestEmbeddedTzdataResolvesInsideScratch` asserts that a binary **with** the
import resolves `Asia/Tokyo` in this image, and nothing in the Dockerfile can
falsify that — the probe carries its own import. Pointing it at the no-tzdata
probe reddens it (`LoadLocation("Asia/Tokyo") inside the runtime image:
err:unknown time zone Asia/Tokyo`), which proves the assertion is wired to the
subject it names. Reverted immediately.

**Count the legs against the mutations rather than assuming a suite that went
red somewhere went red everywhere** — the backfill's own M9/M10 lesson. Twelve
mutations, eighteen legs, and four of the legs needed a mutation aimed at
nothing else: M10, M11 and both halves of M12 exist only because their targets
had never failed.

### What the attempt revealed about VS1's infrastructure

**No defect was found in it.** Every claim VS1's Dockerfile comment makes about
`scratch` is true of the built image: the bundle is there and gives the process
150 real roots, the binary carries the whole IANA database, the user is 65532
at runtime and not just in the Dockerfile, Docker's own probe goes `healthy`
inside a container with no shell, the stack comes up, and the volume survives.
What was missing was the guard, not the correctness — the same sentence
VS1-BACKFILL wrote about `/healthz`, now true of the image.

Two things it recorded that are not defects and are worth carrying:

1. **`x509.SystemCertPool()` reports 150 roots inside the image**, and an
   outbound TLS handshake to `proxy.golang.org:443` verifies. That leg is the
   one place in this tier that needs the internet, so its failure is
   **classified**: a dial that cannot resolve or connect **skips**, because
   that is a fact about the network; a dial that connects and fails to
   **verify** is a hard failure, because that is the defect it exists for.
   Under M3 it took the second branch, which is how the classification was
   itself proven.
2. **The `USER`/capability interaction in finding 1** — recorded because it
   changes what a reviewer may conclude from "the stack came up", and because
   the natural hardening step (`cap_drop: ALL` on the api service) would turn a
   silent condition into a container that never starts. Not proposed here:
   `deploy/docker-compose.yml` is not this step's file to change.

### What is STILL guarded by nothing

- **The image's behaviour on `linux/amd64`.** Everything above ran on
  `arm64` — the probe is cross-compiled for the daemon's architecture, so the
  tier follows the machine rather than pinning one, and no leg has ever run on
  amd64.
- **The `/dev/tty` half of the skip notice, against a real terminal.** It is
  guarded through a temporary file, because this environment has no pty to
  allocate. What is proven is that the helper writes what it is given and
  swallows a path it cannot open — not that a developer at a terminal sees the
  line. That last step is a human with a shell, and it is unticked.
- **`postgres:17` → `18`**, which the VS1 review flagged as a latent trap
  (`PGDATA` moved). `TestTheNamedVolumeSurvivesDownAndUp` would catch the data
  loss on a bump, but nothing asserts the tag, and this tier does not run in
  the gate — so the trap is caught only by somebody running `make test-image`.
