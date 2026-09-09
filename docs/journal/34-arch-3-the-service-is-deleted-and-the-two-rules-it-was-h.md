## ARCH-3 — the Service is deleted, and the two rules it was holding go home

The candidate ARCH-2 split out. `logbook.Service` was a four-field struct with
three methods, two of which forwarded to a store with no rule of their own, and
each of which began by nil-checking the one field it used — it modelled *any
subset of ports* rather than a service.

**1142 legs, from 1143.** The count went DOWN, which is the honest shape of this
change: two tests were deleted with the type they were about, and one was added.

### What each of the three turned out to be

- **`CommitMedia`** is the only one with rules — head the bucket, reconcile
  what came back against what the row declares. It is a **free function** over
  the two ports it uses, `internal/sweep`'s shape and this repository's own
  deepest module.
- **`RefilePhoto`** was not pure forwarding, which the review had it as: its two
  nil checks were **reachable and wire-visible**, and they named `placeId` and
  `visitId` separately where the store's equivalent named `visitId` for both.
  The rule is pure, so it went to `refilePresence`, one implementation called by
  `ValidateRefile` (the handler's shape check) and by `CheckRefileWritable` (the
  store's guard for a caller that is not a handler).
- **`RemovePlace`** was forwarding plus a dead branch: `ParsePhotoDisposition(photos.String())`
  round-tripped a value through its own printer to manufacture an error the
  caller had already produced.

### THE GUARD THAT NEARLY WENT WITH IT

`TestTheServiceRefusesADispositionNobodyChose` said, in its own failure text,
that deleting it makes the zero value arrive as `deletePhotos == false` — D2's
KEEP branch, silently, on a call that answered neither. **Changing the port to
take `PhotoDisposition` instead of a bool does not fix that**: `photosUnspecified`
is still not `DeletePhotos`, so it still falls to keep.

So the guard moved rather than went: `logbook.CheckPhotoDisposition`, called by
`postgres.PlaceStore.RemovePlace` before it acts. The port takes the enum now,
so there is no bool at a call site to get backwards, and the rule that refuses
the zero value has one home.

### A RECORDED DECISION WAS OVERTURNED, AND ITS TEST SAID SO

`TestValidateRefileChecksShapeAndSaysNothingAboutAbsence` asserted the
validator must ignore absence, *"because absence is the Service's refusal and
putting it here too would be two spellings of one rule."* That reasoning was
right and its premise is gone. The concern it names is met a different way:
there is **one implementation** of the presence rule and two call sites, not two
spellings.

Its two isolation legs had to change with it. Each passed one field and relied
on the other being absent, which only reads as "isolate this field" while the
validator ignores absence. They pass both now, so the malformed one is genuinely
the only thing wrong.

### AND ONE WIRE-VISIBLE ANSWER CHANGED

A re-file naming an occasion and no place answered `field: "placeId"` over HTTP
and `field: "visitId"` from the store, because the Service checked first and the
store's own guard named one field for both halves. **It is `placeId` in both
places now.** The store's answer is what moved; the API's is unchanged. A leg
written one step earlier had pinned the store's inconsistent answer, and it was
updated with the reason rather than deleted.

### Declined

- **A leg asserting the handler passes the client's occasion through
  unaltered.** With the Service gone there is no code between the handler and
  the port that could choose one, so the assertion is close to tautological. The
  property that matters — the server does not pick an occasion — is guarded
  where it can fail, in `requireOrMintOccasion`'s legs against a real database.
  The old Service-level test was deleted rather than reproduced.
- **The nil-port checks.** Each method opened with one; `Mount` already refuses
  a nil port at wiring time, and a free function taking two interfaces has no
  half-built state to be in.

### What this leaves guarded by nothing

- **Ten doc comments across the tree do not parse as English.** The comment
  sweep stripped `DEC-nn` references and closed the gap, leaving `is's` and
  `of's` — `internal/logbook/emit.go:6`, `internal/media/store.go:36`,
  `internal/config/config.go:248` and seven more. Only the one naming the
  deleted Service was fixed here. `scripts/check-comments.py` counts lines and
  placement; it cannot read.
- **`logbook.Objects` is satisfied structurally by `media.Store`** and nothing
  asserts the two stay compatible; the compiler catches it at the one call site,
  which is enough today and is one call site.

### Commands, not numbers

```bash
make check
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 1142
grep -rn 'logbook.Service' --include='*.go' internal/ cmd/                # nothing
grep -c 'func (d Deps)' internal/httpapi/auth_handlers.go                 # 1, was 4
grep -rn "is's\|of's" --include='*.go' internal/ | wc -l                  # 9 left
```

