## REVIEW-1 — the map and the journal, and a stamp that was already green

`CLAUDE.md` was **414,310 bytes and 37 `##` sections** at `b970deb`, loaded into
every agent session. It was written step by step in the same commit as the code
from the first commit of this repository, and it is append-only: superseded
sections are struck rather than rewritten. So it contradicted itself by design —
the Argon2 tuning argument is in section 19 and migration 0007 deleted Argon2 —
and there was no document a new reader could learn the current shape of the
system from in under an hour.

**The two jobs were separated rather than the file trimmed.** `CLAUDE.md` is
now a map of the system as it is today: ground rules, the package graph, the
four load-bearing designs, how authentication works, how a photograph reaches
the bucket, the environment. **11,522 bytes and 7 sections.** Everything else
is `docs/journal/`, one numbered file per section, in the order it was written.

### THE MOVE IS PROVED, NOT ASSERTED

A move described as verbatim and not checked is a move that quietly loses a
paragraph. The splitter re-read every file it had just written, concatenated
them in numeric order, and compared:

```
original  414310 bytes  ba37a0ef2e3ec731ef3e2d91628fe8c4e956ddf350a9dee71b946460407fb13d
rejoined  414310 bytes  ba37a0ef2e3ec731ef3e2d91628fe8c4e956ddf350a9dee71b946460407fb13d
VERBATIM: yes
```

Fences were checked before the split rather than after: a ` ``` ` block
containing a `## ` line would have been cut in half, and the balance count at
EOF says none does.

### THE CEILING IS A GATE RULE, BECAUSE THAT IS HOW IT HAPPENED

Nothing but a gate keeps a map from becoming a journal again, and the way it
happens is one honest paragraph at a time — which is exactly how this file got
to 414 KB. `scripts/check-record.py` runs inside `make check` and refuses
`CLAUDE.md` over 12,288 bytes or over nine `##` sections.

Red first, against the tree as it stood before the split:

```
CLAUDE.md: 414310 bytes, over the 12288 ceiling by 402022. …
CLAUDE.md: 37 '## ' sections, at most 9. …
docs/journal: 0 numbered entries, expected at least 10. …
3 record fault(s)
```

**The third fault is a precondition and is labelled as one in the script.**
Without it the ceiling would pass on a repository with no journal at all — a
limit on a map with nowhere to move anything to, which is a check that measures
nothing. This project has caught that shape three times now.

### AND A CLAIM IN THE REVIEW THAT WAS STALE

The review said `scripts/check-plan.py docs/plan-v7.json` "stamps `CLAUDE.md`'s
byte size and sha256 and has been red on those two lines since R1", and told
this task not to try to fix it. **It is green, and it has been since the check
was repaired.** Run at this working tree:

```
0 failure(s); 66 ids; 23 routes; 8 steps; 14 deletions
```

The repair is `_blob_at`: the stamp names `at_commit: e4a3b94`, and the check
reads `git show e4a3b94:CLAUDE.md` rather than the working tree — 207,684 bytes,
immutable, and untouchable by anything done here. R1 recorded the dilemma
correctly and refused both bad answers; somebody then took the third, which is
to read the file at the commit the provenance claim is about. **A stamp is a
claim about what the planner read, so reading the working tree was the bug.**

Nothing else in that check moved. The 23-route figure it reports is the plan's
own count and is not the shipped surface: `routes.go` holds **24** rows today,
and `/healthz` outside it makes 25.

### What moved, and what deliberately did not

- `docs/agents/domain.md` names the map and the journal, and keeps its
  instruction to say what is missing rather than proceed silently. Its
  "no `CONTEXT.md`, no `docs/adr/`" ruling stands and gains a reason: the
  map/journal split resolves the same need, and either would be a third home
  for a decision.
- `README.md` repointed in two places.
- **No section in `docs/journal/` was edited.** Several say "there is no CI",
  which was true when they were written. The map carries the current rule; the
  journal is dated by position.

### What this leaves guarded by nothing

- **Nothing checks that the journal is still verbatim.** The hash above was
  computed once, at this commit. An edit to a moved section is a green gate.
  The guard is that nobody has a reason to edit one, which is a convention
  rather than a mechanism.
- **The map's accuracy.** `check-record.py` bounds its size and says nothing
  about whether the package table, the route count or the auth flow are still
  true. Every number in it carries the command that produced it, which is the
  most a document can do for itself.

### Commands, not numbers

```bash
wc -c CLAUDE.md; grep -c '^## ' CLAUDE.md            # 11522, 7
ls docs/journal/[0-9]*.md | wc -l                    # 39
cat $(ls docs/journal/[0-9]*.md | head -38) | shasum -a 256   # ba37a0ef…
python3 scripts/check-record.py                      # 0 record fault(s)
python3 scripts/check-plan.py docs/plan-v7.json      # 0 failure(s)
```
