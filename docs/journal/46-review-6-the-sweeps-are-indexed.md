## REVIEW-6 — the thirteen sweeps are indexed, and the count is a gate rule

Thirteen test files walk the AST or the import graph to enforce an architecture
rule. They are the strongest thing in this repository and **nobody could list
what they guard without reading all thirteen**.

`docs/SWEEPS.md` is one row each: the rule, the defect class it exists for, and
the exemption list it carries. Written from the files rather than from memory —
which is how three of their own doc comments turned out to be sweep wreckage
(*"Opaque session tokens,, test-first"*, *"inside `make check` rather than
inside A document"*, *"The acceptance check for, as a mechanism"*), repaired
here because an index cannot quote a sentence that does not parse.

### THE COUNT IS FROZEN IN BOTH DIRECTIONS, AND THE SECOND IS THE INTERESTING
### ONE

`scripts/check-record.py` finds every `*_test.go` whose **import declaration**
names `go/ast` or `go/parser`, and compares that set to the index:

- **a sweep with no row** is a rule nobody can find, so the next person deletes
  it;
- **a row with no sweep** is an index describing a guard that has been removed,
  which is worse than no index — it reads as coverage.

The scan is scoped to the import block rather than grepped over the file, for
the reason this repository has recorded seven times: a comment naming `go/ast`
is not an import, and an artefact check that matches prose fails against correct
work.

**And it refuses to measure nothing.** Fewer than five files found is a fault
saying the scan is wrong, because a scan that finds none makes the comparison
agree with itself.

### Four mutations, all reddening

```
M1  delete internal/media/ban_test.go's row
    docs/SWEEPS.md has no row for internal/media/ban_test.go, which imports the
    Go parser. A sweep nobody can find is a rule the next person deletes.
M2  add a row for internal/ghost/gone_sweep_test.go
    …and no such sweep is in the tree. An index describing a guard that has
    been deleted is worse than no index.
M3  land a NEW sweep, unindexed
    docs/SWEEPS.md has no row for internal/httpx/newrule_sweep_test.go …
M4  point the import scan at "go/nothing"
    found 0 sweep file(s), expected at least 5. The import scan is wrong, so
    the comparison below would agree with itself while measuring nothing.
```

M3 is the one the rule is actually for: the failure mode is not somebody
deleting a row, it is somebody adding a sweep and not writing one.

### ONE ROW SAYS IT IS NOT A SWEEP

`internal/auth/token_test.go` is an ordinary test file with a single AST leg in
it, and it is in the derived set because it imports `go/ast`. The row says so.
An exemption would have been the other answer and it is worse: the set is
derived from the tree, and a file that genuinely imports the parser belongs in
a list of files that import the parser, whatever else it is.

### The rule for the next one

Written into the map and into the index: **a new sweep names, in its own doc
comment, the shipped defect it would have caught, and updates the index in the
same commit.** A sweep enforcing a preference rather than a defect is what makes
the next person delete the one that matters.

### Commands, not numbers

```bash
grep -rl 'go/ast\|go/parser' --include='*_test.go' . | sort | wc -l   # 13
grep -cE '^\| \[' docs/SWEEPS.md                                      # 13
python3 scripts/check-record.py                                       # 0 faults
```
