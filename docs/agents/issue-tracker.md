# Issue tracker: TODO.md, not GitHub issues

**This repository has never opened a GitHub issue.** Measured: `gh issue list
--state all` answers nothing, and `gh label list` returns GitHub's stock
defaults — none of the five triage labels the skills speak in exists here.

The tracker is **`TODO.md`**, in this repository and in the client. Do not
create GitHub issues, and do not treat their absence as a gap to fill.

## How TODO.md works

An item is a checkbox with a body, and the body is the point. Entries carry the
measurement that produced them, the command to re-derive it, and — when they
are closed — what closed them and why.

```
- [ ] **A short claim, in bold.** The evidence, with the command that produced
      it, and what breaks if nobody acts.
```

Four rules, and they are what make the file trustworthy:

- **A box is ticked only when somebody ran something.** Nineteen device checks
  are unticked and dated precisely because nothing in either repository can run
  them. An untickable box stays untickable.
- **A closed entry is rewritten, not deleted.** It says what closed it. Several
  entries are closed as *no longer a thing* rather than as done — the fixture
  they described was deleted — and that distinction is kept.
- **A decision that fires its trigger is MOVED, not ticked.** Ticking deletes
  the reasoning along with the box. Two deferred decisions fired during the
  network layer and were recorded as declined, each with a new trigger.
- **Counts are re-derived, never incremented.** This file's own history records
  four count-drift errors caused by carrying a number forward.

## Tiers

Evidence comes in three, and an entry says which it is in:

| tier | means |
|---|---|
| a named leg | a test reddens if you break it |
| an artefact check | a command asserts a fact about a built artefact |
| a human with a device | nothing in the repository can reach it |

## Pull requests as a triage surface

**No.** There is no external contribution flow. `main` is pushed directly and
`./tool/check.sh` (client) or `make check` (backend) is the only gate.
