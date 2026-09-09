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

