## Agent skills

Scaffolding for an installed skills package, and **all three files were
rewritten because the defaults described a repository this is not.** Measured
before changing them: `gh issue list --state all` answers nothing, `gh label
list` returns GitHub's stock defaults with none of the five triage labels
applied, and neither `CONTEXT.md` nor `docs/adr/` exists.

### Issue tracker

**`TODO.md`, not GitHub issues.** No issue has ever been opened here. The file
carries its own conventions — a box is ticked only when somebody ran
something, a closed entry is rewritten rather than deleted, a fired trigger is
moved rather than ticked, and counts are re-derived rather than incremented.
See `docs/agents/issue-tracker.md`.

### Triage labels

**There are none, and that is not an omission to fix.** The nearest true
equivalent is an entry's evidence tier. See `docs/agents/triage-labels.md`.

### Domain docs

**`CLAUDE.md` is the context document.** There is no `CONTEXT.md` and no
`docs/adr/`, and neither should be created: a design review named "the why now
lives in five places, reconciled by hand" as the largest maintenance liability
here, and this record documents four count-drift errors caused by it. The
table of which claim lives where is in `docs/agents/domain.md`, along with the
one instruction that is deliberately inverted from the default — **say what is
missing rather than proceeding silently.**

