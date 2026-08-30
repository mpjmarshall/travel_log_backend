#!/usr/bin/env python3
"""Comment rules, enforced.

Three rules, and one exemption that is arithmetic rather than a loophole:

  1. No comment inside a func, const, type or var body.
  2. No comment run longer than two lines.
  3. Under 20% comment lines, for files of at least MIN_LINES.

A seven-line file with one two-line doc comment is 28% and is correct, so the
density rule applies only above a floor. The other two rules apply everywhere.

Build directives (//go:...) are exempt from all three.
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
