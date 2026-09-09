## ARCH-2 — where a refusal is authored, and the line R6 left to a human

R6 put this and declined to settle it: *"It is a question for the human."* It is
settled now, and the answer is **partly** rather than yes or no. This section is
the ruling, the measurement it rests on, and what was declined.

**Baseline `559e3df`: 1113 legs. At this commit: 1143.** Two changes, one branch
each, both gated on their own.

### The measurement, re-derived twice because the first count was wrong

**37 constructions of `logbook.InvalidFieldError` in non-test `internal/postgres`,
against 59 in `internal/logbook`** — 39% of the domain's field vocabulary
authored in a package the domain does not import. Classified:

| | | |
|---|---|---|
| **PURE** | 9 | decided from values already in hand |
| **NEEDS-A-READ** | 26 | a row read decides *create or update*; the refusal is then a nil check on the write body |
| **INHERENTLY TRANSACTIONAL** | 2 | the comparison IS the SQL predicate; Go does none of it |

**The middle column is not what its label suggests, and that is the whole
ruling.** For about twenty of the twenty-six the read contributes one bit — does
this row exist — and the rule itself is pure. So the line is not *pure versus
impure*: it is **the rule moves, the read stays**.

### The line, and what is on each side

**Moved to `internal/logbook/writable.go`, 24 sites.** `Check*Writable(isCreate,
w)` for the five entities, plus the traveller name, the snooze and re-file
shapes, the cross-city re-file, the two occasion-ownership rules and the
clearing refusal. A bool and the write body; no new types except `TripDates`,
which exists because the trip date rule is the one that consults before-values —
only the sent half of a partial write is overridden, so the stored half decides
whether the order still holds.

**Left in `internal/postgres`, 13 sites**, each named in
`refusalsAuthoredHere` with its reason:

- **11 existence checks.** Moving them would give `internal/logbook` functions
  whose whole body is `if !exists { return InvalidFieldError{Field: "cityId"} }`
  — a function taking a bool and returning an error named after a field. There
  is no rule in it; what it encodes is a fact about storage.
- **2 inherently transactional refusals.** `refuseVisitsHeldElsewhere` and
  `refuseDroppingAnOccupiedOccasion` push the entire decision into the
  statement — the set difference and the photograph count are both computed in
  SQL — and they guard state the same transaction is about to mutate.

`internal/postgres` went **37 → 13**, and an allowlist-by-equality sweep holds
it: a new refusal reddens, and so does a stale exemption. Both directions were
mutated and both reddened.

### THE COUNT OF UNASSERTED REFUSALS WAS NINE AND IS ELEVEN

A reading gave nine. The number was re-derived by giving all eleven candidate
sites a unique sentinel field name and running the suite: **it stayed entirely
green**, so nothing asserted any of them. With the legs written the same
mutation reddens all eleven. That is the evidence, and it is the only evidence
available — these legs characterise behaviour that was already correct, so
**none of them could be red first**, and they are not reported as though they
were.

`refuseClearingAPlaceThatHasOccasions` carries the longest message in
`place_store.go` and was among the eleven. Its leg asserts the occasion count in
the reason as well as the field, because that count is the only thing its read
produces which the decision does not use.

### `Why` NEVER REACHES A CLIENT, WHICH SHRINKS THE ARGUMENT THAT STARTED THIS

The wire envelope is `{code, field}` — `errorPayload` has two fields and `Why`
is not one of them. So "one rule, three messages" is a **log** inconsistency,
not a client-facing one. That is smaller than the review implied and it is
recorded rather than quietly dropped, because it is the strongest thing that
could have been said against doing this at all. What survives it: the *field
name* is wire-visible, and `internal/postgres` was authoring three names —
`visitAt`, `coordinates`, `centre` — that `internal/logbook` never named.

Legs assert `Field` always and `Why` only where it carries a measurement.

### The id check that cannot be reached

Five files spelt `if w.ID == nil` identically and none had a leg. It is
**unreachable over HTTP**: every one of the five `Put*` methods has exactly one
production caller, and each of those handlers calls `reconcileID` first, which
fills the id from the path. It is a programmer-error guard returning a
client-facing 422.

It became one `logbook.CheckWriteID` and **kept returning a field error**.
`FormatETag` is the precedent for the other choice — it panics on a missing half
because that is a programmer error — and it was declined here: changing
behaviour on a path nothing exercises is where silent damage hides, which is a
lesson this record already carries in three other places.

### Declined, with the reason

- **Moving the 11 existence checks.** Indirection, not locality.
- **`logbook.Service`.** It is shallow — 3 methods, 2 forwarding, and a
  `String()`-then-`Parse` round trip its real caller has made unreachable — and
  that is a question about whether a forwarding layer earns its place, not about
  where a refusal is authored. Recorded as its own candidate rather than allowed
  to ride along.
- **A DEC entry.** The ruling lives here, beside R6's deferral, so the question
  and its answer sit together. `scripts/check-plan.py` validates the DEC id set
  and adding one would have needed the plan file changed in the same commit for
  no gain in findability.

### What this leaves guarded by nothing

- **The two transactional refusals are still database-only**, and they are the
  two whose failure destroys the most. They have legs; those legs need Postgres.
- **`refusalsAuthoredHere` is keyed on file and function.** Renaming a function
  reddens it, which is intended; moving a refusal *within* an allowed function
  is invisible, which is not checked.
- **Nothing asserts the moved messages are the ones that moved.** The policy
  permits the text to change, so a rewrite that made every reason unhelpful
  would keep the suite green.

### Commands, not numbers

```bash
make check
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 1143

# where the field vocabulary is authored
nt() { for f in internal/$1/*.go; do case $f in *_test.go) ;; *) echo "$f";; esac; done; }
nt postgres | xargs grep -hc 'InvalidFieldError{' | paste -sd+ - | bc   # 13, was 37
nt logbook  | xargs grep -hc 'InvalidFieldError{' | paste -sd+ - | bc   # was 59

# the rules that now need no database
go test ./internal/logbook/ -run "Writable|TripCannot|TravellerName|Snooze|Clearing" -count=1 -v
```

