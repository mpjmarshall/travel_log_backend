## REVIEW-2 — the gate runs on a machine now

`make check` has been the only gate since VS1 and it has always been manual.
Every count in this file, every leg, the image tier and the 1,888-line arc
were as fresh as the last time somebody typed the command. There was no
`.github/` directory.

Two workflows, and they are deliberately not one:

| workflow | when | what it runs |
|---|---|---|
| `check` | every push and pull request | `make check` against a real `postgres:17`, then `go test -race -count=2` over five packages |
| `slice` | 03:17 UTC nightly, and on demand | `make test-image`, then `make slice` under its own compose project |

**THE DATABASE TIER RUNS IN CI RATHER THAN SKIPPING, AND THAT IS THE ONLY
INTERESTING DECISION IN THE FILE.** `internal/postgres` fails closed without
`TEST_DATABASE_URL`, and `TRAVELLOG_SKIP_DB=1` is the explicit opt-out — a
`make check` that sets it is green over an unrun half of the suite, which is a
badge rather than a gate. It appears nowhere in either workflow, and the
comment above the step says so where somebody would be tempted to add it.

**The service container's health check carries this repository's own
measurement.** A bare `pg_isready` asks the UNIX SOCKET, and the official
entrypoint runs a bootstrap server on the socket only — `listen_addresses=''`
— while it finishes initdb. VS1-FIXES measured twelve samples of
`docker=healthy` while TCP refused, against zero once `-h 127.0.0.1` was
added. The service container is the same image with the same trap, so the
option is `pg_isready -h 127.0.0.1 -U travellog -d travellog`.

**`go test -race` is in CI and stays out of `make check`.** The gate is four
commands and 4.4 seconds by decision. The limiter's lost update is caught
without the detector because the count comes out wrong; the DATA RACE itself
has only ever been reported by a human running the command.

**`COMPOSE_PROJECT_NAME=ci-slice` is what lets the nightly job run at all.**
The arc's A0 does `down -v`, so it refuses the default project by design, and
the guard reads the project name from compose rather than from the variable.
Setting the project is the way through; `SLICE_DESTROY_VOLUME` stays unset,
which is the distinction that ruling exists to draw.

**The slice job has a Postgres service too**, because the arc's `gate` phase
runs `make check` inside a copy of the repository and that copy fails closed
exactly as the real gate does. It is on 5432; the arc's own stack is a
separate project on 5464/8085/9005, so nothing collides.

`.github/` was added to `.dockerignore`. Stage 1 does `COPY . .` and compiles
`cmd/` and `internal/`; the runner checks the tree out itself.

**What this does NOT close.** Nothing in `go test` reads either workflow, so a
step deleted from `check.yml` is a green suite and an ungated repository. The
evidence for these two files is the runs themselves, and that is the honest
tier for them.

### Commands, not numbers

```bash
gh run list --workflow check.yml --limit 5
gh workflow run slice.yml --ref main
grep -rn 'TRAVELLOG_SKIP_DB' .github/          # nothing, on purpose
```
