## REVIEW-7 — two architecture facts, and the ceiling did its job on the first try

Two things a review asked to be stated in the map rather than left to be
inferred.

**1. `internal/postgres` is the hub, and it is a decision rather than drift.**
It imports `internal/admin` in the same direction it imports `internal/logbook`
— mapping rows into each consumer's own vocabulary, so neither imports a
persistence package. It is the only package that knows every consumer.

The review called it *"largest by three times"*. **Re-derived it is 2.4x**:
3,847 non-test lines against 1,611 for `internal/logbook`. Including tests it is
12,377 against 7,708, which is 1.6x — so the multiplier depends on which count
you mean, and the map says which. The trigger is in the map: a second binary
needing persistence without the panel, at which point the mapping moves out.

**2. The "without a database" leg counts in the journal are stale.** Several
entries quote a figure from `env -u TEST_DATABASE_URL go test ./...`. Since 29
August the database tier **fails closed** unless `TRAVELLOG_SKIP_DB=1`, so those
numbers describe a run that no longer happens. The current rule is in the map's
first ground rule; **no journal entry was edited**, because each was true when
it was written and position is what dates them.

### THE CEILING FIRED ON ITS FIRST REAL USE, WHICH IS THE FINDING

Adding both facts took `CLAUDE.md` to **12,436 bytes** and `make check` went
red:

```
CLAUDE.md: 12436 bytes, over the 12288 ceiling by 148. This file is a map of
the system as it is; a new section describing what was decided belongs in
docs/journal/.
1 record fault(s)
```

**148 bytes.** That is exactly the size of the pressure that took the old file
to 414 KB — one honest paragraph at a time, none of them individually wrong.
The answer was to trim, not to raise the limit: two ground rules about mutation
harnesses said the same thing twice and are now one rule with two clauses, and
the new paragraph lost a sentence it did not need. **11,796 → 12,189**, still
under.

Written down because the first time a ceiling fires is the moment somebody
decides whether it is a rule or a suggestion.

### Also

`scripts/__pycache__/check-record.cpython-314.pyc` reached the tree in the
previous commit — running the gate's own Python scripts produces bytecode, and
nothing ignored it. `.gitignore` covers `__pycache__/` and `*.pyc` now. Same
class as `/api`, which `go build ./...` drops on every run and which has had a
paragraph in `.gitignore` since VS1.

### Commands, not numbers

```bash
wc -c CLAUDE.md                                          # 12189, under 12288
for d in internal/postgres internal/logbook; do \
  find $d -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l; done
go list -f '{{.ImportPath}}|{{join .Imports ","}}' ./... | grep internal/admin
```
