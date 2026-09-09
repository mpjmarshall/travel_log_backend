## Comments

**Remove explanatory comments for self-evident code. Keep only comments that are
non-obvious business logic, complex algorithms, or safety constraints.**

Inherited verbatim from the client project this backend serves, whose record states the
rule and whose reasoning applies here unchanged. **It did not cross over on its own** —
this is the fourth practice from that repository that had to be carried across by hand,
after test-first, the `git checkout` mutation-harness warning, and the database review
lens. Every one was written in English in a file every worker read.

**The test is not density. It is whether a reader could recover the sentence from the code
alone.** If they could, it goes. If it took a measurement, a decision or a device to learn,
it stays — and if it took a measurement, put the number in it.

Worth keeping, and this repository is full of the good kind:

- `go build ./...` writes `./api` into the repo root — found because `git add -A` staged
  a 9 MB binary.
- The probe's own deadline must stay strictly below Docker's `--timeout`, or the
  diagnostic never reaches the health log.
- Composite `ON DELETE SET NULL` nulls the whole key including `traveller_id`; the
  column-list form is why this needs PostgreSQL 15.
- A mutation harness must restore by file copy, never `git checkout` — against an
  uncommitted tree that no-ops on untracked files.

Not worth keeping: anything restating a constant, a struct field, or what an obviously
named function does.

**MEASURED 23 August 2026, before any sweep: 8,125 lines, 1,718 comments — 23.4% of
non-blank.** The worst files are 43–66% (`httpx/errors.go`, `httpx/middleware.go`,
`logging/logging.go`). A sweep is scheduled for after the slice runs end to end rather
than mid-flight. Re-derive the number rather than remembering it; do not trust this one
after the sweep.

For comparison, the client sits at 30.5% and a sweep against this same rule found exactly
**one** removable comment there — so a high ratio is not by itself the defect. The
difference is what the comments are carrying.

### No comments inside a declaration

**Comments go ABOVE the thing they describe, never inside it.** No comment inside a struct
body, an interface body, a const or var block's braces, or a function body. Stated by the
human for this project, and previously for the client project in the same words: *"I hate
comments inside classes or functions. Put them outside of them."*

What stays: the doc comment above a declaration — required for exported identifiers, and
what a reader and `go vet` both expect. Package comments. Build constraints and `//go:`
directives, which are not comments in the ordinary sense.

**What to do with a sentence that wanted to be inside a function.** Two answers, and the
second is usually better:

1. Move it to the doc comment, where a reader meets it before the code rather than halfway
   down.
2. **Let the code absorb it** — a named constant, a named helper, an intermediate variable
   whose name is the sentence. A line needing a comment to be legible is usually a line
   wanting a name.

The second is why this rule improves code rather than merely relocating prose. A comment
inside a function is a note to whoever is already reading that line; a doc comment is a
promise to whoever is deciding whether to call it at all. They are not the same
readership, and only one of them can be served from inside the braces.

Note the interaction with the rule above: moving a comment out is not a licence to keep it.
Most comments that lived inside a function body were restating the line beneath them, and
those go rather than move.

---

