# travellog

The Go + PostgreSQL backend for the Travel Log **logbook** client
(`/Users/mattmarshall/Documents/Flutter/Travel Log` — **READ-ONLY**; nothing in
this repository writes there). One binary, `cmd/api`, and three small commands
beside it. Standard library first: `net/http`'s own `ServeMux`,
`encoding/json`, `database/sql`, `log/slog`, `regexp`, `context`. Three direct
dependencies and that is deliberate — pgx (a blank-import driver and nothing
else), minio-go, `golang.org/x/crypto`.

**THIS FILE IS A MAP OF THE SYSTEM AS IT IS TODAY. IT IS NOT THE RECORD.** The
record is `docs/journal/`: 38 sections, moved there verbatim, each dated by its
position and each saying what was true when it was written. It contradicts
itself by design — the Argon2 tuning argument is still in there and migration
0007 deleted Argon2 — and that is what an append-only record is for. When the
two disagree, this file is right about the code and the journal is right about
the reasoning.

Keep this file **under 12 KB**. `scripts/check-record.py` fails `make check` if
it grows, because a 400 KB entry document loaded into every session is the
thing that was wrong with it.

---

## Ground rules

1. **The gate is `make check`.** `go build` → `go vet` →
   `scripts/check-comments.py` → `scripts/check-record.py` → `gofmt -l` →
   `go test ./...`. **It needs a database**: `make up`, then `make test-db`,
   export the `TEST_DATABASE_URL` it prints. Without it the database tier
   **fails closed on purpose**; `TRAVELLOG_SKIP_DB=1` is the explicit opt-out
   and it is not a pass.
2. **CI runs the gate.** `.github/workflows/check.yml` on every push and pull
   request, with a real `postgres:17` and `go test -race`.
   `.github/workflows/slice.yml` nightly for the image tier and the arc.
   Sections of the journal that say "there is no CI" are dated, not current.
3. **Comments.** No comment inside a func, const, type or var body. Two lines
   maximum. Under 20% of a file. No possessive on a word that cannot own
   anything. Never name a decision id, a plan step or a slice (`DEC-12`, `R6`,
   `VS4`) in a code comment — the decisions live in `docs/`, the code says what
   it does. Enforced by `scripts/check-comments.py`.
4. **The record is written in the same commit as the code.** New entries append
   to `docs/journal/` as a new numbered file; this map is edited when the
   system's shape changes.
5. **Counts are re-derived by a command, never remembered.** Put the command
   beside the number. Four counts in the journal went wrong from not doing
   this, and the record says so each time.
6. **Never edit an applied migration.** `0001`–`0007` are frozen. New schema is
   a new numbered pair with a down file.
7. **Mutation testing restores by file copy, never `git checkout`.** Snapshot
   with `cp`, assert the file changed, run, restore with `cp`, verify `cmp -s`.
   A `git checkout` harness against an uncommitted tree no-ops on untracked
   files and poisons every later mutation; it cost this project a step's
   implementation once.
8. **A mutation that does not compile proves nothing**, and looks exactly like
   one that proved everything.
9. **An artefact check that matches its own source proves nothing.** The record
   has seven greps that went red against correct code. Make sweeps structural —
   AST, parsed catalog, parsed block — not textual.
10. **Stdlib first.** A new dependency needs a stated reason in the PR.

## The packages, and what each owns

Derived: `go list -f '{{.ImportPath}}|{{join .Imports ","}}' ./...`, filtered to
`travellog/`.

| package | owns | imports (internal) |
|---|---|---|
| `internal/config` | every environment variable, all required, no defaults | — |
| `internal/logging` | the JSON handler and the key-based secret redactor | — |
| `internal/httpx` | the wire: 13 error codes, ETags, gzip, timeouts, the three rate limiters, the middleware chain. Knows no domain. | — |
| `internal/auth` | invites, mailed sign-in codes, opaque session tokens | — |
| `internal/mail` | the `Sender` seam. **Only the log sender exists.** | — |
| `internal/media` | S3/MinIO: content addressing, the four signed headers, two presign lifetimes | — |
| `internal/geocode` | city search | — |
| `internal/logbook` | the domain: wire types, the one emitter, every write contract and refusal, and the store interfaces `internal/postgres` satisfies | `media` |
| `internal/admin` | the panel's own vocabulary and its HTML | `auth httpx media` |
| `internal/httpapi` | the route table and the handlers | `auth geocode httpx logbook mail media` |
| `internal/postgres` | **the hub.** Every store, every SQL statement, the migration runner. 3,836 non-test lines against 1,611 for the next largest. | `admin auth logbook` |
| `internal/seed` | loading the client's captured log | `logbook media` |
| `internal/sweep` | reclaiming media objects with no row | `media` |
| `migrations` | a Go package because `//go:embed` cannot reach out of its own directory | — |
| `cmd/api` | the binary: wiring, `/healthz`, the probe | all of the above |
| `cmd/invite`, `cmd/seed`, `cmd/sweep` | operator commands | — |

`internal/postgres` imports `internal/admin` in the same direction it imports
`internal/logbook`: it maps rows to each consumer's own vocabulary, so the
panel imports no persistence package at all.

## The four load-bearing designs

**The route table is data.** `internal/httpapi/routes.go` holds 24 rows, each
declaring method, pattern, `Auth`, `Limit` and `NoStore`. `Mount` derives the
middleware from the row, so a new route cannot arrive without a ceiling or a
cache policy — and `Mount` panics at wiring time on any nil port rather than
serving a route that 500s on its first request. `GET /healthz` is deliberately
outside the table: a liveness probe is not part of the API, so 24 rows ship as
25 routes.

**Integrity lives in the schema, not in Go.** Twelve tables, 26 foreign keys,
seven migrations. Every cascade the client's confirmation sheets promise is a
foreign key — deleting a trip is one `DELETE` statement and the schema does the
rest. Concurrent writes serialise on a per-traveller advisory lock; the whole-log
read runs in one repeatable-read snapshot; `travellers.logbook_version` is a
counter bumped by writes and is the second half of the ETag. Nothing in Go
substitutes for referential integrity, and the Go checks that exist name the
field before the constraint fires.

**Thirteen AST sweeps enforce the architecture**, indexed one row each in
`docs/SWEEPS.md`. They are the best thing here and the mechanism most likely to
go red against correct work. Each walks the AST or the import graph rather than
grepping: `encoding/json` reaches exactly two functions, `os.Getenv` exactly one
file, pgx only as a blank import, error codes are never literals, no handler
takes the whole dependency bag, every bare-entity response goes through an
emitter, refusals are authored in the domain. Exemption lists are asserted by
**equality**, so a stale exemption reddens too. **A new sweep names the shipped
defect it would have caught, in its own doc comment, and updates that index in
the same commit** — the gate fails if the count and the rows disagree.

**Four evidence tiers, and only the first is in the gate.** `go test ./...`
(fast, needs the database); `TRAVELLOG_IMAGE_TESTS=1 make test-image` (the built
`scratch` image); `make slice` (the arc, five phases, a live three-service stack
from a cold volume); and mutation proof, which is what a leg gets when it
characterises behaviour that was already correct. Anything claimed as proven was
observed failing first.

## How authentication works today

Invite-only, and no passphrase exists anywhere in the tree — 0006 added invites
and 0007 removed the passphrase column.

1. **An invite is minted** by the operator: `make invite`, or the admin panel at
   `/admin/invites`. Ten random bytes in Crockford-style base32; the database
   stores only a sha256 and one sentinel covers spent, unknown and misspelt.
2. **`POST /v1/auth/register`** takes `{email, invite}` and spends it. The
   address is stored as typed and folded to lowercase by a unique index on
   `lower(email)`, in SQL, in one place.
3. **`POST /v1/auth/code`** mails a six-digit code: ten-minute TTL, five
   attempts, sixty seconds between requests. **Today the only `mail.Sender` that
   exists writes the code to the container log** (`MAIL_LOG_SENDER=1`), which is
   also the only configuration in which the binary boots.
4. **`POST /v1/auth/session`** takes `{email, code}` and answers an **opaque
   bearer token** — 32 bytes of `crypto/rand`, base64url, and the database keeps
   only its sha256. Not a JWT: it costs one indexed lookup per request and buys
   true revocation. Thirty-day TTL, `last_used_at` written at most every five
   minutes.
5. **`Authorization: Bearer <token>`** on the 20 authenticated routes. Four are
   open: register, code, session, and `GET /l/{token}`, the public share read.

Three rate-limit budgets, three separate buckets: credential routes 10/min per
address, authenticated routes 600/min **per traveller**, the public read 120/min
per address. The limiter sits *inside* the authentication, because the traveller
id only exists once the credential is resolved. **All three key on
`RemoteAddr`**, so behind a proxy each becomes one bucket for the whole
internet. There is no reverse proxy yet; see `TODO.md`.

## How a photograph reaches the bucket

`POST /v1/media` with the sha256 the phone computed → the row is created and the
server answers a presigned PUT plus the exact headers to replay. The signature
covers the digest, the length, the content type and `If-None-Match: *`, so the
capability cannot store bytes that are not its own address, cannot be unbounded,
and cannot overwrite. The phone PUTs to the bucket directly. **`POST
/v1/media/{id}/commit`** heads the object, reconciles what the bucket stored
against what the row declared, and stamps `uploaded_at`. Only then may a
photograph reference it. Reads are separate short-lived presigned GETs:
`POST /v1/media/mint` for the phone, and the public envelope embeds its own.

## The environment

Twenty-one variables, every one required, none defaulted in code —
`grep -oE '"[A-Z][A-Z0-9_]+"' internal/config/config.go | sort -u`:

```
ADMIN_COOKIE_INSECURE  ADMIN_PASSWORD  AUTH_RATE_LIMIT_PER_MIN  DATABASE_URL
DB_MAX_IDLE_CONNS  DB_MAX_OPEN_CONNS  LOG_LEVEL  MAIL_LOG_SENDER
MEDIA_MAX_BYTES  PORT  PUBLIC_RATE_LIMIT_PER_MIN  REQUEST_TIMEOUT
S3_ACCESS_KEY  S3_BUCKET  S3_INTERNAL_ENDPOINT  S3_PRESIGN_TTL_PRIVATE
S3_PRESIGN_TTL_PUBLIC  S3_PUBLIC_BASE_URL  S3_REGION  S3_SECRET_KEY
TRAVELLER_RATE_LIMIT_PER_MIN
```

The defaults live in `deploy/docker-compose.yml`, beside the thing they
configure. `deploy/.env.example` documents them.

## Where everything else is

| | |
|---|---|
| `docs/journal/` | the record, 38 dated sections, verbatim and never edited |
| `docs/EVIDENCE.md` | mutation proofs at stated commits |
| `docs/SWEEPS.md` | the thirteen AST sweeps, one row each |
| `TODO.md` | what is guarded by nothing, and the deployment blockers |
| `deploy/RUNBOOK.md` | how to deploy it and what it still lacks |
| `docs/CLIENT-PREREQUISITES.md` | what the client must do; nothing here can check it |
| `docs/PUBLIC-ENVELOPE.md` | what a stranger holding a share link may see |
| `docs/agents/` | issue tracker, triage and domain conventions |

```bash
make check                                   # the gate
make up && make test-db                      # the database the gate needs
COMPOSE_PROJECT_NAME=x ... make slice        # the arc, never the live project
grep -c '^## ' CLAUDE.md && wc -c CLAUDE.md  # this file's own ceiling
```
