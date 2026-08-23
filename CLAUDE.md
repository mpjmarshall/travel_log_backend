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
