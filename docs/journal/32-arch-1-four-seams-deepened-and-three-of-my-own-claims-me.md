## ARCH-1 — four seams deepened, and three of my own claims measured wrong

An architecture review of this repository reported seven candidates and four as
`Strong`. This section is the record of fixing those four. **It is one section
at the end of the branch rather than a section per commit**, which is R1's
reading of DEC-23 and the same standing divergence R1 through R8 each recorded:
a step this size as one commit is unreviewable, and the record was written as
the work ran rather than reconstructed at the end.

**Baseline at `938f9c2`: `make check` exit 0, 1097 legs.** At this commit:
1106 — ten guards added, one test deleted with the subject it was about. Every
count below is re-derived by a command in the last block, and **four of them
were wrong the first time I wrote them down.**

### THE THREE CLAIMS THAT DID NOT SURVIVE BEING RUN

**1. Narrowing `Deps` does NOT turn the nil-guards into compile errors, and
that was the review's headline.** The claim was that per-handler ports would
delete `Mount`'s fifteen guards. Go cannot express "this interface field is not
nil" at compile time, so a narrowed handler still receives a nil
`logbook.PlaceStore` and still panics on the first call. What narrowing
actually buys is the interface each handler learns — and the guards had to
stay. They are now covered by `TestEveryFieldOnDepsIsGuardedByMount` instead,
so a field added to `Deps` and left unguarded reddens rather than shipping.

**2. A guard I wrote to find unguarded fields counted a USE as a guard, and hid
the worst one.** The first draft collected every `deps.X` selector inside
`Mount` and reported one gap, `Geocode`. Tightened to count only
`if deps.X == nil` conditions, it reports **two** — and the second is `Auth`,
the `*auth.Service` itself, whose nil is a panic on every sign-in and every
authenticated request after it. `deps.Auth` appears inside `Mount` because
`authed` closes over it, and that mention was enough to satisfy the loose
version. **A sweep that matches a mention rather than the shape it names is the
same defect class this file has recorded six times, and I wrote a seventh.**

**3. "One dead member on `auth.Store`" was two.** The review named
`TravellerExists`. Writing `TestEveryMethodOnTheStorePortIsReachedThroughIt`
found `MintInvite` as well — its three real callers reach
`postgres.AuthStore` concretely (`cmd/invite`, `cmd/seed`) or go through
`admin.Writer`, and **nothing reaches it through an `auth.Store`**, so it cost
every implementation and both twins a method for no caller through that port.
Reading found one; running found two.

**And a fourth, smaller: "~35% of handler lines delete" is wrong.** Measured,
the three shared helpers take seven handler files from 552 lines to 531 — about
21 lines. The win is not volume. It is that the credential refusal, the tag
rule and the path-id reconciliation each exist in ONE place, with a sweep that
keeps them there.

### What was built, per candidate

- **`auth.Store` 14 methods → 12**, both dead members deleted along with
  `postgres.AuthStore.TravellerExists`, its database test, `checkPassphrase`,
  `MinPassphraseBytes`, `MaxPassphraseBytes`, `ErrRegistrationClosed` (declared,
  matched in `writeAuthFailure`, returned by nothing) and the orphaned
  `dummyHash` doc comment whose declaration had already gone.
- **One exported twin, `auth.Memory`**, replacing two hand-written
  near-duplicates that shared a verbatim-identical comment. `internal/auth`'s
  tests keep field access because the twin is in their package; `internal/httpapi`
  drives it through five exported helpers. `media.Memory` is the precedent for
  shipping a twin in a non-test file.
- **`internal/admin` declares its own vocabulary** — `Overview`, `Traveller`,
  `TravellerDetail`, `Session`, `Invite` — and `postgres.AdminStore` maps to it.
  The panel imports no persistence package at all now. `view.go`'s mapping,
  which was `postgres.SessionRow → admin.SessionRow`, is `Session → SessionRow`:
  data to display, both words the panel's own.
- **`apiRoutes` refuses a nil `*sql.DB`.** It returned `nil` error
  unconditionally and built eight `postgres.XStore{DB: nil}` values, every one a
  non-nil interface value, so all fifteen of `Mount`'s guards passed and the mux
  mounted routes that panic on their first query. `cmd/api/routes_test.go`
  passed a nil handle deliberately; those legs now get a real handle from
  `sql.Open`, which validates a DSN and dials lazily.
- **Every handler takes the ports it uses.** 24 took `Deps`; none does.
  Eighteen of the 24 needed exactly two things.

### THE INVITE AFFORDANCE, WHICH IS A DIFFERENCE FROM THE REAL ADAPTER

`internal/httpapi`'s twin let `TESTINVITE` be claimed any number of times, and
many legs depend on it — every one of them registers somebody first.
`postgres.AuthStore` spends an invite on first use, so the twins genuinely
disagreed. Rather than lose the legs or hide it, `Memory.AcceptInvite(code)`
names the affordance and says in its own comment that no real adapter behaves
this way. `media.Memory.PutWithoutChecksum` is the precedent: a twin carrying a
capability the real store has not, documented as such.

### A guard that refused to measure nothing, which is the point of writing them

`TestThePathIdIsReconciledInOnePlace` looks for the reconcile block by its
source text. When the five copies collapsed into `reconcileID`, the sentinel
stopped appearing anywhere and the test failed on **its own precondition** —
"the reconcile block appears nowhere, so this sweep cannot fail for a reason
about the rule it names" — rather than passing having found zero of zero. The
sentinel now names the text the helper contains.

### The mutation

One, on the newest guard, restored **by file copy and never through git**, and
`cmp -s` verified byte-identical afterwards. Giving `putWalk` the whole bag back
first broke the build on an unused import — **a mutation that does not compile
proves nothing and looks exactly like one that proved everything** (VS3's M11).
Made to compile, `TestNoHandlerTakesTheWholeDepsBag` reddens naming the
function: `1 function(s) take the whole Deps bag — walk_handlers.go:putWalk`.

The other nine guards were each seen red before the code that satisfies them
existed, which is where they came from rather than a proof added afterwards.

### What this leaves guarded by nothing

- **The three shared helpers are proven by the routes that call them and by
  three sweeps; none of the sweeps can see a handler that simply does the work
  inline in a shape the sentinel does not match.** `TestTheCredentialPreambleIsWrittenOnce`
  counts `auth.TravellerFrom` call sites, so a handler reading the context key
  directly would pass it.
- **`Memory` and `postgres.AuthStore` are not checked against each other.**
  Nothing asserts the twin refuses what the real adapter refuses — the invite
  affordance above is a known, named divergence, and any other would be silent.
  The `internal/media` tier has the same gap and has carried it since R2.
- **`unguardedDeps` exempts `Now` on the argument that `Deps.Clock()` defaults
  it.** That is true and nothing checks that it stays true.
- **The narrowing is asserted structurally, not semantically.** A handler may
  still be handed a port it does not need; the sweep only refuses the whole bag.
- **The full request dance is NOT absorbed**, and that is a decision rather than
  an omission. Decode, validate, the store call and the emitter still sit in
  each handler. A generic pipeline over them is expressible in Go and was
  declined: the routes genuinely differ — a bare entity, an envelope, a 204, and
  two conditional shapes whose `Document` is nil exactly when one entity moved —
  and a signature carrying all of that is harder to read than the code it
  replaces, in a repository whose comment convention exists to keep code plain.
  **Trigger for revisiting: a sixth entity write, or a change to the dance that
  has to be made in more than three places.**

### Commands, not numbers

```bash
# the gate, and the legs, at this commit
make check
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 1106

# the four candidates, re-derived. THE HANDLER GREPS EXCLUDE TEST FILES:
# the sweeps' own source contains the strings they look for, so an
# unfiltered count answers 5, 4 and 2 rather than 2, 1 and 1.
nontest() { for f in internal/httpapi/*.go; do case $f in *_test.go) ;; *) echo "$f";; esac; done; }
grep -c 'deps Deps) http.HandlerFunc' internal/httpapi/*.go   # 0, was 24
nontest | xargs grep -hc 'auth.TravellerFrom'                 # 2, was 20
nontest | xargs grep -h  'httpx.FormatETag' | wc -l           # 1, was 9
nontest | xargs grep -l  'bodyID == nil' | wc -l              # 1 file, was 5
awk '/^func Mount\(/,/^}/' internal/httpapi/auth_handlers.go | grep -c 'if deps\.'   # 17, was 15
awk '/^type Store interface/,/^}/' internal/auth/service.go | grep -cE '^\t[A-Z][A-Za-z]*\('  # 12, was 14
grep -l 'travellog/internal/postgres' internal/admin/*.go | grep -v _test | wc -l   # 0, was 3

# the twin, and the race the shared mutex makes possible
go test -race -count=2 ./internal/auth/ ./internal/httpapi/ ./internal/admin/ ./cmd/api/
```

### ARCH-1b — the seed's media half, behind the seam that was already there

Candidate 07 of the same review. **The review's premise was half wrong**: it
said `media.Store` has two adapters and `cmd/seed` does not stand behind the
seam. `upload` already took a `media.Store`. What could not be exercised is the
**HTTP PUT after presigning** — `media.Memory`'s URLs are `memory.invalid` by
construction, a reserved TLD, so the twin cannot serve one. The seam that was
missing is the bucket, not the store, and the legs supply it with
`httptest.NewServer`.

`cmd/seed/main.go` goes 349 lines to 261. `ReadImagery`, `Mapping`,
`MediaObjects` and `Upload` are `internal/seed`'s, with seven legs.

**The digests are `shasum -a 256`'s answer, written as literals**, not this
package's own recomputation — an expectation computed the way the code computes
it cannot disagree with it. Both matched when the seed ran for real.

**`Upload` returns what it did rather than printing it.** The command owns what
an operator reads; the module owns the 412. That is what makes the write-once
branch assertable at all — as a `[]Uploaded` with `AlreadyThere` set, rather
than as a line on stdout.

**Two legs are labelled BACKFILL in the file.** `Mapping` and `MediaObjects`
were written before their tests, which is the horizontal-slicing mistake in
miniature. Each is mutation-proven instead: nil `uploaded_at` reddens the row
leg, and collapsing every locator onto one digest reddens the mapping leg —
**which is the exact defect R4 records the whole-log round trip as blind to**,
because `RewriteAssets` is applied to both sides of that comparison.

### What running it found, and it is candidate 06 rather than this one

`make seed` on a fresh stack failed before it reached any of this, on a restart
loop:

```
{"level":"ERROR","msg":"api: stopping","err":"no mail provider is configured
 and DEVELOPMENT is not set: mail: the log sender writes sign-in codes to the
 log and must be asked for by name"}
```

`DEVELOPMENT` defaults to `0` in `deploy/docker-compose.yml`, and `mail.Sender`
has **no adapter that delivers mail** — so the api does not come up at all
unless a deployment asks for the sender that writes credentials to its own log.
That is the review's candidate 06 arriving in the field rather than on paper,
and it is **not tracked anywhere**: `grep -i mail docs/BEFORE-A-PUBLIC-DEPLOY.md`
matches nothing.

### What this leaves guarded by nothing

- **The real bucket.** Every status the upload legs assert is `httptest`'s, not
  MinIO's. The 412 is what MinIO answers for `If-None-Match: *` and it is
  asserted here against a server this repository wrote. The arc's A19 is the
  leg that meets a real bucket, and it does not run `cmd/seed`.
- **`imageryNames` is a literal pair.** A third file in the bundle is read by
  nothing and addressed by nothing, silently. Nothing compares the list to the
  directory.
- **`cmd/seed/main.go` still has no test**, and that is the point of the split
  rather than a gap in it: what is left is flags, prompts, printing and the
  refusals, and `cmd/sweep` is the shape it now matches.

