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

