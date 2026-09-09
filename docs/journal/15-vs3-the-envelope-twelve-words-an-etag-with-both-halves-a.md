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

