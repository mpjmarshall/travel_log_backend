## Comments

Comments say what the code does. They do not say how the project got here.

Three rules, enforced by `scripts/check-comments.py` in `make check`:

1. **No comment inside a func, const, type or var body.** A doc comment sits
   above the declaration. If a line inside a body needs explaining, the
   explanation is a better name or a smaller function.
2. **Two lines maximum.** Go's own convention puts a complete summary in the
   first sentence; that is the whole budget.
3. **Under 20% of a file**, for files of at least 40 lines. Below that the
   arithmetic is meaningless: a seven-line file with one doc comment is 28%.

Build directives are exempt from all three.

**Never name a decision, a plan step or a slice.** `DEC-12`, `VS4`, `SAF-MAJ-5`
and `spec L20` do not appear in a comment. A comment has to stand on its own
for somebody who has never read this file's history, and an identifier they
cannot resolve is worse than silence. The decisions live in `docs/`; the code
says what it does.

**Most code needs no comment at all.** An exported declaration gets one when
its name does not already say what it is. Unexported ones usually get none.

This was applied to all 153 Go files in one sweep, taking 13,775 comment lines
to about 2,000. The sweep is also the reason the rules are checked rather than
merely written down: every file in this repository was authored under the old
convention, so nothing but the gate keeps it from drifting back.

**Identifiers in string literals were left alone.** Error messages, test
failure output and CLI text are what the program says to a person; changing
those is a behaviour change and a separate decision.

