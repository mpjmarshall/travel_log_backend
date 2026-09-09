# Domain docs

## Before exploring, read this

**`CLAUDE.md`, at the repo root — the MAP.** Under 12 KB, and it describes the
system as it is today: the ground rules, the package graph, the four
load-bearing designs, how authentication works, and the environment. Read it
first and read all of it. `scripts/check-record.py` fails the gate if it grows,
because it reached 414 KB once by being both things at once.

**`docs/journal/` — the RECORD.** 38 sections, moved out of `CLAUDE.md`
verbatim and numbered by their position, which is what dates them. It is
append-only and contradicts itself by design: each section says what was true
when it was written. Do not edit a section here. When the map and the journal
disagree, the map is right about the code and the journal is right about the
reasoning.

Then `TODO.md` for what is open.

**There is still no `CONTEXT.md` and no `docs/adr/`, and neither should be
created.** The skills' default layout expects both; the map/journal split
resolves the same need and adding either would make a third place for a
decision to live.

## Where each kind of claim already lives

The one thing this project guards hardest is that a claim has ONE home. A
design review named the alternative — "the why now lives in five places,
reconciled by hand" — as the largest maintenance liability here, and the record
documents four count-drift errors caused by exactly that. Adding a sixth
surface would make it worse.

| kind of claim | its home |
|---|---|
| how the system works today | `CLAUDE.md`, the map |
| what was decided, what was declined, and what it was measured against | `docs/journal/`, in the section for the step that decided it |
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
