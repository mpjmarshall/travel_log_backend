#!/usr/bin/env python3
"""The entry document's own ceiling.

CLAUDE.md is loaded into every agent session. It reached 414,310 bytes and 37
`##` sections as an append-only journal, in which superseded sections are
struck rather than rewritten — so it contradicted itself by design and there
was no document a new reader could learn the system from. The journal moved to
docs/journal/ verbatim and CLAUDE.md became a map of the system as it is.

Nothing but a gate keeps a map from becoming a journal again, because the way
it happens is one honest paragraph at a time. So the ceiling is checked rather
than written down.

Every failure names the number it measured, because the fix is to move the new
section into docs/journal/ rather than to raise the limit.
"""
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

MAP = "CLAUDE.md"
MAP_MAX_BYTES = 12288
MAP_MAX_SECTIONS = 9
JOURNAL = "docs/journal"
JOURNAL_MIN_FILES = 10


def check_the_map(faults):
    path = ROOT / MAP
    if not path.exists():
        faults.append(f"{MAP}: missing — it is the entry document")
        return
    size = len(path.read_bytes())
    if size > MAP_MAX_BYTES:
        faults.append(
            f"{MAP}: {size} bytes, over the {MAP_MAX_BYTES} ceiling by"
            f" {size - MAP_MAX_BYTES}. This file is a map of the system as it"
            f" is; a new section describing what was decided belongs in"
            f" {JOURNAL}/."
        )
    sections = sum(1 for l in path.read_text().splitlines() if l.startswith("## "))
    if sections > MAP_MAX_SECTIONS:
        faults.append(
            f"{MAP}: {sections} '## ' sections, at most {MAP_MAX_SECTIONS}."
            f" The journal is what grows a section per step."
        )


def check_the_journal(faults):
    entries = sorted((ROOT / JOURNAL).glob("[0-9]*.md"))
    if len(entries) < JOURNAL_MIN_FILES:
        faults.append(
            f"{JOURNAL}: {len(entries)} numbered entries, expected at least"
            f" {JOURNAL_MIN_FILES}. This is a precondition, not a rule about"
            f" the record: below it the ceiling above is a limit on a map with"
            f" nowhere to move anything to, so it would pass while measuring"
            f" nothing."
        )


def main():
    faults = []
    check_the_map(faults)
    check_the_journal(faults)
    for f in faults:
        print(f)
    print(f"{len(faults)} record fault(s)")
    return 1 if faults else 0


if __name__ == "__main__":
    sys.exit(main())
