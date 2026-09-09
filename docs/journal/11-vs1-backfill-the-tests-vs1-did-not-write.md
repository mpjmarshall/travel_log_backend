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

