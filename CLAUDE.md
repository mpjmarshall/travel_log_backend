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
whether or not it lists a single file *it can parse*. A bare `gofmt -l . && …`
is therefore a check that cannot fail — the class the parent plan calls out at
DEC-28. The Makefile captures its output and fails on non-empty.

**CORRECTION (VS1-FIXES): that premise was half the story, and the missing half
was a hole in the only gate this project has.** On a **syntax error** gofmt
exits **2** and writes to **stderr**. The recipe captured stdout only and
tested `[ -n "$out" ]`, so `out` was empty, the test was false, the recipe line
succeeded, and **`make check` exited 0 with a `.go` file in the tree that gofmt
could not parse.** Measured at `ee543b9` on a file copy, with the malformed
file at `.tools/broken.go` — a hidden directory, which `./...` does not match
and which `internal/config`'s AST sweep skips at `sweep_test.go:67`, so no
other step in the gate saw it either:

```
$ gofmt -l . ; echo "gofmt exit=$?"
.tools/broken.go:2:12: expected ')', found '{'
gofmt exit=2
$ make check          # VS2's Makefile, at ee543b9
go build ./...
go vet ./...
.tools/broken.go:2:12: expected ')', found '{'
go test ./...
ok  	travellog/cmd/api	0.303s
ok  	travellog/internal/config	0.460s
MAKE EXIT=0
```

The recipe now captures `$?` as well as the output and checks the status first;
the same tree gives `MAKE EXIT=2`. **And the lesson is worth more than the
fix.** VS1 proved this step with **one** mutation — a badly formatted but
*parseable* file — and recorded the class as closed. That mutation still
reddens (re-run as a control at `ee543b9`: `MAKE EXIT=2`, "gofmt -l reported
unformatted files"). **A guard proven once against one mutation is proven
against that mutation, not against its class.** The step has three recorded
legs now — unparseable → 2, misformatted → 1, clean → 0 — under VS1-FIXES with
their output.

**Every target does what it says now.** This paragraph used to read
"`make migrate` and `make slice` fail non-zero today", and both halves have
been overtaken — VS4 implemented `migrate`, VS8 implemented `slice`. The
sentence it existed for is the one to keep: **a target that exits 0 having
done nothing is indistinguishable from one that succeeded**, and that is how a
missing step gets counted as a passing one. It is now a leg rather than a
paragraph — the arc's `record` phase runs `make slice SLICE=<stub>` against a
stub exiting 0 and a stub exiting 3, and asserts make answers 0 and 2. (The
exit-2 number itself was wrong here in two places until VS1-FIXES, which is
why it is derived by running the target rather than written down.)

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
internal/              everything else (config, logging, httpx, auth, postgres,
                       logbook, rest, media)
migrations/            .up.sql / .down.sql, a PACKAGE (//go:embed cannot reach
                       outside its own directory), applied by internal/postgres
deploy/                Dockerfile, docker-compose.yml, .env.example
.dockerignore          AT THE ROOT, because the build context is the root
scripts/               slice-arc.sh — `make slice`, five phases, needs Docker
docs/                  EVIDENCE.md, the plan and the review records
```

Standard Go project layout, as go_backend.md L17 asks.

**`.dockerignore` is at the repository root and cannot move into `deploy/`.**
Compose builds the api with `context: ..`, so the context root is the
repository root, and Docker reads `.dockerignore` from the context root and
nowhere else — a copy beside the Dockerfile would be read by nothing, silently.
It was absent until VS1-FIXES; see that section for what `COPY . .` was picking
up.

**`docs/DIVERGENCES.md` has never existed, and that is the only half of this
paragraph still standing.** It used to say `docs/` and `README.md` did not
exist either; `README.md` landed at `de94ca1` and `docs/` now holds the plan,
the three review records, the handoff and — from VS8 — `EVIDENCE.md`. The
finding it was written for survives all of that: `deploy/Dockerfile` claimed a
divergence was "recorded in three places" and named two files that had never
been created, which is what the arc's `record` phase now checks on every run.

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
  limiter **keys on `RemoteAddr`** as of VS3 — **correct for a direct
  connection and wrong the moment a proxy appears**, at which point every
  request arrives from the proxy's address and the limiter is one bucket for
  the whole internet. The limiter-behind-proxy leg (two different
  `X-Forwarded-For` values, one `RemoteAddr`, separate buckets) belongs to the
  step that adds Caddy, and does not exist yet. `httpx.ClientKey` is the one
  function that changes.
- **`internal/httpx` has no caller in `lib` code.** VS3 built the chain; VS4
  turned out not to be where a mux goes through it — VS4 is the runner and the
  schema, and the routes are VS6/VS7. Two consequences, both small and both real:
  `Timeout(d)` wants a duration `internal/config` does not read yet (there is
  no `REQUEST_TIMEOUT`), and `/healthz` still writes its body with
  `fmt.Fprintf(w, "{%q:%q}\n", …)` — the tree's only hand-rolled JSON writer.
  Converting it is one line and was **tried and reverted**: it reddens
  `TestProbeAgreesWithTheRealMux`, which asserts the body byte-for-byte
  *including its trailing newline*, and `httpx.WriteJSON` deliberately emits
  none. The newline question is settled once, for every route, by the step that
  wires the mux.
- **`make slice`** — **done at VS8**, and off this list with `make migrate`,
  which VS4 implemented. The entry is kept rather than deleted because what it
  said about a target that fails loudly is now a leg: see the `record` phase.
- **The DEC-27 floor attribution** — see above; VS1 has no dependencies and
  cannot answer it.
- **The third budget: a general per-address ceiling over the whole API.**
  Added at VS8-SEC, which built the second one. There are now two — per address
  for the credential routes, per traveller for the authenticated ones — and
  neither covers a route with **no identity at all**. The public share read (R8)
  is the first such route, and it is also what turns `Route`'s middleware
  derivation into the sixth field that step declined. The same budget is what
  would bound the session lookup an authenticated request pays for before the
  per-traveller ceiling can see it. One step, three things, and none of them is
  urgent until the share read lands.

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

> **CORRECTED, VS1-FIXES.** Both paragraphs are right about `go test` and the
> tzdata measurement above is sound, but the *remedy* they name — "a human with
> the image" — was over-scoped, and filing a guard at the wrong tier is how it
> stays unticked. All three `scratch` compensations are provable by a
> **differential Docker build**, in a project that already requires Docker for
> its acceptance check. Proven rather than asserted, against VS1's exact stage-2
> recipe: without the tzdata import, `TZDATA: FAIL unknown time zone
> Asia/Tokyo`; without the CA copy, `TLS/CA: FAIL ... x509: certificate signed
> by unknown authority`; with `USER nonroot` in place of the numeric form, the
> container will not start at all — `docker: Error response from daemon: unable
> to find user nonroot: no matching entries in passwd file`, exit 125. Three
> mutations, three reddenings, each restored by file copy and re-run green. The
> full output is under VS1-FIXES, and `test/image/` is where the standing legs
> land.

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

---

## VS1-FIXES — the review's eleven findings, and what each fix is proven by

An adversarial review of VS1, run against `ee543b9`, reported **one blocker,
four majors and six minors**. This section is the record: what changed, what
each change was proven by, and what was deliberately left for VS8.

The whole surface is `Makefile`, `deploy/Dockerfile`,
`deploy/docker-compose.yml`, `.gitignore`, a new root `.dockerignore`, and this
file. **Nothing under `cmd/` or `internal/` needed to change and nothing there
did** — one finding was already closed there by VS2, and the rest are
infrastructure.

**Every mutation below was snapshotted and restored BY FILE COPY**, never
through `git checkout`, and each restore was verified with `cmp -s` before the
next leg ran. The harness incident recorded under VS2 is the reason: against an
uncommitted tree `git checkout --` no-ops on untracked files and restores
tracked ones from the index, so mutations stack and a later red belongs to an
earlier mutation. That condition held again here — this tree was uncommitted
and partly untracked while the work ran.

**Infrastructure mutations were made on a COPY of the repository outside it**
(`rsync -a` into a scratch directory), so no temporary breakage ever existed
inside the repository and `git diff` stayed clean of it throughout.

**Tiers, stated plainly.** Nine of the eleven are **shell or Docker**, not
`go test`; two are **artefact checks and are labelled as such**, because an
artefact check can only fail when the record is wrong, which is exactly what
those two are for.

### 1 (BLOCKER) — `make check` exited 0 on a file gofmt cannot parse

**The gate's own comment claimed this class was closed, on a premise that holds
only for parseable input.** The correction, the measurement and the lesson are
written up under "The gate" at the top of this file; the four legs are here.

The malformed file went at `.tools/broken.go` deliberately: a hidden directory
is skipped by `./...`, so neither `go build` nor `go vet` sees it, **and** by
`internal/config`'s AST sweep at `sweep_test.go:67`, so `go test` does not
either. That isolates the gofmt step as the only thing that could have caught
it. (Put the same file under `testdata/` at `ee543b9` and the gate does go red
— but by way of the os.Getenv sweep, a test about something else entirely,
which is accidental coverage rather than a guard.)

| leg | tree | expected | observed |
|---|---|---|---|
| A | unparseable file, **Makefile at `ee543b9`** | should fail | **`MAKE EXIT=0`** — the defect |
| B | unparseable file, fixed Makefile | fail | `MAKE EXIT=2` |
| C | control: VS1's own mutation (parseable, misformatted) | fail | `MAKE EXIT=2`, recipe `exit 1` |
| D | restored, clean | pass | `MAKE EXIT=0` |

```
########## LEG B — the blocker, FIXED Makefile ##########
go build ./...
go vet ./...
gofmt itself failed (exit 2) — this is a file it cannot PARSE,
not a file it would reformat:
.tools/broken.go:2:12: expected ')', found '{'
make: *** [check] Error 2
MAKE EXIT=2

########## LEG C — control: VS1's own mutation, parseable but misformatted ##########
go build ./...
go vet ./...
gofmt -l reported unformatted files:
cmd/api/main.go
run: make fmt
make: *** [check] Error 1
MAKE EXIT=2
```

**Leg C is the one that carries the lesson**: the guard VS1 proved still works,
exactly as proved, while the class beside it walked through. **A standing leg
for A belongs in VS8's `scripts/slice-arc.sh`** — a test that invokes `make
check` from inside `make check` is circular, which is why `Makefile` still
reads `test_strategy: none` in the table above. Left there deliberately rather
than dropped.

### 2 (MAJOR) — the postgres healthcheck probed the socket, not TCP

`pg_isready` with no `-h` asks the **Unix socket**, and the official entrypoint
runs a bootstrap server on the socket only — `listen_addresses=''` — while it
finishes initdb and runs `/docker-entrypoint-initdb.d/*`. Through that phase
the check passes and TCP refuses, so `depends_on: service_healthy` released the
api against a port that was not listening. The api reaches Postgres over TCP at
`postgres:5432` and never over the socket.

Fixed with `-h 127.0.0.1`, with the mechanism written beside it in the compose
file. **Proven by differential**, VS4's future migrations stood in for by one
`sleep 20` script mounted at `/docker-entrypoint-initdb.d` through a compose
override — no repository file touched — polling Docker's verdict against a real
`pg_isready -h 127.0.0.1` inside the container once a second:

```
===== LEG A — DEFECT: socket healthcheck (pre-fix), slow init =====
configured healthcheck: ["CMD-SHELL","pg_isready -U travellog -d travellog"]
t(s)   docker     TCP(-h)      socket(no -h)
0      starting   REFUSED      DOWN
1      starting   REFUSED      OK
5      healthy    REFUSED      OK
...
20     healthy    REFUSED      OK
21     healthy    ACCEPT       OK
SAMPLES WITH docker=healthy WHILE TCP=REFUSED: 12

===== LEG B — FIXED: pg_isready -h 127.0.0.1, same slow init =====
configured healthcheck: ["CMD-SHELL","pg_isready -h 127.0.0.1 -U travellog -d travellog"]
0      starting   REFUSED      DOWN
...
20     starting   REFUSED      OK
21     starting   ACCEPT       OK
22     healthy    ACCEPT       OK
SAMPLES WITH docker=healthy WHILE TCP=REFUSED: 0
```

Twelve disagreements to none, and under the fix `healthy` arrives only after
TCP accepts. **Raising `start_period` would not have fixed it**: a check that
passes during the start period marks the container healthy immediately.

The review measured the window on a *normal* cold start at 0.33s / 0.28s /
0.31s against a 2s interval, and could not catch the flip live — so the race is
proven by mechanism and by the widened case, not by observation of the narrow
one. **A standing leg belongs with VS8's restart arc**, which already has to
bring the stack up and poll it.

### 3 (MAJOR) — no `.dockerignore`, and `COPY . .` copied `deploy/.env`

The build context is the repository root (`context: ..`) and stage 1 does
`COPY . .`, so every git-ignored file in the tree landed in the **build stage**
— including `deploy/.env`, the file `.gitignore` exists to keep out of the
repository and `.env.example` tells developers to create.

Fixed by adding `.dockerignore` **at the context root**, which is the
repository root and not `deploy/`. Two legs, and only the second is evidence:

- **artefact check** (cheap, catches a stale record): the file exists and names
  `deploy/.env`.
- **the real one** — a canary `POSTGRES_PASSWORD` in `deploy/.env`, built
  `--target build`, then read back out of the intermediate image:

```
########## LEG A — the defect: .dockerignore REMOVED (the ee543b9 pre-state) ##########
#5 [internal] load .dockerignore
#5 transferring context: 2B done
#7 transferring context: 5.44MB 0.3s done
--- cat /src/deploy/.env inside the build stage ---
POSTGRES_PASSWORD=hunter2-vs1fixes-canary
cat exit=0

########## LEG B — with .dockerignore ##########
#5 transferring context: 1.84kB done
#7 transferring context: 1.63kB 0.0s done
--- cat /src/deploy/.env inside the build stage (expect: no such file) ---
cat: /src/deploy/.env: No such file or directory
cat exit=1
--- .git present? ---
ls: cannot access '/src/.git': No such file or directory
```

`transferring context: 2B` is the empty default — the measurement that says no
`.dockerignore` was being read at all. **The shipped image was never affected**
(stage 2 is `scratch` and copies two paths; still two RootFS layers after the
change), so this is intermediate-image and build-cache exposure rather than a
leak in what ships. The context dropped from **5.44 MB to 1.63 kB**, which is
also the layer-caching fix: `.git` and the stray `./api` binary invalidated
`COPY . .` on every commit.

### 4 (MAJOR) — `make test-db` printed a URL it had made up

Port, user, password and database were literals while compose resolves all four
from `deploy/.env`. The consequence is worse than a URL that will not connect:
host-run `internal/store` tests create and drop tables, and on this machine
5432 and 5433 are other people's databases.

Fixed by deriving every field from the container that is actually running —
`compose port postgres 5432` for the port, `compose exec -T postgres printenv`
for the three environment values — so the print and the stack have one source.
**Proven with an override `deploy/.env` (`alice` / `s3cret` / `otherdb` /
5999):**

```
########## LEG A — DEFECT: the hardcoded recipe (ee543b9) ##########
export TEST_DATABASE_URL=postgres://travellog:travellog@127.0.0.1:5434/travellog?sslmode=disable

########## LEG B — FIXED: derived from the running container ##########
export TEST_DATABASE_URL=postgres://alice:s3cret@127.0.0.1:5999/otherdb?sslmode=disable

########## cross-check against compose's own answer ##########
compose port postgres 5432 -> 127.0.0.1:5999

########## LEG C — the URL actually connects ##########
$ psql "$URL" -tAc "select current_user||'@'||current_database()"
alice@otherdb
```

Leg C is what makes it evidence rather than string comparison: the URL the
target prints opens a session as the user and database the stack is running.
**A standing leg belongs in VS8** — it needs the stack up.

### 5 (MAJOR) — HEALTHCHECK coupled to a listen port it did not read

**ALREADY FIXED BY VS2, and confirmed rather than re-fixed.** The review found
`-healthcheck` relying on a `-addr` flag defaulting to `:8080` while the
server's port was a separate knob: `docker run -d <img> -addr :9090` gave
`health=starting failingStreak=4` and climbing while `docker exec <c> /api
-healthcheck -addr :9090` exited **0** — a container serving correctly and
reported unhealthy forever, which `make up --wait` turns into a failed deploy.

VS2 **deleted the flag**. `main()` builds `addr` as `":"+cfg.Port` and
`-healthcheck` loads the same config the server does, so there is one source
for the port and nothing to pass. Confirmed against the real image, the whole
stack at a non-default port through a compose override:

```
PORT: "9090", published 9091
 Container vs1fixes-port-postgres-1  Healthy
 Container vs1fixes-port-api-1  Healthy
compose up exit=0
health=healthy failingStreak=0 user=65532:65532
$ curl -s -i http://127.0.0.1:9091/healthz
HTTP/1.1 200 OK
Content-Type: application/json
```

**What VS2's fix does NOT close, and it is smaller than it looks:** `8080`
still appears as a literal in three places — `EXPOSE 8080`, compose's
container-side port in `127.0.0.1:${API_PORT:-8080}:8080`, and compose's
`PORT: "8080"`. Only the last one decides anything; `EXPOSE` publishes and
configures nothing, and the mapping's container side must equal `PORT` or the
published port reaches nothing. Both now carry a comment saying so. The
probe-to-server coupling — the one that made a healthy container unhealthy — is
gone.

### 6 (MINOR, RECORD) — a Dockerfile comment cited two files that never existed

`deploy/Dockerfile` claimed DEC-09's divergence was "recorded in three places:
here, `docs/DIVERGENCES.md`, and `README.md`". **Neither file has ever
existed**, checked three ways rather than assumed: `ls docs` is absent;
`git show f6705e6 --name-only` lists only `CLAUDE.md`, `cmd/api/main.go`,
`cmd/api/main_test.go`; and `git log --all --diff-filter=A --name-only |
grep -iE 'divergence|readme'` matches nothing in the entire history. They were
not deferred either — the Layout block plans `docs/EVIDENCE.md` (VS8) and no
other `docs/` file.

The comment now says **two places** and names them, with the correction and how
it was checked kept beside it, per this file's standing instruction. The Layout
block above says the same thing from the other end. **This is an artefact check
and is filed as one**: a grep asserting that every path named in a comment
exists can only fail when the record is wrong, which is what it is for. It is
not evidence about code, and it belongs in VS8's arc script beside the other
record checks.

### 7 (MINOR, RECORD) — "Both exit 1 today" — they exit 2

Corrected in the two places it appeared. `make migrate >/dev/null 2>&1; echo $?`
→ **2**; `make slice` → **2**. The recipes do `exit 1`; make reports its own
exit 2 when a recipe fails, and 2 is what a caller or CI observes. The
substantive claim was right and only the number was wrong — recorded because
this project's standing rule is that a number in the record is re-derived
rather than remembered, and this one was remembered.

### 8 (MINOR, RECORD) — the `scratch` guards were filed at the wrong tier

This file concluded that the tzdata guard "remains a human with the image" and
that `USER 65532:65532` is "not testable in Go either". Both are **correct
about `go test`** and the tzdata measurement behind them is sound — the
embedded database is consulted only after every filesystem source fails, so on
a machine with `/usr/share/zoneinfo` the import is invisible to a test binary.
**The remedy was over-scoped**, and a guard filed at the wrong tier is one that
stays unticked.

All three are provable by a **differential Docker build**, in a project that
already requires Docker for its acceptance check. Proven here rather than
argued, against VS1's exact stage-2 recipe — build once with everything, then
once per removed line:

```
########## CONTROL — all three compensations present ##########
TZDATA: OK Asia/Tokyo loaded
TLS/CA: OK verified chain to example.com
UID: 65532 GID: 65532

########## MUTATION 1 — remove the tzdata blank import ##########
TZDATA: FAIL unknown time zone Asia/Tokyo

########## MUTATION 2 — remove the CA bundle COPY ##########
TLS/CA: FAIL Get "https://example.com": tls: failed to verify certificate:
        x509: certificate signed by unknown authority

########## MUTATION 3 — USER 65532:65532 -> USER nonroot ##########
docker: Error response from daemon: unable to find user nonroot:
        no matching entries in passwd file.
run exit=125

########## RESTORED — all three back ##########
TZDATA: OK Asia/Tokyo loaded / TLS/CA: OK / UID: 65532 GID: 65532
```

Three mutations, three reddenings, each restored by file copy and re-run green
— the same standard the eleven Go legs were held to, from outside `go test`.
The numeric `USER` was also confirmed on the **real** image rather than a
stand-in: `docker top` on the running api container reports `UID 65532` for
`/api`. The reclassification is *"guarded by nothing, reachable by a Docker
leg"* and never *"needs a human with a device"*; `VS1-IMAGE-TESTS` above is the
tier that lands the standing legs.

### 9 (MINOR) — `.gitignore` read as an invitation to a root `.env`

Compose's project directory is the compose file's directory, `deploy/`, so only
`deploy/.env` is interpolated. A root `.env` is ignored **silently** — no
warning, no error, every variable falls back to its default and the stack comes
up looking correct, which is the failure mode hardest to notice. Measured:

```
root .env=6001     -> compose resolves published: "5434"   (the default)
no .env at all     -> compose resolves published: "5434"
deploy/.env=6002   -> compose resolves published: "6002"
```

**The bare `.env` line was KEPT rather than dropped**, which was the other
option, and the reason is written beside it: a stray root `.env` is exactly the
file that must not reach the repository if somebody writes one anyway, so a
git-ignored dud beats a committed one. What was missing was the sentence saying
compose does not read it, where the developer is looking. Asserting that
compose ignores it would be testing compose, not this repository.

### 10 (MINOR) — the probe's deadline equalled Docker's timeout

`HEALTHCHECK --timeout=3s` against `probe`'s own `context.WithTimeout(3s)`: a
probe that actually reached its deadline raced Docker's kill, so the health log
recorded a killed check with **empty output** instead of the
`healthcheck: Get ...: context deadline exceeded` line the code exists to
print. The diagnostic could never win.

Fixed in the Dockerfile rather than in `cmd/api` — the outer budget is the one
in this step's surface, and raising it changes nothing about the server.
`--timeout=4s` gives the probe 1s to land its message. The three budgets now
nest, and the comment says so: **/healthz's ping 2s < probe 3s < HEALTHCHECK
timeout 4s < interval 5s.** Confirmed on the built image —
`Timeout: 4000000000`. No leg: it needs a server that hangs past the deadline,
which is a fixture this slice has no other use for. The ordering constraint is
recorded as a comment instead, which is what the convention asks for.

### 11 (MINOR) — no `stop_grace_period` on the api

`serve()` shuts down with a 10s context and Docker's default stop timeout is
also 10s, so a shutdown that used its allowance was SIGKILLed at exactly the
boundary — the graceful path's last moments are the ones that got killed, and
from VS2 an in-flight request is a write to Postgres. `stop_grace_period: 15s`,
confirmed on the running container (`.Config.StopTimeout` → **15**, previously
unset). Same ordering rule as finding 10 and the comment says so. Observable
only under load, so no leg: it wants a slow in-flight request during
`compose stop`, which is VS8's arc territory.

### The latent trap that was flagged and NOT tested: `postgres:17` → `18`

Postgres 18's official image moved the default `PGDATA` to
`/var/lib/postgresql/18/docker`. The mount is `/var/lib/postgresql/data`, which
is exactly where 17 puts it — so bumping the tag alone would leave the named
volume mounted over a directory the server no longer uses: initdb runs into the
image's own filesystem, the stack comes up looking fine, and **the data is gone
at the next `down`.** That is the precise failure the named volume exists to
prevent. Not tested — it is a latent trap rather than a live defect, and VS8's
restart arc is the thing that would catch it. A comment now sits beside the
pin, naming what a bump must bring with it (a `PGDATA` variable or a changed
mount path, plus a dump/restore for any volume already written by 17).

### What VS1-FIXES leaves for VS8, explicitly rather than by silence

Four standing legs, all shell or Docker, all needing something `make check`
deliberately does not have:

1. **The gate's parse-error leg** — drop an unparseable `.go` file in a hidden
   directory, assert `make check` is non-zero, remove it. Needs no Docker.
   Circular from inside `make check`, which is why it is not a Makefile target.
2. **The healthcheck/TCP agreement leg** — a slow init script through an
   override, poll `docker inspect` against a real TCP connect, assert they
   never disagree. Same harness the restart arc needs.
3. **The `make test-db` leg** — bring postgres up under an override `.env`,
   assert the printed port equals `compose port postgres 5432`.
4. **The record checks** — every path named in a comment exists; no target
   claims an exit code it does not produce. Artefact tier, and labelled as
   such.

### And what this section did NOT do

**It changed nothing under `cmd/` or `internal/`.** Finding 10's other possible
fix was shortening `probe`'s own context to 2s, and finding 5's was already
taken by VS2; both were left alone. The one place that argument could have gone
the other way is finding 10, and the outer budget was chosen because a comment
in the Dockerfile can say *why* the two numbers must differ, next to both of
them.

---

## VS3 — the envelope, twelve words, an ETag with both halves, and the chain

`internal/httpx`: six source files, **900 lines of Go and 1,775 of test** —
`ls internal/httpx/*.go | grep -v _test | xargs wc -l | tail -1`, and the same
with `*_test.go`. *(This paragraph first said 700 and 1,276, from adding the
figures up in my head while writing it. Both were wrong before the commit that
introduced them, which is the whole reason this file says to re-derive counts
with the command rather than carry them. Two more numbers below were wrong the
same way and are corrected the same way.)*
**68 top-level legs, 89 counting subtests. 54 mutations run. Every leg was
reddened by at least one of them, and none of the 54 was a MISS or a build
break at the end of the run** — three were, during it, and each is recorded
below because the failure was more informative than the pass.

Test-first throughout, in the §6.7 order, with one labelled exception and one
labelled backfill, both named under "How each leg was seen red".

### What is in it

- **`json.go`** — `WriteJSON` and `DecodeJSON`, the only two functions in the
  repository that touch the encoder. 1 MiB ceiling via `http.MaxBytesReader`;
  `DisallowUnknownFields` deliberately **OFF** (DEC-13).
- **`errors.go`** — the **twelve-code** block (DEC-12), the status map, DEC-62's
  one mapping function `CodeFor`, and the two prebuilt bodies.
- **`etag.go`** — `FormatETag` / `ParseETag` / `ETagMatches` (DEC-49), both
  halves, weak comparison per RFC 9110 §8.8.3.2.
- **`middleware.go`** — `Chain`, `Base`, and recover → request id → access log →
  timeout, outermost first, with auth's slot below them.
- **`context.go`** — the request id, on its own unexported key type.
- **`ratelimit.go`** — an in-memory token bucket keyed on `RemoteAddr` (DEC-48).

### The acceptance check was RED against correct code, and that is the finding

VS3's acceptance check is
`grep -rln 'encoding/json' internal/ cmd/` returning only `json.go` and test
files. It **failed** when this step was written, on `internal/httpx/errors.go`
— a *comment* explaining that the encoder is confined to two functions. The
comment was reworded (`encoding/json` → "the JSON encoder") and the grep now
passes.

**Rewording a comment to satisfy a check is the evidence that the check is
looking at the wrong thing.** This is defect class 10 arriving exactly on
schedule, and VS2's `os.Getenv` sweep had already established the shape. So the
guard is `sweep_test.go`, which walks the AST and asserts two things a grep
cannot express:

1. exactly one non-test file **imports** `encoding/json`, and it is `json.go`;
2. inside `json.go`, exactly two **functions** use it — `WriteJSON` and
   `DecodeJSON`. The file-level grep cannot say this at all, and "confined to
   two functions" quietly becoming "confined to one file" is a change nobody
   would see.

### DEC-12's own re-derivation command over-counts, by 14

The decision says the count is re-derived, never remembered, with
`grep -c '^\s*Code' internal/httpx/errors.go`. **Measured at this commit, that
command returns 26.** It matches the const block's twelve, the status map's
twelve — every key in `statusByCode` also begins `Code` — the `Code` field of
`errorPayload`, and one `Code:` literal in `WriteFieldError`.

It is not a grep-dialect problem (`grep` on this machine is **ugrep 7.8.4**, not
BSD grep, and handles `\s` fine). It is that the command was written against a
file that did not exist yet, and the file the decision produced has a second
twelve-row block in it. A command that over-counts in the direction of "the
vocabulary is bigger than you think" is worse than no command.

- **The portable form that answers 12:**
  `grep -cE '^[[:space:]]+Code[A-Za-z]+ +Code = ' internal/httpx/errors.go`
- **And the mechanism, which is what should actually be trusted:**
  `TestTheConstBlockAndTheRuntimeMapHoldTheSameTwelveWords` parses `errors.go`,
  collects every `ValueSpec` whose declared type is `Code`, and asserts the set
  equals `httpx.Codes()` — which is derived from `statusByCode`, so the block,
  the map and the count are one fact with one source. Mutation M17 (drop
  `upload_incomplete`) and M18 (add a thirteenth to the map only) redden it from
  both directions.

### Typing the parameter does NOT close the vocabulary — measured

`Code` is a defined string type, and the instinct is that
`WriteError(w, r, someString)` therefore cannot compile. **It can.** An untyped
string constant converts implicitly, so `WriteError(w, r, "banana")` builds
cleanly, and `httpx.Code("banana")` is a one-word bypass on top of that. So
three mechanisms close it and each covers a hole the others cannot reach:

| mechanism | catches | proven by |
|---|---|---|
| the AST sweep | a literal or a conversion at a call site | M36, M37 |
| `WriteError`'s `known()` check | a `Code` value invented at runtime | M19 |
| `CodeFor`'s validation of a `Coder` | a **domain** error naming a word of its own (DEC-62's seam, invisible to any AST walk) | M20 |

**The sweep has exactly one exemption and it is named rather than allowed for.**
`WriteErrorFor` hands `WriteError` whatever `CodeFor` returned, which is a
variable. `wireCodeExemptions` in `sweep_test.go` holds that one row, and the
assertion is **equality** with the list — so a *second* variable call site
reddens the leg and has to argue for itself (M37 proves that). It is keyed on
file and function, never on a line number: a line number moves whenever an edit
above it is longer than the one it replaced, which is a check that fails against
correct work.

And `TestTheCheckerRejectsALiteralAndAConversion` runs the same checker over a
synthetic source holding all three bad shapes. Without it the sweep is a
function that has never been shown to reject anything — which today it would be:
`grep -rn 'WriteError(' internal/ cmd/ --include='*.go' | grep -v _test.go`
finds **three** call sites in lib code, one of them the exempted one.

### What `http.TimeoutHandler` actually sends, measured on a real server

The one response DEC-12's sweep structurally cannot see is the one the stdlib
writes. Read in `$(go env GOROOT)/src/net/http/server.go`, the timeout branch is
exactly `w.WriteHeader(StatusServiceUnavailable)` then
`io.WriteString(w, h.errorBody())`. **It touches no header.**

Measured, unwrapped, against `httptest.NewServer` with the envelope as the
message:

```
status=503 Content-Type="text/plain; charset=utf-8" body="{\"code\":\"timeout\"}"
```

So the body is right and the header is wrong: net/http sniffs an unset
Content-Type off the first bytes, and `{"code":"timeout"}` sniffs as text.
`jsonByDefault` fills it in **at `WriteHeader` time**, which is late enough that
a handler with a type of its own keeps it — `maps.Copy(dst, tw.h)` runs before
`w.WriteHeader(tw.code)` on the success path, so by the time the wrapper looks,
the handler's header is already there. M31 (drop the wrapper) and M10
(set it unconditionally) prove both halves.

**And two consequences of `timeout` being 503 rather than 504.** The status is
not the handler's to choose, so the vocabulary maps `timeout` to what the stdlib
writes; a table saying 504 would disagree with the one response the table does
not produce (M54). And the *other* `ctx.Done()` branch — a client that
disconnected — writes **503 with no body at all**, which is stdlib behaviour,
not a bug in this code, and is the reason the timeout leg parses the body rather
than assuming one is there.

### The one leg that could not fail

`TestIdleTimeDoesNotAccumulateBeyondTheAllowance` guards the token bucket's
ceiling — the property that stops an idle hour becoming an hour of burst, which
is the whole reason DEC-48 exists. **Mutation M32 removed the `min()` and the
leg stayed green.**

The reason is worth carrying: the test idled the clock **before the bucket
existed**. `Allow` creates a missing bucket *full*, with `last` set to the
moment of creation, so there was no elapsed time to accumulate over — the leg
was asserting that a fresh bucket holds its own burst, which is true with or
without a ceiling. One line fixed it: spend a token first, *then* idle.

This is §8's rule paying for itself. Fifty-three other mutations behaved; this
one found a guard that had never been able to fail, and no amount of reading it
would have. **A green suite cannot tell a guard from a decoration.**

### Two mutations that failed as mutations, and what each taught

- **M11 broke the build.** Deleting Recover's `http.ErrAbortHandler` branch
  outright left `errors` imported and unused, so nothing compiled and every leg
  was "red" for a reason that had nothing to do with the behaviour. Rewritten to
  *swallow* the abort rather than delete the branch. **A mutation that does not
  compile proves nothing, and it looks exactly like a mutation that proved
  everything.**
- **M01 was a wrong expectation, not a wrong test.** Putting a space in the
  prebuilt timeout body was expected to redden the timeout leg. It did not — the
  leg *parses* the body and asserts the key set, so whitespace is correctly
  invisible to it. What reddened was
  `TestThePrebuiltBodiesEqualWhatTheEncoderProduces`, which is the leg that
  exists for exactly that. The timeout leg is proven by M30 (empty body) and M31
  (no Content-Type) instead. **Record the expectation you had, not only the
  result — a MISS is sometimes the harness being wrong about the test.**

### How each leg was seen red

- **Test-first (§6.7), the great majority.** For each file the test was written
  first and run against a **compiling stub** — signatures with zero-value
  bodies — so the red is a real assertion failure rather than
  `undefined: WriteJSON`. That matters: VS2 found twelve legs a skeleton had
  satisfied vacuously, and running against a stub is how you find yours. Seven
  legs here passed against their stub and are named below.
- **BACKFILL, labelled: `json.go`.** `errors.go` could not compile without
  `WriteJSON`, so the encoder was written before `json_test.go` existed. The
  red-first sequence was then reconstructed honestly rather than claimed:
  `json.go` was copied aside, `WriteJSON`/`DecodeJSON` stubbed, the new tests
  run — twelve legs red with real assertions — and the file restored. It is
  backfill and it is not presented as test-first.
- **BACKFILL, labelled: `ratelimit_internal_test.go` and `sweep_test.go`.** The
  pruning legs and the AST sweeps were written against code that already
  existed. Proven by M15/M16 and M34–M37 respectively.
- **The seven legs a stub satisfied**, each of which needed its own mutation and
  got one: the prebuilt bodies (M01), `RequestIDFrom` on a bare context (M02),
  the string-key collision (M03), no trailing newline (M04), `ParseETag`
  refusing everything (M06), nothing matching an empty tag (M07), `Chain` with
  no middleware (M08), a fast handler through the timeout (M10),
  `ErrAbortHandler` (M11), an untrusted inbound id (M12), and an immediate
  refusal (M13).

**Coverage, stated as a number that was computed rather than felt:** 54
mutations, 68 top-level legs, **0 legs never reddened**. The harness parses
`go test -json` and unions the failing test names; the check is
`set(all_legs) - set(reddened) == ∅`.

### The mutation harness

`scratchpad/vs3/mutate.py`, and it **snapshots and restores by file copy**. It
recomputes a SHA-256 over every `.go` file in the package after each restore and
**stops the run** if the digest has moved. That is not caution for its own sake:
VS2 built a harness on `git checkout`, which against an uncommitted tree no-ops
on untracked files and restores tracked ones from the index — it lost that
step's implementation mid-run and contaminated six of thirteen results, and the
quiet half is that a bad revert poisons every *later* mutation while the suite
keeps going red, which looks exactly like success. `internal/httpx` was
untracked for the whole of this step, so a `git checkout` harness here would
have reverted **nothing at all** and reported 54 clean kills against 54
un-mutated files.

Each mutation also asserts the file actually changed before the suite runs. A
mutation that changed nothing is a green suite proving nothing.

### What was decided, and what each was decided against

- **`WriteJSON` marshals to a buffer, it does not stream.** `json.Encoder`
  writes as it walks, so a value that fails half way through has already sent an
  implicit 200 and some bytes — a truncated body under a success status, which a
  client cannot distinguish from a short read. Marshalling first means the
  failure happens while nothing has gone out and the response can still be an
  honest 500. Second, smaller reason, and it is load-bearing: `Encoder` appends a
  newline, `Marshal` does not, and the two prebuilt bodies are guarded by byte
  equality against this function. M24 replaces it with a streaming encoder and
  reddens both legs.
- **The two prebuilt bodies are literals, guarded by byte equality.**
  `http.TimeoutHandler` takes its body as a **string at construction time**,
  before any request exists, and a third helper marshalling it would break the
  two-function monopoly. So `bodyTimeout` and `bodyInternal` are written by
  hand — and `TestThePrebuiltBodiesEqualWhatTheEncoderProduces` asserts each is
  byte-identical to what `WriteJSON` produces for the same payload. The
  alternative was to trust them, which is what "the one response the sweep
  cannot see" means in practice.
- **`DecodeJSON` refuses trailing content.** `{"id":"kyoto"}{"id":"osaka"}`
  decodes the first value and silently discards the rest, so a client sending two
  documents would be told its second write succeeded (M22).
- **`DisallowUnknownFields` is OFF, and it is a promise in both directions.**
  DEC-13's additive-and-optional rule is usually read as "the server may add
  keys". The other half is that a client built against a later API sends a key
  *this* build has never heard of, and refusing it would make every additive
  change a breaking one (M21).
- **`upload_incomplete` maps to 409, not 422.** The referenced object exists and
  the request is well formed; what is wrong is the object's *state*, which is a
  conflict rather than a field validation. It ships unused in the slice, exactly
  as DEC-12 requires. **The media step owns this flow and may overturn it** — the
  reason is in the comment on `statusByCode` so it can be argued with rather than
  guessed at.
- **`CodeFor` answers `internal` for `nil`.** A nil error reaching an
  error-writing path is a bug at the call site, and answering 2xx to it would
  mean a handler reported success for work it did not do.
- **`CodeFor` checks the domain's word before it uses it.** DEC-62 says the
  sentinel is the domain's word and the code is the wire's; a domain that names a
  word outside the block gets `internal`. Without this, DEC-62's seam is the one
  hole DEC-12's sweep cannot see, because the word is a runtime value (M20).
- **Recover is outermost and reads the request id off the RESPONSE header.** Its
  own request predates the id — the id is minted below it — so
  `RequestIDFrom(r.Context())` is empty there. The request-id middleware has
  already called `w.Header().Set` on the shared ResponseWriter, so the id is
  reachable from above. Without this the one log line that matters most is the
  one line that cannot be correlated (M38).
- **The access line for a panicking request records `status: 0`, and that is
  correct rather than a defect.** The access log's deferred line runs as the
  panic unwinds, which is *before* the outer recover has written anything, so it
  records what the handler wrote: nothing. The 500 is real and the client gets
  it; the two lines are joined by the request id. Moving recover inside the
  access log would fix the number and lose everything recover is for. There is a
  leg asserting the 0 and a comment saying why, so the next reader does not
  "fix" it.
- **An inbound `X-Request-Id` is not trusted.** The id lands in every log line
  for the request, so adopting a stranger's hands anyone a way to forge one,
  collide with somebody else's, or inject into the log. There is no proxy in
  front of this server whose header could be trusted instead — that question is
  deferred with Caddy (M12).
- **The query string is not logged.** DEC-10/DEC-11's share path is deferred but
  its shape is settled: a capability lives in the URL. A logger that records
  query strings records capabilities, in plain text, for as long as the logs are
  kept (M27).
- **The limiter prunes full buckets, and needs no TTL to do it safely.** A full
  bucket and an absent bucket are the same thing — `Allow` creates an absent one
  full — so dropping full buckets changes no answer the limiter will ever give.
  That invariant is what makes the sweep tuning-free. The sweep runs only when
  the map is over `limiterPruneAbove` (1,024) and only when a *new* key arrives,
  so the ordinary request pays nothing. M15 (no-op prune) and M16 (prune
  everything) redden the two halves.
- **`Limiter.Len()` is exported for the pruning leg, and earns it.** Unbounded
  map growth is the one failure this type can have that no external behaviour
  reveals until the process is killed.
- **`FormatETag` panics on a missing half.** A zero version is what a caller
  reaches by forgetting an argument or reading a column nobody set — a programmer
  error, not a client one. It fails where the stack still names the caller,
  instead of emitting `W/"0-7"` and being found months later as a cache that
  never invalidates. Recover turns it into a 500, not a dead process (M26).
- **`ParseETag` accepts the strong spelling it never emits.** A cache, a proxy or
  a hand-written curl echoes `"2-7"` without the `W/`, and refusing it would
  answer 200 to a client that is revalidating correctly. Weak comparison is what
  RFC 9110 §8.8.3.2 specifies for `If-None-Match` anyway (M49).
- **An empty current tag matches nothing, `*` included.** A handler reaching the
  comparison with no tag computed has a bug, and answering 304 hands the client
  an empty body it will treat as unchanged — DEC-49(b)'s permanently empty app,
  arriving by a second route (M07).

### Declined, each with the reason

- **A `304`/no-body helper.** VS7 owns the conditional read and is the first
  caller; a helper written now would be a guess at its shape. `WriteJSON` is not
  needed for an empty body — a 304 carries none.
- **`Retry-After` on a 429.** It is a wire promise, and DEC-12's body is the code
  alone precisely so the client's fixed per-surface sentences do not have to
  track server detail. The client has no retry surface to hang it on. Add it with
  the step that gives the client something to do with it.
- **Trusting `X-Forwarded-For`.** Deferred with Caddy, and recorded under
  "Inherited unfinished" rather than left to be rediscovered.
- **Converting `/healthz` to `WriteJSON`.** Tried, measured, reverted — see
  "Inherited unfinished".
- **A `Service` type.** DEC-62 lands it at VS5 and says it must earn its place
  per operation. VS3 builds the seam it needs — one mapping function and the
  `Coder` interface — and nothing else. `httpx` imports no domain package and
  the domain will import no `httpx`: `Coder` is satisfied structurally, which is
  what keeps "the business logic owns the contract" true when the contract is an
  HTTP one.

### What VS3 leaves guarded by nothing

Stated plainly, in the tiers VS2's record established.

- **`Base()`'s order is proven by behaviour, not by identity.** The legs assert
  what the chain produces — a JSON 500 for a panic, an access line carrying the
  response's id, an access line existing at all for a timed-out request. M39 and
  M40 redden them. But **nothing asserts that the four are those four
  functions**: swap `Recover(log)` for an identical inline closure and the suite
  stays green. That is the right trade — asserting function identity is
  asserting the implementation — and it is worth saying out loud.
- **The 1 MiB ceiling is a number nobody has justified against a real body.**
  The largest body in the slice is one trip, which is far under it. The leg
  proves the limit is enforced *at exactly `MaxBodyBytes`*, from both sides
  (a limit asserted only from above is satisfied by a 1-byte ceiling); it proves
  nothing about whether 1 MiB is the right number. VS7's real payloads are where
  that gets an answer.
- **`-race` is not in `make check`.** `TestConcurrentCallersSpendExactly...`
  catches the lost-update under plain `go test` because the count is wrong, and
  M33 reddens it — but the *data race* itself is only reported under
  `go test -race`, which the gate does not run. Run
  `go test -race -count=5 ./internal/httpx/` by hand when touching the limiter;
  it is green at this commit.
- **Nothing exercises `httpx` over a real network.** Every leg drives handlers
  through `httptest.NewRecorder` or `ServeHTTP` directly. Header canonicalisation,
  chunked bodies, and a client that hangs up mid-request are all VS8's arc.

## Comments

**Remove explanatory comments for self-evident code. Keep only comments that are
non-obvious business logic, complex algorithms, or safety constraints.**

Inherited verbatim from the client project this backend serves, whose record states the
rule and whose reasoning applies here unchanged. **It did not cross over on its own** —
this is the fourth practice from that repository that had to be carried across by hand,
after test-first, the `git checkout` mutation-harness warning, and the database review
lens. Every one was written in English in a file every worker read.

**The test is not density. It is whether a reader could recover the sentence from the code
alone.** If they could, it goes. If it took a measurement, a decision or a device to learn,
it stays — and if it took a measurement, put the number in it.

Worth keeping, and this repository is full of the good kind:

- `go build ./...` writes `./api` into the repo root — found because `git add -A` staged
  a 9 MB binary.
- The probe's own deadline must stay strictly below Docker's `--timeout`, or the
  diagnostic never reaches the health log.
- Composite `ON DELETE SET NULL` nulls the whole key including `traveller_id`; the
  column-list form is why this needs PostgreSQL 15.
- A mutation harness must restore by file copy, never `git checkout` — against an
  uncommitted tree that no-ops on untracked files.

Not worth keeping: anything restating a constant, a struct field, or what an obviously
named function does.

**MEASURED 23 August 2026, before any sweep: 8,125 lines, 1,718 comments — 23.4% of
non-blank.** The worst files are 43–66% (`httpx/errors.go`, `httpx/middleware.go`,
`logging/logging.go`). A sweep is scheduled for after the slice runs end to end rather
than mid-flight. Re-derive the number rather than remembering it; do not trust this one
after the sweep.

For comparison, the client sits at 30.5% and a sweep against this same rule found exactly
**one** removable comment there — so a high ratio is not by itself the defect. The
difference is what the comments are carrying.

### No comments inside a declaration

**Comments go ABOVE the thing they describe, never inside it.** No comment inside a struct
body, an interface body, a const or var block's braces, or a function body. Stated by the
human for this project, and previously for the client project in the same words: *"I hate
comments inside classes or functions. Put them outside of them."*

What stays: the doc comment above a declaration — required for exported identifiers, and
what a reader and `go vet` both expect. Package comments. Build constraints and `//go:`
directives, which are not comments in the ordinary sense.

**What to do with a sentence that wanted to be inside a function.** Two answers, and the
second is usually better:

1. Move it to the doc comment, where a reader meets it before the code rather than halfway
   down.
2. **Let the code absorb it** — a named constant, a named helper, an intermediate variable
   whose name is the sentence. A line needing a comment to be legible is usually a line
   wanting a name.

The second is why this rule improves code rather than merely relocating prose. A comment
inside a function is a note to whoever is already reading that line; a doc comment is a
promise to whoever is deciding whether to call it at all. They are not the same
readership, and only one of them can be served from inside the braces.

Note the interaction with the rule above: moving a comment out is not a licence to keep it.
Most comments that lived inside a function body were restating the line beneath them, and
those go rather than move.

---

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

## VS6 — Argon2id, opaque tokens, register and sign in

**TEST-FIRST** (agent-graph-spec-V4 §6.7). Every leg below was written and
watched to fail before the code it names existed, and every one was reddened
again by its own mutation afterwards. Nothing here is reported as proven that
was not observed failing.

`make check` **exit 0**, **363 passing legs and 16 skips** — re-derived, not
remembered: `go test ./... -count=1 -v | grep -c -- '--- PASS'`, with
`TEST_DATABASE_URL` exported. The 16 skips are `test/image`, which is gated on
`TRAVELLOG_IMAGE_TESTS=1` and a Docker daemon.

### THE GATE DID NOT PASS AT `62a7821`, AND THAT IS THE FIRST FINDING

The handoff records `main @ 3102786, 219 tests, gate green`. **VS5's `tx.go`
landed at `3baa425`, which is AFTER that commit**, and it shipped without
`defer tx.Rollback()` in `WithTravellerTx`. Every one of that function's four
error exits — begin's lock, the bump's `ErrNoRows`, the bump's driver error,
and the body's own error — returned with the transaction still open. The
pooled connection is never checked back in, the session sits `idle in
transaction` for the life of the process, and it keeps the traveller's
advisory lock and every row lock the body took.

Measured on the committed tree, one leg, alone:

```
$ go test ./internal/postgres/ -run TestWithTravellerTxRollsBackTheBumpWhenTheBodyFails -timeout 20s
=== RUN   TestWithTravellerTxRollsBackTheBumpWhenTheBodyFails
panic: test timed out after 20s
goroutine 37 [chan receive]:
database/sql.(*Tx).awaitDone(…)
```

The whole package: `FAIL travellog/internal/postgres 600.893s`. With the
defer: `ok … 13.168s`. So `219` cannot be re-derived at `62a7821` — nothing
can, because the run does not finish.

**The lesson is bigger than the fix, and it is about the two legs that already
existed.** `TestWithTravellerTxRollsBackTheBumpWhenTheBodyFails` and
`TestWithTravellerTxRollsBackAPanickingBodyAndDoesNotStrandTheLock` are both
written against exactly this failure and **neither could say so**. Both assert
through a SECOND connection, and a `SELECT` reads the pre-commit snapshot
happily under MVCC — so both PASSED their assertions and then hung in
`testdb`'s cleanup, where `DROP SCHEMA … CASCADE` waits on the stranded
transaction's relation lock. **A guard whose only failure mode is a
ten-minute timeout is a guard somebody eventually deletes**, because the
report it produces names the harness rather than the defect. Two legs were
added that give it a name — `db.Stats().InUse` after a failed body and after a
bump that found no traveller — and they redden in **0.78s** with the sentence
`1 connection(s) still checked out after a failed write, want 0`.

That is a new entry in the defect classes: **an assertion that reads through a
different connection cannot see an open transaction.** The read succeeds, the
value is right, and the resource is still held.

### The module graph after `go mod tidy`

Re-measured rather than carried, Go 1.26.5 darwin/arm64:

```
$ go mod tidy && grep '^go ' go.mod        -> go 1.25.0
$ go list -m all | wc -l                   -> 20
$ go mod edit -go=1.25 ; go build ./...    -> 1   go: updates to go.mod needed
                       ; go vet   ./...    -> 1   go: updates to go.mod needed
```

`golang.org/x/crypto v0.55.0` is the addition. **It did not arrive alone**:
tidy also raised `golang.org/x/sync v0.17.0 → v0.22.0`, `golang.org/x/text
v0.29.0 → v0.41.0`, and pulled in `golang.org/x/sys v0.47.0`. Five modules in
the graph now declare `go 1.25.0` — pgx, x/crypto, x/sys, x/sync and x/text —
so **VS1's open question is answered twice over and its own correction stands**:
VS2 measured that pgx alone already forced the floor, and x/crypto forcing it
too changes nothing about the hazard, which is re-editing the directive DOWN
after tidy has settled it.

`golang.org/x/crypto` is the **seventh package** in `pubspec`-terms — the count
convention this file settled at phase 7 is *packages, runtime halves included*.
It needed no new conversation: DEC-08 named Argon2id and named the library.

### What was decided, and what each was decided against

- **The hash is of the RAW bytes and not of the base64 text**, and the leg
  asserts the negative as well as the positive. Both are 32 bytes, both
  round-trip, both are unique per token, and sign-in works either way: there is
  **no observable difference inside this system at all**. The difference is
  that only one of them is what spec L24 says, and the day somebody
  re-implements either half against L24 the two stop agreeing with nothing
  anywhere to explain it.

- **`SameHash` checks the length itself**, which is not redundant with
  `subtle.ConstantTimeCompare`'s own. Measured: that function answers **1 for
  two EMPTY slices**, which is the documented behaviour of an equal-length
  comparison of nothing. Without the guard a nil digest — from a caller that
  ignored an error — authenticates against a nil column read.

- **`HashToken` refuses a token of the wrong length, and that is the only
  place the wire shape exists as a rule.** A one-byte token base64-decodes
  cleanly, hashes to a perfectly good 32-byte digest, satisfies
  `sessions_token_hash_sha256_ck` and compares without complaint.

- **Argon2's parameters travel with the hash, and `Verify` reads them out of
  the string rather than off the struct.** That is what makes DEC-21's
  deferred tuning deferrable: raising the memory cost otherwise recomputes a
  different key for every traveller already registered and locks all of them
  out at once. The leg is a triangle — a hash written at `m=16384,t=3,p=2`
  must verify under a hasher configured `m=65536,t=1,p=4` AND under one
  configured `m=8192,t=2,p=1`.

- **`Verify` validates before it computes**, because `argon2.IDKey` PANICS on
  parameters no caller would choose but a corrupt row can hold. Measured on
  x/crypto v0.55.0: `t=0` panics `argon2: number of rounds too small`, `p=0`
  panics `argon2: parallelism degree too low`, and **a zero-length key does
  something worse than either** — `blake2b.New(0, nil)` fails, argon2 does not
  check it, and the call dies on a **nil pointer dereference inside
  `blake2b.(*digest).Write`**. One hand-edited row would otherwise take a
  goroutine's stack out through a 500.

- **Every parsed field is re-rendered and compared rather than scanned and
  trusted.** Measured: `fmt.Sscanf("m=8192,t=2,p=1,evil=1", "m=%d,t=%d,p=%d")`
  answers `n=3` and **no error**, silently discarding the tail.

- **The gate refuses zero, and refuses it in both directions of the mistake.**
  A zero-capacity buffered channel is an UNBUFFERED one, so the first login
  blocks for ever rather than being refused — which is the reading DEC-48
  exists to prevent. And a negative capacity is worse: measured,
  `make(chan struct{}, -1)` **panics `makechan: size out of range`** and takes
  the process down at construction. `config.Load` already floors
  `ARGON2_MAX_CONCURRENT` at 1; `NewGate` is the half that holds for a caller
  who is not config, and `apiRoutes` is a third.

- **Both halves of the Hasher are capped.** `Verify` calls `argon2.IDKey`
  exactly as `Hash` does and costs the same 64 MiB; capping `Hash` alone caps
  register and leaves sign-in — the route reachable without creating anything
  — open.

- **Every slot is returned with `defer`.** A run of malformed stored hashes, or
  one panic out of the KDF, would otherwise leak a slot per call until the gate
  is wedged shut and every login afterwards is a 429 with nothing to explain
  it.

- **THE ENUMERATION ORACLE IS CLOSED IN BOTH ITS FORMS, and the second is the
  one a status assertion cannot see.**
  - *The message.* One sentinel, one text, and the leg asserts `errors.Is` as
    well as the string: two sentinels sharing a string still tell a caller
    which is which.
  - *The clock.* Returning early on an unknown address skips ~64 MiB and tens
    of milliseconds of Argon2, which is measurable from the outside on every
    attempt. An unknown address is verified against a real hash of a passphrase
    nobody holds, and the leg **counts Verify calls** rather than timing them —
    a timing leg would be a flake on a loaded machine and a pass on a fast one.
  - The decoy is computed **eagerly, at package init**, and **derived from
    `DefaultParams`**. Computing it lazily leaves exactly one attempt per
    process paying an extra Argon2 call — a one-shot oracle rather than a
    per-attempt one, which is better and is still not none. Writing it as a
    literal would leave it cheaper than the real thing the day DEC-21's tuning
    lands, putting the timing oracle back **with every leg still green**.
  - The wire comparison is on **bytes**, not on a decoded map:
    `{"code":"unauthenticated"}` and `{"code":"unauthenticated","field":""}`
    carry the same meaning and are two different answers. The **headers** are
    compared too, minus `X-Request-Id` — a `Content-Length` differing by one is
    the same oracle wearing a different hat.

- **REGISTER IS DELIBERATELY NOT LIKE THAT.** DEC-60's surviving leg asks for a
  **409** on a second registration of one address in another casing, so
  register reports that an address is taken **by design**. The two are not
  inconsistent: whether an address is available is a fact a sign-up form has to
  report, and whether somebody's passphrase was right is not.

- **NOTHING IN GO LOWERCASES AN EMAIL ADDRESS** (DEC-65). The folding is in
  SQL, in one place, enforced by `travellers_email_lower_key`. DEC-60's rule
  that exactly two call sites must remember is precisely the rule one of them
  eventually forgets; the index needs neither to remember. The address is
  stored **as typed**, because RFC 5321 makes the local part case-sensitive.

- **The unique violation is read from the ABSENCE of a `RETURNING` row**, not
  from the driver's `23505`. Reading the code would mean importing `pgconn`,
  and `cmd/api`'s import sweep asserts pgx is imported exactly once, blank, in
  main — spec L20's "solely as a blank import driver". `ON CONFLICT
  (lower(email)) DO NOTHING RETURNING` answers the same question in SQL, and
  naming the expression rather than a bare `DO NOTHING` means a future second
  unique constraint on this table cannot be silently swallowed as "that address
  is taken".

- **`gen_random_uuid()` rather than a uuid package.** Core PostgreSQL since 13,
  and this schema's floor is 15 (DEC-66), so it needs no extension — which is
  the whole reason it beats a dependency. DEC-02's slug rule is about the
  CLIENT's ids and does not reach travellers or sessions.

- **`TouchSession` checks the row count**, which `WithTravellerLock`'s
  existence check cannot do for it: an `UPDATE` matching nothing reports
  success, so a session that has been deleted or belongs to somebody else would
  keep authenticating for as long as the caller believed the answer.

- **A traveller who is gone answers `auth.ErrNoSession`, not
  `auth.ErrNoTraveller`.** This is reached from `Authenticate`, where the
  honest report is that the credential is not live — a 401. Reporting "no such
  traveller" would make a deleted account a 500.

- **A 401 and a 500 are different answers and the difference matters to the
  client.** A credential that is not live is a 401 and the phone's answer is to
  sign in again. A database that has gone is a 500 and the phone's answer is to
  wait — reporting it as a 401 would have the client discard a perfectly good
  session it cannot get back. Same argument inside the service: a busy Argon2
  gate is a 429 and never bad credentials, because retrying with a different
  passphrase is the wrong advice.

- **Detail goes to the log only on the branch that is a server fault.** A wrong
  passphrase is not an error condition: logging one at ERROR per attempt turns
  an ordinary typo into an alert and a password-spraying run into a flood that
  hides the thing worth seeing.

- **`Mount` PANICS on a nil limiter rather than reading it as "no limit".**
  DEC-48 is a ruling, and a ruling like this regresses silently — an optional
  field left unset reads as working software and removes the only bound on
  unauthenticated Argon2 work. Failing at wiring time is loud and cannot reach
  production.

- **`DefaultSessionTTL` is thirty days and is UNTUNED**, in the same sense
  DEC-08's parameters are: nothing has measured it against anything. It suits
  a phone that keeps its token in the platform keychain (DEC-45) and has no
  refresh flow, and there is no revocation surface yet. It is a named constant
  rather than an eighth environment variable, because an eighth variable is a
  change to `config.Load`, `deploy/.env.example` and
  `deploy/docker-compose.yml` that this step was not asked for; the step that
  needs it per-deployment is the step that should add it.

- **`MinPassphraseBytes = 8` is this build's own policy and the weakest thing
  on the list.** It is written as a constant so that raising it is one constant
  and one leg. The 254-byte address ceiling is the wire's (RFC 5321) rather
  than a policy, and the 1024-byte passphrase ceiling is a DoS bound: Argon2's
  cost is independent of input length, but a 10 MB field is still 10 MB to read
  and copy.

### DEC-50's one exception is settled, and VS5's prediction about it was wrong twice

VS5's `tx_sweep_test.go` carried an allowlist entry for
`internal/rest/auth_handlers.go`, predicting that `POST /v1/auth/register`
would open a transaction it could hand to neither helper.

1. The package is **`internal/httpapi`**, not `internal/rest` (DEC-74), so the
   entry named a path nothing would ever occupy — and
   `TestNoAllowlistEntryIsStale` tolerates entries for files that do not exist
   yet, so it would have sat there indefinitely.
2. Register's write is **one INSERT, which is already atomic**. It opens no
   transaction at all, so the sweep never sees it and no exemption is needed.
   An allowlist entry grants an exemption; **an exemption nothing uses is a
   hole with a comment over it.**

The entry is gone. `TestTheRegisterExemptionIsWrittenDown` — an artefact check,
labelled as one by its author, which could only fail if somebody edited the map
— is replaced by two AST legs over `auth_store.go` that fail on the **code**:
`CreateTraveller` calls neither helper and neither `Begin` nor `BeginTx`, and
`CreateSession`/`TouchSession` call `WithTravellerLock` and never
`WithTravellerTx`.

**The membership claim still has a behavioural guard beside the syntactic one,
and the behavioural one is the evidence**: creating a session and five touches
move `logbook_version` by **zero**. Route `CreateSession` through
`WithTravellerTx` and it reads `0 -> 1` — the mutation the slice's own
definition of done names.

### Divergences from the step's file list, each deliberate

| the step said | what was built | why |
|---|---|---|
| `internal/api/auth_handlers.go` | `internal/httpapi/auth_handlers.go` | DEC-74 supersedes the `internal/rest` half of DEC-71 |
| `internal/auth/middleware.go` | `internal/auth/bearer.go` **plus** `RequireTraveller` in `internal/httpapi` | DEC-62 asks for ONE mapping from sentinel to wire code; a 401-writing middleware in `internal/auth` is a second place in the tree that knows DEC-12's vocabulary. `internal/auth` imports no httpx at all |
| (no route to protect) | the middleware's legs mount **their own** probe handler | VS7 builds the first authenticated route. Mounting one in `lib/` here would be VS7's work arriving early |

`cmd/api` also grew a `serverChain`, and **it is `httpx.Base` MINUS Timeout,
stated rather than silent**: `Timeout(d)` takes a duration `internal/config`
does not read — there is no `REQUEST_TIMEOUT` — and inventing one here is a
configuration value nobody chose, silently in force, which is the thing
`config.Load` exists to refuse. `Recover`, `RequestID` and `AccessLog` are
wired now because the auth routes are the first thing here a client can reach
with a body: without `Recover` a panicking handler closes the connection with
no response at all. This is the first caller of `internal/httpx` in `lib` code
— the "no caller" line under **Inherited unfinished** is now half true, and the
`/healthz` newline question it names is still open because `Timeout` is still
out.

`newMux`'s third parameter is **variadic**, and that is what kept twelve legs
from being rewritten: it is called from a dozen places in `cmd/api`'s tests,
every one about `/healthz` and none about auth, and a second required parameter
would have edited all twelve to pass a nil they do not care about.

### Two survivors, argued rather than left unmentioned

- **`RequireTraveller`'s `if !held` branch.** Deleting it — reading the token
  with `_` and letting `Authenticate` answer — leaves
  `TestAnAuthenticatedRouteRefusesEveryShapeOfMissingCredential` **green**,
  measured. The two paths reach the same 401 by different roads: one refuses to
  READ a credential, the other refuses to ACCEPT one, and an absent header
  arrives at the second as the empty string, which `HashToken` rejects on
  length. The branch is kept because **the redundancy is between FILES**:
  without it, "no Authorization header at all" is answered correctly only
  because `auth/token.go` refuses a zero-length token — a property of another
  package, guarded by another leg, whose loss nothing here would notice.

- **A leg whose name promised more than it could see.**
  `TestSigningInResolvesTheAddressInAnyCasing` in `internal/httpapi` cannot see
  casing: the fake store folds case on lookup exactly as
  `travellers_email_lower_key` does, so `strings.ToUpper(body.Email)` at the
  SignIn call site left it **green** — measured. It is renamed
  `TestRegisterThenSignInGoesThroughTheRealChain` and says in its own comment
  what it proves and what it does not. DEC-65's rule is guarded where the index
  is, in `internal/postgres`, in both directions and with `EXPLAIN` as the
  ruling asks.

### The mutations

Every one changed the file (verified by sha256 before and after), every one was
restored **by file copy and never through git** — the VS2 incident is why — and
each restore was verified byte-identical. Skeleton-first legs are counted
separately from the ones that needed a mutation of their own, because **N
mutations covering M legs looks like coverage and is not**.

Counts re-derived rather than remembered: `grep -c '^func Test' <file>`.

| test file | legs | reddened by the skeleton | needed their own mutation |
|---|---|---|---|
| `internal/auth/token_test.go` | 9 | 5 | 4 |
| `internal/auth/hasher_test.go` | 10 | 5 | 5 |
| `internal/auth/gate_test.go` | 8 | 4 | 4 |
| `internal/auth/service_test.go` | 20 | 13 | 7 |
| `internal/auth/bearer_test.go` | 6 | 2 | 4 |
| `internal/postgres/auth_store_test.go` | 18 | 6 | 12 |
| `internal/httpapi/auth_handlers_test.go` | 16 | 7 | 7 (+2, below) |
| `cmd/api/routes_test.go` | 6 | 4 | 2 |
| `internal/postgres/tx_test.go` (added) | 2 | 2 | — |
| `internal/postgres/tx_sweep_test.go` (added) | 2 | — | 1 (P-M7 reddens both) |

**97 legs, and the two `+2` in `httpapi` are the two argued above** — the
`if !held` survivor, and the renamed leg that could not see what its first name
claimed. `tx_sweep_test.go` also LOST one: the artefact check
`TestTheRegisterExemptionIsWrittenDown`.

**Four mutations are build failures rather than red assertions**, and that is
recorded rather than glossed: renaming `Argon2id.Verify`, `Capped.Verify`,
`AuthStore.TouchSession` or `Capped`'s interface satisfaction does not make a
leg report a wrong answer, it makes the package stop compiling with
`… does not implement …`. That is still exit 1 and it is the right red: an
interface and its only implementation drifting apart is a fact about types.

**One mutation changed the file and not the behaviour, and had to be
rewritten.** Adding a comment inside a SQL constant string in `auth_store.go`
left the suite green — a green suite proving nothing, exactly as the standing
rule says. The replacement writes `name` into the INSERT and reddens with
`the name column holds "matt@example.com"`.

**Two mutations reddened as HANGS rather than as assertions**, both before the
`WithTravellerTx` fix and one after: a queueing gate makes the N+1th caller
wait, and a stranded slot makes the probe wait. Where a hang was the only
available red, the leg was rewritten to answer from a goroutine behind a
`select` with a deadline, so the report names the defect instead of the
harness.

### What VS6 leaves guarded by nothing

Unchanged from VS2 and repeated so the list does not shorten by silence: the
Dockerfile's CA bundle, `time/tzdata`, the numeric `USER`, the named volume,
and the fact that `deploy/.env.example` and `deploy/docker-compose.yml` are not
checked against `config.Load`'s variable list. New at VS6:

- **The Argon2 parameters are guarded as VALUES and not as a CHOICE.** A leg
  asserts `DefaultParams` is 64 MiB / t=1 / p=4 with a 16-byte salt, so the
  numbers cannot move unnoticed. **Nothing measures whether they are the right
  numbers on any machine** — that is DEC-21, and it is deferred to a real box
  with a real login rate.
- **`DefaultSessionTTL` has no leg about its VALUE at all**, only about the
  arithmetic that applies it. Thirty days is a choice nothing has examined.
- **Nothing asserts the rate limit is per CLIENT ADDRESS.** The limiter keys on
  `RemoteAddr` (VS3's deferral, unchanged), and every request in these legs
  arrives from loopback, so a limiter keyed on a constant would pass every leg
  here. `httpx.ClientKey` has its own legs; the *composition* has none, and the
  leg that would settle it is the one that arrives with Caddy and
  `X-Forwarded-For`.
- **`internal/seed` still has no test files**, and it is a `wip` commit that
  predates this step.

### The routes are NOT in the running container, and that is on the record

`make check` is green and the routes are exercised over the real
`http.ServeMux`, the real middleware chain and a real PostgreSQL — but **the
image was not rebuilt in this session**, so `docker compose ps` shows an `api`
container from before VS6 and `POST /v1/auth/register` against
`127.0.0.1:8080` answers **404**. The build stops at its first step:

```
#1 [api internal] load build definition from Dockerfile   DONE 0.0s
#2 [api] resolve image config for docker-image://docker.io/docker/dockerfile:1
   (no progress; killed after ~15 minutes)
```

`deploy/Dockerfile`'s `# syntax=docker/dockerfile:1` line makes BuildKit fetch
that frontend from Docker Hub on every build, and **egress to
`registry-1.docker.io` did not answer here**. It is an environment fact rather
than a defect in the Dockerfile, and it is written down for two reasons: the
next worker must not read `Up (healthy)` as "the auth routes are live", and
**VS8's arc cannot run at all until that fetch succeeds** — the arc's first
request is `POST /v1/auth/register`. `docker compose build api` once, on a
machine with Hub reachable, is the whole of it. If it stays unreachable,
pinning the frontend to a digest already in the local cache — or dropping the
`# syntax` line, which costs the heredoc and mount syntaxes the file uses — is
the conversation.

---

## VS7 — one conditional read, one trip write, and the shape the client already decodes

**TEST-FIRST** (agent-graph-spec-V4 §6.7). Every leg below was written and
watched to fail before the code it names existed, and every one was reddened
again by its own mutation afterwards. Nothing here is reported as proven that
was not observed failing.

`make check` **exit 0**, **462 passing legs and 16 skips** — re-derived, not
remembered: `go test ./... -count=1 -v | grep -c -- '--- PASS'`, with
`TEST_DATABASE_URL` exported. VS6 left 363. The 16 skips are `test/image`,
unchanged.

**73 new top-level legs and 71 mutations.** Counts re-derived rather than
carried: `grep -c '^func Test' <file>` over `internal/httpx/mux_test.go` (5),
`internal/logbook/emit_test.go` (15), `internal/logbook/validate_test.go` (6),
`internal/postgres/logbook_store_test.go` (15),
`internal/httpapi/logbook_handlers_test.go` (25),
`internal/httpapi/routes_test.go` (5), plus two added to files that already
existed (`cmd/api/routes_test.go`).

### THE DEFECT RUNNING IT FOUND THAT NO LEG DID

The gate was green, 71 mutations had been run, and then the binary was pointed
at the real database on 127.0.0.1:5434. `PUT /v1/trips/kyoto` answered:

```
{"id":"kyoto","name":"Kyoto in May","cityIds":null,"start":"2027-05-12T00:00:00.000Z",…}
```

`null`, and `trip.g.dart` reads `(json['cityIds'] as List<dynamic>)` with no
null branch — so the client throws on the answer to its own write. **The GET
was correct the whole time**: `Emit` normalises nil slices, and the write path
answers a bare entity that never goes through it. One rule, two paths, and only
one implementation of it. `logbook.EmitTrip` is the rule as a named thing both
paths call.

**Two legs could not see it, and the second is the sharper lesson.** The splice
leg appends the returned trip to a document and re-emits, so `Emit` repaired it
on the way. And the *first draft of the new leg* sent `"cityIds":[]` in the
body — which decodes to an empty NON-NIL slice and marshals as `[]` with or
without the fix. Measured: it survived its own mutation. **A leg about a nil
slice has to OMIT the key**, because an empty JSON array and an absent one are
two different values on the Go side and only one of them is the bug.

The general form is the one this project keeps re-learning: a green suite
cannot tell a guard from a decoration, and neither can a suite that has never
been pointed at the real thing.

### What is in it

- **`internal/logbook`** — the domain. `types.go` (the wire shape and
  `Instant`), `emit.go` (the one emitter and the two version constants),
  `validate.go` (the compiled regexps and `TripWrite`), `store.go` (the
  contract `internal/postgres` satisfies).
- **`internal/postgres/logbook_store.go`** — the six queries one read is made
  of, and the whole-state upsert.
- **`internal/httpapi`** — `routes.go` (DEC-28's declared table) and
  `logbook_handlers.go` (the tag, the condition, the status).
- **`internal/httpx/mux.go`** — the two responses `http.ServeMux` writes for
  itself, brought inside the envelope.

### THE ROUND TRIP IS THE STRONGEST LEG, AND WHY

`TestTheClientsOwnLogRoundTripsThroughTheseTypes` decodes
`internal/logbook/testdata/client_sample_log.json` — the 85,422-byte document
the Flutter app's own encoder produced before its fixture was deleted (DEC-75)
— into these Go types, emits it back, and asserts the two are equal value for
value. Seven trips, twelve cities, seventeen places, 49 visits, 284
photographs, two walks. **The reference was not written beside the code that
has to satisfy it**, which is the whole of its strength: it proves every key,
every date string and every number at once, against bytes neither this package
nor its tests authored.

Everything below it — the golden key file, the per-field date legs — exists
because the round trip cannot say *which* thing broke. The golden is checked in
and a second leg asserts **the golden IS the client's key set**, so a golden
regenerated to match a mistake reddens too (mutation L11).

### The dates, and the six fields DEC-68 asks about

`Instant` renders `2027-12-06T07:05:00.000Z`. Dart's `toIso8601String()` writes
milliseconds unconditionally; Go's `time.Time` marshals RFC 3339 with trailing
zeroes REMOVED. Both parse, only one is byte-identical to what the client sent,
and the three `date` columns — `trips.started_on`, `trips.ended_on`,
`walks.recorded_on` — must come back as `T00:00:00.000Z` or `DateTime.parse`
hands the client a local time for those three and a UTC one for every other
date in the log.

**Two measurements came out of writing those legs.**

- **All 284 photographs in the client's log carry `"filedLater": null`.** So
  the sixth date-bearing field has no fixture, the leg that would have compared
  it says so with `t.Fatalf` rather than passing vacuously, and a synthesised
  leg covers it — written against the string a Dart encoder produces, not
  against what Go happens to do.
- **Mutation L2 — deleting `.UTC()` from `MarshalJSON` — left the whole package
  green.** Every leg built its `Instant` through `At`, which converts, so the
  legs proved `At`'s conversion and never the marshaller's. A store scanning a
  `timestamptz` may reach for the bare conversion, which `At` does not protect.
  `TestAnInstantBuiltByConversionIsStillRenderedInUTC` is the one leg that
  closes it, and L2 reddens it now.

**And one thing measured before relying on it:** `city.g.dart:18` reads a
coordinate as `(json['lat'] as num).toDouble()`, not `as double`. So a
whole-numbered latitude emitted by Go as `35` rather than `35.0` decodes
correctly, and `encoding/json`'s shortest round-tripping form is safe. A bare
`as double` would have made that a defect.

### The 304, and the shape of the interface it forced

`logbook.Store.Read` takes a **callback** rather than answering a document:

```go
Read(ctx, travellerID string, assemble func(version int64) bool) (Snapshot, error)
```

Two facts make that the only honest shape. The version and the document must
come out of ONE repeatable-read snapshot, or the phone stores a torn body under
a number describing a different moment; and the decision to assemble belongs to
the HANDLER, which is what holds `If-None-Match` and DEC-49's emitter version.
A two-call interface cannot keep both — the second call is a second snapshot —
and returning `(version, document)` always assembles. `Snapshot.Document` is
nil on the 304 path, so "the 304 does not assemble the document" is a fact
about the type rather than a claim.

**It is proven twice, at two tiers, and the store's proof needed its instrument
changed.** At the handler, an instrumented fake counts assemblies: three
revalidations after one 200 leave the count at one. At the store, the five
entity tables are **DROPPED** and the refused read still succeeds, with an
assembling read as the control. `pg_stat_all_tables` was the first attempt and
is the wrong instrument — its counters are collected asynchronously and cached
per transaction, so the leg would have been a flake pretending to be a
measurement. A table that is not there cannot be read from by accident.

### Decisions taken, each against a real alternative

- **A traveller at `logbook_version` 0 is served 200 with NO ETag.**
  `FormatETag` panics on a zero half deliberately, and `W/"1-0"` is precisely
  the one-half tag DEC-49's first half exists to prevent. `ETagMatches` then
  refuses every `If-None-Match` including `*`, which is the right answer: a 304
  against a log the client has never held hands it an empty body it reads as
  unchanged — DEC-49(b)'s permanently empty app, arriving by a third route. The
  alternative was a migration moving the column's DEFAULT to 1, which is one
  line and changes what the counter MEANS, and would redden VS6's own
  bump/no-bump legs. Declined.
- **A tag from another emitter does not revalidate**, and it has its own leg
  (`If-None-Match: W/"99-7"` against data version 7 answers 200). That is the
  slice's "bumping the emitter constant alone invalidates a cached client",
  expressed as a request rather than as arithmetic about a constant.
- **The 405 keeps its status and its `Allow` header, and its body says
  `not_found`.** Rewriting it to a 404 would make the status and the
  vocabulary agree at the cost of telling a client the path does not exist when
  the mux has just listed the methods it takes. A THIRTEENTH word
  (`method_not_allowed`) is refused by DEC-12: the block is closed, and a 405
  is a client that disagrees with the API rather than a condition a user can be
  told about.
- **The mux wrapper decides on the Content-Type, not on the status alone.**
  `http.Error` sets `text/plain; charset=utf-8` before `WriteHeader` and
  `WriteJSON` sets `application/json` before it, so by the time the wrapper is
  asked the two are distinguishable — and a handler's own 404 keeps DEC-12's
  `field`. Measured on Go 1.26.5: `GET /nope` → `404 text/plain "404 page not
  found\n"`; `POST /v1/logbook` → `405 text/plain "Method Not Allowed\n"` with
  `Allow: GET, HEAD`.
- **The path id wins and a disagreeing body id is 422 on `id`.** A body with no
  id is the ordinary case — the path already carries it, and that is what makes
  the route an upsert on a client-minted key (DEC-33). A body naming a
  DIFFERENT trip is a client that believes it is writing somewhere else, and
  honouring either half of that puts the write where nobody asked for it.
- **The write's existence checks are in the store, not in `ValidateTrip` and
  not left to the foreign keys.** Under the traveller's advisory lock the check
  is race-free, which is exactly what DEC-02 says the lock buys. Reading the
  violation back off the driver would mean importing `pgconn` for SQLSTATE
  23503, which `cmd/api`'s import sweep forbids (spec L20). Without them an
  unknown city is a 500 with nothing the client can show. **DEC-64 deleted the
  Go check that SUBSTITUTED for referential integrity; this is the one that
  names the field before the constraint fires, and the constraint is still what
  enforces it.**
- **`walks.points` is unnested in SQL, not decoded in Go.** The obvious answer
  — `json.Unmarshal` into `[]LatLng` — would make the store the SECOND non-test
  file importing `encoding/json`, which `internal/httpx`'s AST sweep asserts
  against (spec L19). `jsonb_array_elements … WITH ORDINALITY` answers the same
  question in SQL, and Postgres decoding its own jsonb is not payload encoding.
  It also keeps the ORDER explicit, where a Go decode would have inherited it
  silently.
- **Every list is `ORDER BY id`, and that is about determinism rather than
  display.** Two reads with no write between them must be byte-identical or the
  ETag is a claim the server cannot keep; the client sorts for display itself
  and always has. The two exceptions are the ordered lists the schema mandates:
  `trip_cities` by `ordinal` (DEC-64), and visits by `ordinal, id` — the second
  key so a pre-existing duplicate degrades to stable rather than random, because
  emitting visits in a different order silently rebinds a photograph to a
  different occasion (DEC-26).
- **The upsert does not NAME the three sharing columns**, in either the column
  list or the `SET` clause, so a create leaves them at their schema defaults and
  a rename leaves them exactly as they were (SF6). The type helps: `TripWrite`
  has no slot for them at all, and DEC-13 keeps unknown fields tolerated, so a
  client sending `shareCoordinates: true` is not refused — it is simply not
  heard.
- **The write's answer is RE-READ from the row.** The three flags are not in
  the body, so a response assembled from the input could only guess at them —
  and a response assembled from the input is a response that agrees with the
  client about a write the database may have shaped differently. Mutation H7 is
  the slice's own named proof, and it reddens.
- **`!Auth` is exactly DEC-48's rate-limited set, derived rather than declared
  as a sixth field.** Every route in this table that does not require a
  traveller is a credential attempt; `/healthz` is unauthenticated, unlimited,
  and deliberately not in the table, because a liveness probe is not part of the
  API. `TestOnlyTheUnauthenticatedRoutesAreRateLimited` makes the derivation a
  fact rather than a comment. The day an unauthenticated route arrives that is
  not a credential attempt, this becomes a field.
- **`Mount` panics on a nil store**, for the reason it already panicked on a
  nil limiter: nil does not read as "no logbook", it reads as working software
  until somebody asks for their log and gets a 500 out of the recover
  middleware.
- **A traveller who has gone is 401, not 500.** The row can be deleted between
  the credential being accepted and the query running; the honest report is
  that the credential is not live and the answer is to sign in again. A 500
  would have the client wait for a server that is perfectly well. Same argument
  VS6 made for `ErrNoSession`.
- **Two ceilings are this build's own policy and are marked as such.**
  `MaxNameBytes = 200` and `MaxSummaryBytes = 4096`. Nothing in the schema
  bounds either — both are `text` — so without them a one-megabyte trip name is
  storable and then re-emitted on every read of the whole log, for ever. They
  are constants so raising one is one constant and one leg, exactly as
  `auth.MinPassphraseBytes` is.

### THE TWO VERSION NUMBERS ARE NOT ONE NUMBER, and the step text reads as if they might be

`emitterVersion` is **1** and the document's `version` is **2**, and both are
right:

| | what it names | where it goes | when it moves |
|---|---|---|---|
| `logbook.EmitterVersion` | the CODE that rendered the bytes | the first half of the ETag, never the body | every deploy that changes what this package emits |
| `logbook.FormatVersion` | the WIRE's shape | the body's `version` key (DEC-40) | a coordinated release, negotiated by DEC-53 |

VS7's step text says `"version": 2` in one sentence and `emitterVersion starts
at 1` in the next. They are not in tension and a reader in a hurry will think
one of them is a typo. Written down here so nobody "corrects" either.

**`Emit` takes the format version as a PARAMETER** and refuses one it cannot
write rather than falling back to the one it can — a fallback is DEC-40's
refetch loop wearing a 200. `Formats()` is what the 406 names.

### Three mutations that survived, and what each one bought

- **H10 — deleting the handler's early format gate — SURVIVED, and the gate
  stays.** `Emit` refuses the version on its own and `writeLogbookFailure` maps
  that to the same 406 with the same header, so the two paths agree on every
  byte the client sees. What they do not agree on is the WORK: without the gate
  the read opens a snapshot and builds the whole document before refusing it.
  `TestA406NeverAssemblesTheDocument` is the leg that says so, and H10 reddens
  it now.
- **L2 and H19/L12** are above: both were survivors that named a real gap and
  both are closed by a leg written for them.
- **H2 — writing a JSON body on the 304 — SURVIVED, and it stays a survivor
  with a measurement.** `net/http` refuses to write a body for a 304
  (`bodyAllowedForStatus`), so the bytes never leave the process. Measured
  through `httptest.NewServer` and a real `http.Client`: the body is empty with
  the mutation applied. **So the empty-body half of
  `TestAMatchingIfNoneMatchAnswers304WithAnEmptyBody` is the stdlib's guarantee
  rather than this handler's**, and the leg is reddened by H1 and H4 instead,
  which are about the status and the tag. Say which half of an assertion is
  yours.

**One leg no mutation reddened, argued rather than left unmentioned:**
`TestLogbookStoreIsTheStoreTheDomainDeclared`, a compile-time interface
assertion whose failure mode is `LogbookStore does not implement
logbook.Store` — exit 1 out of the build. VS6 settled that this is the right
red for a claim about types. `set(all_legs) − set(reddened)` is that one name.

### The arc, run against the real database without rebuilding the image

`docker compose build` was out of scope for this step and the running `api`
container still predates VS6. So the binary was built and pointed at the real
PostgreSQL on 127.0.0.1:5434 directly. Everything below is output, not
description:

```
GET  /v1/nope                    404 application/json  {"code":"not_found"}
POST /v1/logbook                 405 application/json  {"code":"not_found"}
POST /v1/auth/register           201
POST /v1/auth/session            201  token I11QgC_6…
GET  /v1/logbook  (version 0)    200, NO ETag
   {"version":2,"logbook":{"trips":[],"cities":[],"places":[],"photos":[],"walks":[],"traveller":null}}
PUT  /v1/trips/kyoto             200  ETag: W/"1-1"    (body carried shareCoordinates:true)
   psql: share_photos|share_notes|share_coordinates -> f|f|f
GET  /v1/logbook                 200  ETag: W/"1-1"    cityIds:[] , dates as T00:00:00.000Z
GET  + If-None-Match: W/"1-1"    304, ETag: W/"1-1", NO BODY (curl created no output file at all)
GET  + If-None-Match: W/"99-7"   200
GET  + X-Logbook-Format: 3       406  X-Logbook-Format: 2  {"code":"unsupported_format"}
GET  with no credential          401  {"code":"unauthenticated"}
```

The `f|f|f` line is the acceptance check: **a PUT body carrying
`shareCoordinates: true` left the stored flag unchanged.**

### Divergences from VS7's file list, each deliberate

| the step said | what was built | why |
|---|---|---|
| `internal/api/routes.go`, `internal/api/*_handlers.go` | `internal/httpapi/…` | DEC-74 supersedes the `internal/rest` half of DEC-71, and `internal/api` is the name DEC-71 renamed away from |
| `internal/api/testdata/logbook_keys.golden` | `internal/logbook/testdata/logbook_keys.golden` | it is the emitter's shape and it is asserted against `client_sample_log.json`, which is already there |
| (nothing) | `internal/logbook/store.go` | DEC-62's contract has to live with the domain, and the callback shape is a decision the read forced |
| (nothing) | `internal/httpx/mux.go` | the unknown-route gap, ruled to this step |

### What VS7 leaves guarded by nothing

Unchanged from VS6 and repeated so the list does not shorten by silence: the
Dockerfile's CA bundle, `time/tzdata`, the numeric `USER`, the named volume,
and the fact that `deploy/.env.example` and `deploy/docker-compose.yml` are not
checked against `config.Load`'s variable list. New at VS7:

- **The four unimplemented lists have no round trip through storage.** The
  emitter is proven against the client's own document, and `LogbookStore.Read`
  is proven to bring back what `PutTrip` wrote — but PutTrip only writes TRIPS.
  Nothing has ever put a city, a place, a visit, a photograph or a walk into
  PostgreSQL and read it back through the emitter, so the six read queries'
  column ordering, the visits nesting and the `jsonb_array_elements` unnest are
  each guarded by their scan compiling and nothing else. `make seed` (DEC-75) is
  what closes this, and it is the leg to write first when it lands.
- **`ORDER BY id` is asserted for `trip_cities` and for nothing else.** Two
  reads with no write between them are proven byte-identical only for a log
  holding one trip, because that is all a trip write can make. A city list that
  came back in a different order twice would pass every leg here.
- **Nothing measures the 1 MiB body ceiling against a real payload.** VS3 left
  that open for "VS7's real payloads", and the largest body VS7 takes is still
  one trip — far under it. The whole-log READ is the big one and it has no
  ceiling at all.
- **The emitter version is a constant a human must remember to bump.** There is
  a leg asserting it appears in the tag and a leg asserting it is 1; there is
  nothing that notices when this package changes shape and the constant does
  not. That is DEC-49's stated design and it is worth naming as the risk it is.
- **The route table's `Mutating` flag is read by no lib code.** A leg keeps it
  agreeing with its verb, which is the most a declared-and-unused field can be
  guarded by.

### The routes are STILL not in the running container

*(Closed at VS8. Left standing because the note is what made the gap findable,
and because the reason VS6 gave for the build failing turned out to be wrong —
see VS8 below.)*

VS6's note stands and now covers four routes rather than two. `make check` is
green, the arc above ran against the real database through the real binary, but
**the image was not rebuilt in this session** — `docker compose build` was
explicitly out of scope — so `docker compose ps` shows an `api` container from
before VS6 and `GET /v1/logbook` against `127.0.0.1:8080` answers **404**, in
plain text, from a build that predates `httpx.MuxErrors`. VS8's arc needs that
build to succeed; VS6 recorded the BuildKit frontend fetch as the thing that
stops it.

> **CLOSED, VS8.** `scripts/slice-arc.sh arc` builds the image as step A1 and
> then asserts, at A13, the four answers only the running container can give —
> the mux's own 404 inside the JSON envelope, the 405 with its `Allow` header,
> the 401 with no credential, and the 406 naming the formats. A 404 in plain
> text at any of them means an image from before VS6, which is what that
> paragraph was warning about and is now a red rather than a note.

---

## VS8 — the arc, the gate, and the evidence

**The slice is closed.** `make slice` runs the whole arc against the live stack
from a cold `docker compose down -v`, plus the four standing legs earlier steps
left explicitly for this one. `make check` is unchanged and still **4.4s**,
**462 legs and 16 skips** — VS8 added no Go legs, which is the right answer for
a step whose whole subject is the tiers `go test` cannot reach.

`make slice`: **exit 0, 76 assertions, 1m26s** on a warm image cache. The five
phases run cheapest-first, so a stale record fails in a second rather than
after a two-minute build: `record`, `gate`, `arc`, `testdb`, `healthcheck`.
Each is also a subcommand — `scripts/slice-arc.sh gate` — because a phase you
cannot run alone is a phase nobody debugs.

**`docs/EVIDENCE.md` exists**, which the plan has asked for since v1. It is the
mutation proofs, each run, at a stated commit, with the diff checked. Do not
restate its tables here: it is the file, and the reason this one has carried
four wrong counts is that a number lived in two places.

### The two `down`s are different commands and the script says so

`down -v` runs **once**, at the top: the volume goes, so the 201s that follow
are real creations rather than rows that were already there. `down` — no `-v` —
runs at the restart leg, and reading the trip back after it is the **only**
proof `pgdata` works. Swap the two and the restart leg passes while proving the
opposite.

**That is not hypothetical, and MU-A1 is the measurement.** Changing the mount
from `/var/lib/postgresql/data` to `/var/lib/postgresql/dataX` — which is
exactly what bumping `postgres:17` to `18` does, since 18 moved the image's
default `PGDATA` — left **every leg from A0 to A13 green**: register, sign in,
the write, the read, the 304, the 405, all of it. A15 answered **401**, because
the sessions table went with the trips. The latent trap the VS1 review flagged
and could not test is now caught, by the one leg that restarts the stack.

### The uppercased sign-in is the only thing in the arc that proves DEC-65

Two steps, and they prove different halves. **A5** registers the same address
UPPERCASED and expects **409** — that is the unique index on `lower(email)`
refusing it, not any Go code. **A6** signs in UPPERCASED and expects **201** —
that is `WHERE lower(email) = lower($1)` finding it. Lowercase either request
and both steps pass against a plain b-tree on `email`, so **the case is the
assertion**.

And the two mutations that look like one are not. `ON CONFLICT (lower(email))`
→ `ON CONFLICT (email)` reddens **A4**, not A5 — naming an index that does not
exist breaks the *first* register too, so it says nothing about the uppercase
one. Isolating A5 needs the schema and the statement moved **together**: a
plain unique index on `(email)` with `ON CONFLICT (email)`, under which both
registrations answer 201 and A5 is the only thing that notices.

### THE DEFECT RUNNING IT FOUND, AND IT WAS IN THE SCRIPT

VS7's lesson was that a green suite cannot tell a guard from a decoration. VS8's
is the same sentence one level up: **a green arc cannot either, and two of its
legs were decoration until they were broken on purpose.**

- **`curl -o` does not create or truncate its output file when the response has
  no body.** So `wc -c < body` after the 304 read the **previous** request's
  document — **333 bytes** — and the leg *passed*, because the number it wanted
  was 0 and it never got one. Had the handler started writing a body on a 304,
  this leg would have gone on passing. `req` removes both files before every
  request now, and `body_bytes` counts a file curl never created as zero. *An
  absence assertion is the easiest kind to write so that it cannot fail* — this
  project's own list, item 2 of the two smaller ones, arriving in a shell.
- **A JSON body written inline inside a quoted command substitution is not
  quoted.** `assert_eq 201 "$(req … -d "{\"email\":\"$X\",…}")" "…"` reaches the
  shell with the braces bare and **brace-expands**: curl ran twice with half an
  object each, the server answered `400 invalid_body` to both, and the failure
  was reported with the label `400` rather than the step's name. **The identical
  text as the right-hand side of an assignment parses correctly**, which is what
  made it look like a server defect for twenty minutes. `jq` builds every body
  now and every status is assigned before it is asserted on.

Neither is a defect in `cmd/` or `internal/`. Both would have made a leg pass
while proving the opposite, which is the only kind of test defect that matters.

### VS6's diagnosis of the build hang is WRONG, and here is the measurement

VS6 recorded `docker compose build api` stalling for ~15 minutes at
`resolve image config for docker-image://docker.io/docker/dockerfile:1` and
concluded that "egress to `registry-1.docker.io` did not answer here", leaving
"`docker compose build api` once, on a machine with Hub reachable" as the whole
of the fix. **Hub was reachable the whole time.** Measured at VS8 on the same
machine, while a `docker compose build` had been hung for ten minutes:

```
$ curl -o /dev/null -w '%{http_code} in %{time_total}s\n' https://registry-1.docker.io/v2/
401 in 0.168s                     # correct for an unauthenticated request

$ ps -ax | grep docker-credential
54821  10:23  docker-credential-desktop get      # hung for the whole build

$ echo 'https://index.docker.io/v1/' | docker-credential-desktop get
(no output)                       killed at 15s, exit 143

$ DOCKER_CONFIG=<copy of ~/.docker with credsStore deleted> docker compose build api
 Service api  Built                8.6s
```

The BuildKit frontend fetch **is** where it stalls, and the reason is the
credential helper Docker Desktop installs, not the network. `docker_preflight`
in `scripts/slice-arc.sh` probes the helper with a ten-second deadline before
the build, changes nothing when it answers, and prints all of the above plus a
live Hub timing when it does not — then continues against a `credsStore`-free
copy, because every image this project pulls is public. The helper answered
again half an hour later, so the condition is intermittent, which is the worst
kind to inherit a wrong explanation for.

### The four standing legs, and what each one is

All four were named in "What VS1-FIXES leaves for VS8" and all four are now
written. Two of them needed a shape nobody had specified.

- **G2, the gate's parse-error leg.** `make check` against `.tools/broken.go` in
  a **copy** of the repository: exit 2, "cannot PARSE". It is here rather than
  in a Makefile target because a test invoking the gate from inside the gate is
  circular, and that is why `Makefile` still reads `test_strategy: none`. G3
  keeps VS1's own misformatted-file mutation as the control, and under MU-G1 —
  the `ee543b9` recipe restored — the two branches separate exactly as
  VS1-FIXES said: **unparseable alone gives `make exit=0`, misformatted gives
  `make exit=2`.**
- **H1/H2, the healthcheck/TCP agreement leg, and it is DIFFERENTIAL by
  necessity.** The window on a normal cold start was measured at 0.33s against a
  3s interval, so a poll that simply never disagrees proves the poll is too slow
  just as well as it proves the fix. H1 therefore runs the **defect** — socket-only
  `pg_isready`, through a compose override, against a 15-second init script —
  and **fails if it does not disagree**: 16 samples, **10** with
  `docker=healthy` while TCP refused. H2 runs the shipped `-h 127.0.0.1` recipe
  under the same init: 17 samples, **0**. H3 is the budget leg VS4 asked for —
  every compose healthcheck's timeout below its interval, which is the rule the
  api image's own test enforces and the compose file shipped violating.
- **T1–T3, `make test-db`.** Under `POSTGRES_PORT=5999 POSTGRES_USER=alice
  POSTGRES_DB=otherdb` and its own project name, so it can never disturb
  `make up`. T1 and T2 are string comparison against `compose port postgres
  5432`; **T3 is what makes it evidence** — the URL the target prints opens a
  session and answers `alice@otherdb`.
- **R1–R3, the record checks, and they are labelled artefact tier.** R1 asserts
  every repo-relative path named in a **comment** exists, with an exemption list
  where the reason is written down — "the comment names it to say it never
  existed" is a reason, and `internal/rest` (DEC-74 renamed away), `internal/store`
  (VS5's predicted name), `deploy/.env` (a file that must NOT be in the tree)
  and `docs/DIVERGENCES.md` are the four. R2 asserts the `##` headings and
  `.PHONY` are the same set. R3 proves `make slice` propagates a non-zero exit
  **without recursing**, by running the target against two stub scripts through
  a `SLICE` variable.

**R1 found two stale citations on its first run and both were fixed rather than
exempted.** `internal/auth/bearer.go` called `internal/httpapi/middleware.go`
"the middleware" — a file that has never existed; `RequireTraveller` is in
`auth_handlers.go`. `internal/seed/seed.go` named `cmd/seed` as "the only entry
point" when DEC-75's command does not exist at all. And three places —
`deploy/docker-compose.yml`, `deploy/.env.example` and the `Makefile` — told a
developer to run `go test ./internal/store/...`, which matches no package in
this repository. That is VS1-FIXES finding 6 recurring three steps later, which
is the argument for the check rather than against it.

### And the seventh artefact check to go red against correct code

R2's first draft read the Makefile with `grep -oE '^## [a-z-]+'`, which matches
the **continuation** lines under a heading as well as the heading — it compared
`normally`, `without` and `three` against the target list and failed against a
perfectly good Makefile. The em dash is what separates a heading from its prose.
Phase 2 of the parent plan ran six of these; this is the seventh, and it landed
in the very step that exists to write them. **Write the artefact checks — they
catch a stale record cheaply — and expect the first draft of each one to be
wrong about the artefact rather than about the code.**

### What the inherited evidence turned out to be

Nine sections of this file carry mutation proofs. **Every one quotes real
output. Three state the commit they were run at; six do not.** That is recorded
as a finding rather than smoothed over — it is exactly what `docs/EVIDENCE.md`
exists to stop, and the inference "it was the step's own commit" is an
inference, not a statement.

Two hundred and forty-three mutations across five packages is not a step's work
to re-run, and re-running them without cause would be theatre. VS8 took a
**three-mutation sample** instead, one per tier that could regress silently, and
re-ran it at `cbb467a`. All three reddened with the text the record quotes: VS2's
`M-C` (the environment AST sweep), VS7's `L2` (the `.UTC()` survivor), and VS4's
`Q1` (the composite `ON DELETE SET NULL` blocker three review passes walked
past). The output is in `docs/EVIDENCE.md`.

### What VS8 leaves guarded by nothing

`docs/EVIDENCE.md` carries the whole list, in one place, for the first time —
including what VS8 moved **out** of it. The two entries that moved and are worth
naming here because they had stood since VS1: the named volume is now proved
**through the API** as well as by `test/image`, and `postgres:17` → `18` is not
asserted but **is caught**, because MU-A1 is that failure exactly.

New at VS8, and both are about this script rather than about the server:

- **The arc writes one trip and nothing else**, because that is all `PutTrip`
  can make. Every claim it proves about cities, places, photographs and walks is
  a claim about an empty list. `make seed` (DEC-75) is what changes that.
- **Nothing runs `make slice` but a human.** There is no CI, `make check` is
  deliberately fast and Docker-free, and the phases that need a daemon are
  opt-in. So the arc's evidence is only as fresh as the last time somebody ran
  it — which is the same tier `make test-image` has always been in, and it is
  worth saying out loud rather than letting a green file imply otherwise.

---

## VS8-SEC — every authenticated route had no ceiling at all

A security review found it, it is **built code rather than a plan defect**, and it
is one line of `Mount`:

```go
if route.Auth { handler = authed(handler) } else { handler = limited(handler) }
```

Rate limiting and authentication were applied as **either/or**. So the two
credential routes were limited and **the two authenticated routes were limited
by nothing whatever** — and the eighteen unbuilt routes of the parent plan would
each have arrived with the same hole, because the derivation in `routes.go` is
what decides the middleware.

### Measured against the running stack before the fix, at `7b47bee`

```
POST /v1/auth/session x15, one address    400 400 400 400 400 400 400 400 400 429 429 429 429 429 429
GET  /v1/logbook      x60, one token, -P 20        60 200
```

The credential ceiling bites after its burst; sixty concurrent authenticated
whole-log reads drew **no limiter at all**. Against DEC's thirty-day untuned
session TTL with **no revocation surface until R8**, that is unlimited whole-log
reads for a stolen token, and unlimited cascading deletes the day the write
routes land.

### The fix is composition, and the second budget is a second decision

`handler = authed(perTraveller(handler))`. Three calls in it, each against a
real alternative:

- **Two limiters, not one.** The credential limiter exists to bound an
  unauthenticated 64 MiB-per-attempt Argon2 surface, so `AUTH_RATE_LIMIT_PER_MIN`
  is deliberately 10. Wrapping the authenticated routes in *that* would give
  every route a ceiling — and `TestEveryRouteInTheTableIsRateLimited` would pass —
  while a phone syncing a log met a limit built for a password guesser.
  `TRAVELLER_RATE_LIMIT_PER_MIN` is the eighth configuration variable and defaults
  to 600.
- **Keyed on the traveller, not the address.** A stolen token used from a thousand
  addresses is one traveller and would otherwise be a thousand buckets; every
  traveller behind one NAT would otherwise be one bucket. Both directions are
  wrong and the second is the one users feel.
- **The limiter sits INSIDE the authentication, which is the opposite order from
  the one the fix was first written as.** The traveller id is on the context only
  after `RequireTraveller` has resolved the credential, so `limited(authed(h))` —
  the obvious spelling, and the one the review proposed — has nothing to key on.
  Two consequences follow and both are wanted: a flood from somebody holding no
  credential cannot spend a traveller's allowance (there is a leg), and the
  refusal is per-identity rather than per-socket. One consequence is **not**
  wanted and is stated rather than hidden: the session lookup happens *before* the
  ceiling, so what this bounds is the log read and the cascade, not the token
  lookup itself. Bounding that needs a general per-address ceiling on the whole
  API, which is a third budget and does not exist.

`httpx.RateLimitBy(l, log, keyName, key)` is the seam: `RateLimit` is now that
function with `ClientKey` and the name `client`. The key function belongs to the
caller because **`internal/httpx` imports no domain** and has never heard of a
traveller — the same line DEC-74 draws for the error vocabulary. A request the
key function cannot key is a **500, logged at ERROR**, not a shared bucket and
not a wave-through: the only way it fails is a middleware mounted outside the one
that supplies the fact, which is a wiring defect, and failing open there removes
the ceiling in exactly the case nobody notices. Same ruling DEC-48 already made
for a nil limiter, applied one layer down.

### THE SIXTH FIELD WAS PUT AND DECLINED, and the reason is not scope

`routes.go` said, in the source: *"The day an unauthenticated route arrives that
is not a credential attempt, this becomes a sixth field rather than a
derivation."* That day is the **public share read**, which is two steps away and
is not this step. A per-route ceiling added now is a field whose value is a pure
function of `Auth` on **every row of the table** — the exact shape `Mutating` is
in, which needs `TestMutatingAgreesWithTheMethod` to stop it becoming decoration.
And it would be the wrong field: what the share read needs is a **third** budget
— per address, generous, for a route with no identity at all — and a field that
can only say *credential* or *traveller* could not carry it.

The derivation is also **forced** today rather than merely convenient, which is
the half that was not written down before: a route with no traveller has no id to
count against, so `Auth` does not choose the key so much as exhaust it. The field
arrives with the row that cannot be derived, and it arrives holding three values.

### The red, before the fix existed

`internal/httpapi`, eight legs written first, six red on behaviour:

```
--- FAIL: TestEveryRouteInTheTableIsRateLimited
    GET /v1/logbook answered 6 requests in a minute at a ceiling of 3 without one 429.
    PUT /v1/trips/{id} answered 6 requests in a minute at a ceiling of 3 without one 429.
--- FAIL: TestAnAuthenticatedRouteRunsOutOfItsOwnAllowance
    the 4th authenticated request at a limit of 3 a minute = 200 {"version":2,…}, want 429.
--- FAIL: TestOneTravellerRunningOutDoesNotRefuseAnotherAtTheSameAddress
    the first traveller was served 4 of an allowance of 3
--- FAIL: TestOneTravellersTwoSessionsShareOneAllowance
    a second session for the SAME traveller = 200 {"version":2,…}, want 429.
--- FAIL: TestARefusedAuthenticatedRequestNeverReachesTheHandler
    a request past the allowance = 200 {"version":2,…}, want 429
--- FAIL: TestTheTravellerLimitLogsTheTravellerAndNeverTheToken
    the 2nd request at a limit of 1 = 200 {"version":2,…}, want 429
```

`internal/config`:

```
--- FAIL: TestLoadNamesEveryVariableWhenTheEnvironmentIsEmpty
    error does not name TRAVELLER_RATE_LIMIT_PER_MIN:
    config: 7 problems with the environment: …
--- FAIL: …/traveller_rate_limit_is_not_a_number
--- FAIL: …/traveller_rate_limit_is_zero
--- FAIL: TestComposeSetsEveryVariableTheConfigPackageReads
    deploy/docker-compose.yml does not set TRAVELLER_RATE_LIMIT_PER_MIN on the api service.
```

`internal/httpx`, the honest first red for a function that does not exist yet:

```
internal/httpx/ratelimit_test.go:246:20: undefined: httpx.RateLimitBy
FAIL	travellog/internal/httpx [build failed]
```

**Two of the sixteen new legs could not be red first and are labelled here rather
than left to look like the rest.** `TestTheTwoBudgetsAreNotOneBudget` and
`TestAnUnauthenticatedFloodDoesNotSpendTheTravellersAllowance` both **passed
vacuously against the defect** — with no limiter at all, no authenticated route is
refused and no allowance is spent. They are guards against the two wrong fixes
rather than against the defect, so their whole evidence is M4 and M5 below.

### THE LEG THAT DISTINGUISHES PER-TRAVELLER KEYING FROM PER-ANYTHING KEYING

This is the class the existing limiter legs are structurally blind to: **every
leg in this suite arrives from loopback**, so a limiter keyed on the address, on
a constant, or on the path passes the whole suite. It takes a pair, and neither
half is sufficient:

- `TestOneTravellerRunningOutDoesNotRefuseAnotherAtTheSameAddress` — two
  travellers, one address. Kills *keyed on the address* and *keyed on a constant*.
- `TestOneTravellersTwoSessionsShareOneAllowance` — one traveller, two live
  tokens, one budget. Kills *keyed on the bearer token*, which passes the first
  leg and is bought off with a second sign-in by anybody holding the passphrase.

**And the premise of the first is asserted rather than assumed.** The harness
records `httpx.ClientKey` for every request that reaches the chain and the leg
fails if the two travellers arrived from more than one address — because a leg
whose premise is an expectation about `httptest` stops meaning what it says the
day the harness changes. M10 is that assertion's own proof.

### The thirteen mutations, run at `7b47bee` + this working tree

Snapshotted and restored **by file copy**, with a sha256 before and after each
edit; the two that did not compile were rewritten until they did, because a
mutation that does not build proves nothing (M1 and M4 first went red as
`declared and not used`).

| | mutation | what reddened |
|---|---|---|
| M1 | the shipped defect restored: `authed(handler)` | 6 legs, the same six as the red above |
| M2 | key the authenticated budget on the **client address** | two-travellers-one-address; the log leg |
| M3 | key it on the **bearer token** | one-traveller-two-sessions; the log leg |
| M4 | authenticated routes wear the **credential** limiter | `TestTheTwoBudgetsAreNotOneBudget` + 4 |
| M5 | the limiter **outside** the authentication | 33 legs — every authenticated route 500s |
| M6 | `RateLimitBy` fails **open** on a request it cannot key | `TestRateLimitByRefusesARequestItCannotKey` |
| M7 | a refused request reaches the handler anyway | 4, including VS6's own DEC-48 leg |
| M8 | config stops reading `TRAVELLER_RATE_LIMIT_PER_MIN` | 3 config legs |
| M9 | the two ceilings wired from each other's variable | `TestTheTwoCeilingsComeFromTheirOwnVariables` |
| M10 | the harness stops recording addresses | two-travellers-one-address |
| M11 | `RateLimitBy` ignores the key function | 3 httpx legs |
| M12 | the refusal logged under a hardcoded name | `TestTheKeyNameIsWhatTheRefusalIsLoggedUnder` |
| M13 | `Mount` stops refusing a nil traveller limiter | `TestMountRefusesToWireAHalfBuiltAPI/no_traveller_rate_limiter` |

**M5 is the interesting one and its blast radius is the finding.** Putting the
limiter outside the authentication is not a subtle regression — it is 33 red legs,
because every authenticated request then arrives with no traveller on the context
and `RateLimitBy` answers 500. That is the fail-closed choice paying for itself:
the wrong order is impossible to ship quietly.

**M9 is the one nothing else could have caught.** Which variable feeds which
limiter is invisible from outside the process — a swapped pair gives the
credential routes a ceiling of 600 against a 64 MiB-per-attempt surface and gives
a phone a ceiling of 10, and every leg about status codes passes. `limiters()` in
`cmd/api/main.go` exists as its own function so that a test can spend from both.
`Mount`'s nil panic guards the *wiring*; this guards the *arithmetic*.

### The acceptance check, against the rebuilt stack

```
$ docker compose -f deploy/docker-compose.yml up -d --build
$ seq 700 | xargs -P 20 -I{} curl … /v1/logbook -H "Authorization: Bearer $TOK"
    619 200
     81 429
$ # a SECOND traveller, same address, while the first is out of allowance
    200
$ docker compose logs api | grep 'rate limited' | tail -1
{"level":"WARN","msg":"rate limited","traveller":"62726077-…","path":"/v1/logbook",…}
```

619 rather than 600 because the bucket refills while the run is in flight, which
is the token bucket working. The log line names the **traveller** and not the
address, which is what tells an operator which of the two ceilings fired.

### A claim in README.md that was false, and how it was measured

README said *"deploy/.env.example is the template, and a test asserts it lists
everything the config package reads."* **There was no such test** —
`grep -rn 'env.example' --include='*_test.go' .` matched three comments and
nothing executable. The claim was also untrue as written: `DATABASE_URL` and
`PORT` are read by `internal/config` and are deliberately **not** in the template,
because compose composes the first from the `POSTGRES_*` variables and pins the
second to the port the container publishes.

`internal/config/deploy_files_test.go` now asserts the two halves that are true —
compose's api service sets every variable the config package reads, and
`.env.example` documents every variable compose interpolates from the environment.
**Both are artefact tier and both are labelled as such**: they cannot fail because
the code is wrong. They were nonetheless each seen red for a real reason during
this step rather than by mutation — the first when the variable reached
`internal/config` and not compose, the second when it reached compose and not the
template.

### What VS8-SEC leaves guarded by nothing

- **The credential routes' own key is still `RemoteAddr`.** Unchanged, correct for
  a direct connection, wrong the moment Caddy appears — see "Inherited
  unfinished", which this step does not close and does not widen.
- **There is no ceiling on the token lookup itself.** The traveller limiter is
  inside the authentication by necessity, so an attacker with no credential still
  buys one session-table read per request on an authenticated path. What bounds
  that is a general per-address ceiling over the whole API — the third budget, and
  the one that also serves the public share read. It is the same step as the sixth
  field.
- **600 is untuned**, exactly as the Argon2 parameters and the pool sizes are. It
  was chosen against no client traffic: a phone syncing a whole log is one
  conditional read and a handful of writes, so the number is two orders of
  magnitude above the honest case by intent rather than by measurement. The first
  real client is what re-derives it.
- **Nothing tests the log line's *level*.** `TestTheTravellerLimitLogsTheTraveller…`
  asserts the traveller is named and the token is not; a refusal demoted to DEBUG
  would keep the suite green and lose the only signal an operator has that
  somebody's token is being used at machine speed.
- **No leg pins the DEFAULT ceilings** — 10 and 600 live in
  `deploy/docker-compose.yml` and `deploy/.env.example`, and the artefact legs
  assert the variables are *present*, never their values. Changing 600 to 6 is a
  green suite and an application that does not work.
