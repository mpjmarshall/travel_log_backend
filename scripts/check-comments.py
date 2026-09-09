#!/usr/bin/env python3
"""Comment rules, enforced.

Three rules, and one exemption that is arithmetic rather than a loophole:

  1. No comment inside a func, const, type or var body.
  2. No comment run longer than two lines.
  3. Under 20% comment lines, for files of at least MIN_LINES.
  4. No possessive hanging off a word that cannot own anything.

A seven-line file with one two-line doc comment is 28% and is correct, so the
density rule applies only above a floor. The other two rules apply everywhere.

Build directives (//go:...) are exempt from the first three. Rule 4 is what
the sweep that stripped decision ids out of comments left behind: "EmitterVersion
is's first half" was "is DEC-49's first half", and eight of those shipped. The
grammar cannot be checked, but a possessive attached to a copula, a preposition
or a finite verb is always wreckage rather than a sentence.

Two shapes, because a word list was the wrong idea the first time. A closed
set of function words catches `of's`, `and's`, `at's`, `into's` — words that
end in no particular letter and can never own anything. It does NOT catch
`brings's`, `renders's` or `wraps's`, which are the same wreckage on a finite
verb, and eight of those were still in the tree after the first pass. Any word
ending in `s` followed by `'s` is the second shape, and it subsumes `is's`,
`was's` and `has's`.

That shape has real exceptions — English nouns ending in `s` — so
OWNS_LEGITIMATELY names them and is asserted by EQUALITY: an entry that matches
nothing in the tree is itself a fault, because a stale exemption is a hole with
a comment over it. Measured across every .go file, there is exactly one.

The one collision on the first shape: a parameter named `have`, in the
have/want test idiom, can legitimately own something. That comment was reworded
rather than exempted, because `has's` and `does's` are exactly what it catches.
"""
import re
import sys
import pathlib

MIN_LINES = 40
MAX_RUN = 2
MAX_PCT = 20

COMMENT = re.compile(r"^\s*//")
INDENTED = re.compile(r"^[ \t]+//")
DIRECTIVE = re.compile(r"^\s*//(go:|line )")
CANNOT_OWN = (
    "is are was were be been am "
    "of to in on at by for from with into"
    " and or but not "
    "has have had does do did"
).split()
OWNS_LEGITIMATELY = ("harness",)

FUNCTION_WORD = re.compile(r"\b(?:" + "|".join(CANNOT_OWN) + r")'s\b")
VERBAL_S = re.compile(r"\b([A-Za-z]+s)'s\b")


def verbal(line):
    """The `<word ending in s>'s` shape, minus the nouns that really do own."""
    for m in VERBAL_S.finditer(line):
        if m.group(1).lower() not in OWNS_LEGITIMATELY:
            return m.group(0)
    return None


def audit(path):
    lines = pathlib.Path(path).read_text().splitlines()
    total = len(lines) or 1
    faults = []

    ncom = run = start = 0
    for i, line in enumerate(lines, 1):
        if COMMENT.match(line) and not DIRECTIVE.match(line):
            ncom += 1
            if run == 0:
                start = i
            run += 1
            if INDENTED.match(line):
                faults.append(f"{path}:{i}: comment inside a body")
            hit = FUNCTION_WORD.search(line) or verbal(line)
            if hit:
                faults.append(
                    f"{path}:{i}: {hit!r} — a possessive on a word that"
                    " cannot own anything, so a noun was stripped out of this"
                    " sentence and never put back"
                )
        else:
            if run > MAX_RUN:
                faults.append(f"{path}:{start}: comment run of {run} lines, at most {MAX_RUN}")
            run = 0
    if run > MAX_RUN:
        faults.append(f"{path}:{start}: comment run of {run} lines, at most {MAX_RUN}")

    pct = ncom * 100 // total
    if total >= MIN_LINES and pct >= MAX_PCT:
        faults.append(f"{path}: {pct}% comment ({ncom}/{total} lines), under {MAX_PCT}% wanted")
    return faults


def stale_exemptions(seen):
    return [
        f"scripts/check-comments.py: OWNS_LEGITIMATELY names {w!r} and no"
        f" comment in the tree writes {w}'s — an exemption nothing uses is a"
        f" hole with a comment over it"
        for w in OWNS_LEGITIMATELY
        if w not in seen
    ]


def main():
    roots = sys.argv[1:] or ["."]
    faults = []
    exempted = set()
    for root in roots:
        for path in sorted(pathlib.Path(root).rglob("*.go")):
            if "vendor" in path.parts:
                continue
            for line in pathlib.Path(path).read_text().splitlines():
                if COMMENT.match(line):
                    for m in VERBAL_S.finditer(line):
                        w = m.group(1).lower()
                        if w in OWNS_LEGITIMATELY:
                            exempted.add(w)
            faults.extend(audit(str(path)))
    faults.extend(stale_exemptions(exempted))
    for f in faults:
        print(f)
    print(f"{len(faults)} comment fault(s)")
    return 1 if faults else 0


if __name__ == "__main__":
    sys.exit(main())
