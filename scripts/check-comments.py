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

The one collision measured: a parameter named `have`, in the have/want test
idiom, can legitimately own something. Reword the comment; do not widen the
exemption, because `has's` and `does's` are exactly the shape this catches.
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
    "has have had does do did "
    "parses takes holds names reads writes picks carries returns refuses "
    "answers gives makes puts sets says knows wants needs"
).split()
STRIPPED = re.compile(r"\b(?:" + "|".join(CANNOT_OWN) + r")'s\b")


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
            hit = STRIPPED.search(line)
            if hit:
                faults.append(
                    f"{path}:{i}: {hit.group(0)!r} — a possessive on a word that"
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


def main():
    roots = sys.argv[1:] or ["."]
    faults = []
    for root in roots:
        for path in sorted(pathlib.Path(root).rglob("*.go")):
            if "vendor" in path.parts:
                continue
            faults.extend(audit(str(path)))
    for f in faults:
        print(f)
    print(f"{len(faults)} comment fault(s)")
    return 1 if faults else 0


if __name__ == "__main__":
    sys.exit(main())
