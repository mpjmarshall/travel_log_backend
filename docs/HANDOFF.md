# Handoff — 23 August 2026

Two repositories, one project: a Flutter travel logbook, and a Go backend being
built for it. Written at a context limit; everything below is on disk.

## State

| | |
|---|---|
| **Go** `~/Documents/Go/travellog` | `main` @ `3102786`, 219 tests, gate green |
| **Flutter** `~/Documents/Flutter/Travel Log` | `main` UNTOUCHED @ `9997aae`; work on `wipe/mock-data` @ `903de99` |
| **Running** | `docker compose -f deploy/docker-compose.yml up` — postgres + api, healthy |

**Three agents were mid-flight when this was written.** Check for uncommitted work
before doing anything: Go VS5 (transaction helpers), the Flutter test rebuild, and
the seed generator. One earlier agent **died after an hour with nothing committed** —
so tell workers to **commit incrementally**.

## The one thing to read first

`travellog/CLAUDE.md`. Seven agents wrote measurements into it. It carries the
conventions, the divergences, and what is guarded by nothing.

Then `docs/rulings-v3-pending.json` — **27 human rulings, DEC-43..75**. They
**override** the plan wherever they conflict, and the plan is stale in several places.

## What is built

Four of eight slice steps. `docs/slice-plan.json` is the plan; `docs/go-backend-plan-v6.json`
is the full 61-decision plan the slice was cut from.

```
✓ VS1 repo, compose, scratch image      ✓ VS3 error vocabulary, middleware, ETag
✓ VS2 config, logging, pool, healthz    ✓ VS4 migration runner, eleven tables
⏳ VS5 transaction helpers   · VS6 auth   · VS7 read + write   · VS8 the arc
```

**It boots, migrates, and answers `/healthz` with a real database check. There are no
other routes.** No auth, no logbook endpoints. Tables are empty.

## Architecture, settled after four reversals

**The server owns the data.** PostgreSQL is the record. The phone caches the whole
log (85,422 bytes — measured, not the 79,152 four documents carried) and reads it off
disk before its first frame, so `logbookProvider` stays **synchronous**. Writes go to
the server and wait; blocked offline. Photographs go to S3-compatible storage,
uploaded direct-to-bucket, read via presigned URLs.

An earlier design made the phone authoritative with the server as a replica. **It is
withdrawn.** If you find tombstones, a device id, an operation sequence or an
idempotency table, you are reading the withdrawn design.

## What the reviews found, and why it matters

Three critic passes (10, 13, 6 blocking), a fix verifier (7 blockers, **2 fixes that
were pure fiction**), and a DBA pass (**22 findings** three passes had walked past).

The DBA pass is the lesson: nobody had read the schema **as a database** for six
revisions. It found a blocker no amount of reading would catch — composite
`ON DELETE SET NULL` nulls the whole key including `traveller_id`, so two of the seven
delete rules could not execute. Existing tests passed straight over it.

`docs/dba-review.json` has all of it.

## Discipline in force

`~/Desktop/agent-graph-spec-V4.md` (v4.0.2) — rewritten this session from this run's
failures. Read §6.1a, §6.2, §6.7, §8.

- **Test-first**, and **"write it and break it"** — every test, not only guards.
  Four tests that *could not fail* were found this way.
- **Restore mutations by file copy, never `git checkout`** — that silently destroyed a
  step's implementation here, and the client's own record warned about it.
- **Measured, not assumed.** Nearly every real finding came from executing.
- **No comments inside a declaration.** Repo is 23.4% comments; one sweep done.

## Open, in rough priority

1. **VS5–VS8.** Sequential; auth needs the helpers, the routes need auth.
2. **Flutter test rebuild.** `lib/` is clean and the app builds; **143 errors in
   `test/`** from the deleted fixture. 12 files need worlds they build themselves —
   *not* a shared replacement fixture, which would recreate what was deleted.
3. **The clock override** goes with the fixture. `main_test.dart` asserts the override
   list; `widget_test.dart` has one deliberately unpinned leg that must stay.
4. **`make seed`** from `docs/client_sample_log.json` (DEC-75), and the large generator.
5. **Unknown routes return plain-text 404**, not the JSON envelope. Ruled to VS7.
6. **Postgres 15 is a hard floor** now (the SET NULL fix). `make test-db` refuses older.

## Facts that cost something to learn

- `go 1.25` fails; the directive must be literally **`go 1.25.0`**. `go mod tidy` raises
  it silently, so the hazard is re-editing it **down**.
- `go build ./...` writes `./api` into the repo root.
- The gate **could not fail** on an unparseable file — gofmt exits 2 to stderr.
- Postgres reports healthy while TCP refuses, during initdb, unless `pg_isready -h`.
- `trips.end` is a **reserved word**; renamed `ended_on`. Wire key unchanged.
- Two distinct image digests across all 284 fixture photographs.
