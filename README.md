# travellog

A Go + PostgreSQL backend for [Travel Log](https://github.com/mpjmarshall/travel_log),
a Flutter travel logbook — trips, the cities in them, the places you pinned, the
photographs, and a timeline you can share.

**Half built.** It boots, migrates, and answers `/healthz` with a real database check.
There are no other routes yet. See [Status](#status).

## Run it

```bash
make up          # postgres + api, migrations run on boot
curl localhost:8080/healthz
make down
```

Needs Docker. `make up` waits for health, so a non-zero exit means it genuinely
did not come up.

## The gate

```bash
make check       # build, vet, gofmt, test  -- the only gate, and it is manual
make test-db     # store tests against a real postgres
make test-image  # the scratch image, the healthcheck, the volume
```

There is no CI. `make check` fails on unformatted source, and **`go vet` is not
optional** — it is what enforces the language floor.

`make check` is green without Docker: tests that need a database skip, and say so.

## What it is

**The server owns the data.** PostgreSQL is the record. The phone caches the whole
log — 85,422 bytes, measured — and reads it off disk before its first frame, so its
provider stays synchronous. Writes go to the server and wait; blocked offline.
Photographs go to S3-compatible storage, uploaded direct-to-bucket, read through
presigned URLs.

An earlier design made the phone authoritative with the server as a replica. **It is
withdrawn.** Tombstones, a device id, an operation sequence and an idempotency table
all belonged to it and are gone.

## Constraints

From the governing spec, and they are hard:

- `net/http`'s `ServeMux` only — no third-party router
- `encoding/json` only, confined to two functions in `internal/httpx`
- `database/sql` only, with `pgx/v5/stdlib` as a **blank-import driver** — no ORM
- Configuration strictly via `os.Getenv`, in one package
- Opaque session tokens: 32 bytes from `crypto/rand`, SHA-256 hashed at rest
- `log/slog`, structured JSON
- Multi-stage build into `scratch`

Three deliberate divergences are recorded in `CLAUDE.md`: the Go build image, the
`go.mod` directive, and a fourth compose service for object storage.

**PostgreSQL 15 is a hard floor** — the schema uses column-list `ON DELETE SET NULL`,
and `make test-db` refuses an older server rather than failing obscurely.

## Layout

```
cmd/api/          the binary
internal/
  config/         the only package that reads the environment
  logging/        slog, with a redactor
  httpx/          envelope, error vocabulary, middleware — imports no domain
  postgres/       the database, and the two transaction helpers
  logbook/        the domain
  auth/           tokens and the hasher
  seed/           loads a captured logbook for development
migrations/       forward-only, embedded, checksummed
test/image/       opt-in Docker tier
docs/             the plan, the rulings, the reviews, HANDOFF.md
```

## Configuration

`DATABASE_URL` · `PORT` · `LOG_LEVEL` · `DB_MAX_OPEN_CONNS` · `DB_MAX_IDLE_CONNS` ·
`AUTH_RATE_LIMIT_PER_MIN`

`deploy/.env.example` is the template, and a test asserts it lists everything the
config package reads. Compose reads `deploy/.env` and **only** that one — a `.env` at
the repository root is silently ignored.

## Status

```
done     repo · config · logging · HTTP layer · schema · transaction helpers
next     auth · the read and the write · the arc end to end
```

Every step is test-first, and every test has been **broken once to prove it fails** —
four tests that *could not* fail were found that way.

## Read next

**`CLAUDE.md`** — the record. Conventions, decisions with their costs, and what is
guarded by nothing. Then `docs/rulings-v3-pending.json`, which overrides the plan
wherever they disagree, and `docs/HANDOFF.md`.
