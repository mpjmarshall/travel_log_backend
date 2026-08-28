# Domain docs

## Before exploring, read this

**`CLAUDE.md`, at the repo root.** It is the context document, it is long
(~6,600 lines here, ~3,000 in the client), and it is deliberately the single
authority. Read `TODO.md` next for what is open.

**There is no `CONTEXT.md` and no `docs/adr/`, and neither should be created.**
The skills' default layout expects both; this project resolved the same need
differently and earlier, and adding them would split the record.

## Where each kind of claim already lives

The one thing this project guards hardest is that a claim has ONE home. A
design review named the alternative — "the why now lives in five places,
reconciled by hand" — as the largest maintenance liability here, and the record
documents four count-drift errors caused by exactly that. Adding a sixth
surface would make it worse.

| kind of claim | its home |
|---|---|
| what was decided, and what was declined | `CLAUDE.md`, 'Decisions taken' / 'Decisions deferred' |
| what is open | `TODO.md`, as a checkbox with its evidence |
| a measurement at a stated commit | `docs/EVIDENCE.md` |
| a ruling with its reasoning | `docs/rulings-v3-pending.json` (DEC-nn) |
| a constraint whose deletion leaves the gate green | a comment, next to the code |
| what the code used to be | git |

That last row is the rule the comment sweep applied: *a comment explains the
code that is there; git explains the code that used to be.*

## Say what is missing

The skills' default is to **proceed silently** when a document is absent. **Do
the opposite here.** This project's method is that a missing thing is stated,
dated and left unticked — nineteen device checks sit unticked for that reason,
and the record repeatedly prefers a red gate to a green one that means nothing.
If something you need does not exist, say so.
