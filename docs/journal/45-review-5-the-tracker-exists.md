## REVIEW-5 — the tracker the conventions describe now exists

`docs/agents/issue-tracker.md` has said since it was written that the tracker is
`TODO.md`, gave its four rules, and named its three evidence tiers. **The file
did not exist.** The open items lived at the end of each journal section and had
been read together exactly once, in the R8 reckoning.

`TODO.md` is **38 entries**, grouped by tier, seeded from that reckoning and
from every *guarded by nothing* section after it — ARCH-1, ARCH-2, ARCH-3,
DEPLOY-1, and the four written this week.

### EVERY COUNT WAS RE-DERIVED, AND ONE OF THEM HAD MOVED

The reckoning's own headline was **"twenty-five shipped default values are
pinned by nothing, and that number has never been stated whole"**. Re-derived at
this commit it is **28** — three arrived with the admin panel and the two
switches DEPLOY-1 split. The entry says 28 and carries the command, which is the
rule that number was written to establish.

### THREE ENTRIES CLOSED BY THIS WEEK'S WORK, AND THEY ARE REWRITTEN

Not deleted, per the conventions, because what closed an entry is worth as much
as the entry:

- **`-race` is not in `make check`** — closed by CI, and it stays out of the
  gate deliberately.
- **The image has only ever run on `arm64`** — closed by the nightly running on
  `ubuntu-latest`, which is x86_64. That one had stood since VS1-IMAGE-TESTS.
- **`make slice` and `make test-image` are only as fresh as the last time
  somebody ran them** — closed by the nightly, which found both broken on its
  first run.

### AND ONE TRIGGER HAS FIRED, SO IT IS MOVED RATHER THAN TICKED

*"A second traveller. …DEC-86 closes registration after the first, so a second
traveller cannot exist to test it against."* Migration 0006 ended the
one-traveller rule. The blocker is gone and the gap is not, so the entry moves
into the open list with the trigger recorded as fired — which is the convention's
third rule, and the reason it exists: ticking would have deleted the sentence
explaining why nobody had done it.

### The deployment blockers are entries and not a section of prose

Five, each marked as needing a human decision, each with what it costs rather
than only what it is. The mail one carries the measurement that makes it urgent:
**`MAIL_LOG_SENDER=1` is the only configuration in which the binary boots**, and
that is what broke `make test-image` and the arc for ten days.

### Commands, not numbers

```bash
grep -c '^- \[' TODO.md
grep -oE '\$\{[A-Z_0-9]+:-[^}]*\}' deploy/docker-compose.yml | sort -u | wc -l
```
