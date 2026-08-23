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

**`make migrate` and `make slice` fail non-zero today** — the recipes `exit 1`,
and make reports its own **exit 2** when a recipe fails, which is what a caller
or CI observes. (This file said "exit 1" in two places until VS1-FIXES.
Re-derived rather than remembered: `make migrate >/dev/null 2>&1; echo $?` → 2,
`make slice` → 2. The substantive claim — that they fail loudly rather than
exiting 0 on nothing — was right; only the number was wrong.) A target that
exits 0 having done nothing is indistinguishable from one that succeeded, and
that is how a missing step gets counted as a passing one.

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
scripts/               slice-arc.sh (VS8)
docs/                  EVIDENCE.md (VS8)
```

Standard Go project layout, as go_backend.md L17 asks.

**`.dockerignore` is at the repository root and cannot move into `deploy/`.**
Compose builds the api with `context: ..`, so the context root is the
repository root, and Docker reads `.dockerignore` from the context root and
nowhere else — a copy beside the Dockerfile would be read by nothing, silently.
It was absent until VS1-FIXES; see that section for what `COPY . .` was picking
up.

**`docs/` and `README.md` do not exist and never have.** `docs/EVIDENCE.md` is
planned for VS8 and is the only file planned under `docs/`. Recorded here
because `deploy/Dockerfile` claimed a divergence was "recorded in three places"
naming `docs/DIVERGENCES.md` and `README.md` — see VS1-FIXES.

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
- **`make slice`** — VS8. It fails non-zero today: the recipe `exit 1`s, and
  make reports **exit 2**, which is what a caller observes. (Said "exit 1" here
  until VS1-FIXES; corrected against `make slice >/dev/null 2>&1; echo $?` → 2.)
  **`make migrate` is no longer on this list** — VS4 implemented it, and the
  server also migrates at boot.
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
