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
SWEEPS = "docs/SWEEPS.md"
SWEEP_IMPORTS = ('"go/ast"', '"go/parser"')
SWEEP_MIN_FILES = 5


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


def imports_of(path):
    """The quoted paths inside a Go file's import declaration, and nowhere else.

    Scoped rather than grepped: a comment naming go/ast is not an import, and a
    sweep is a file that IMPORTS the parser. The repository has seven recorded
    artefact checks that went red against correct work for exactly that reason.
    """
    body = path.read_text()
    at = body.find("\nimport ")
    if at < 0:
        return ""
    rest = body[at + len("\nimport "):]
    if rest.lstrip().startswith("("):
        end = rest.find("\n)")
        return rest[:end] if end >= 0 else rest
    return rest.split("\n", 1)[0]


def sweep_files():
    out = []
    for path in sorted(ROOT.rglob("*_test.go")):
        if ".git" in path.parts:
            continue
        block = imports_of(path)
        if any(name in block for name in SWEEP_IMPORTS):
            out.append(str(path.relative_to(ROOT)))
    return out


def check_the_sweeps(faults):
    """The index and the tree, in both directions.

    A sweep with no row is a rule nobody can find; a row with no sweep is an
    index describing a guard that has been deleted, which is worse.
    """
    found = sweep_files()
    if len(found) < SWEEP_MIN_FILES:
        faults.append(
            f"found {len(found)} sweep file(s), expected at least"
            f" {SWEEP_MIN_FILES}. The import scan is wrong, so the comparison"
            f" below would agree with itself while measuring nothing."
        )
        return
    path = ROOT / SWEEPS
    if not path.exists():
        faults.append(f"{SWEEPS}: missing, and {len(found)} sweeps are in the tree")
        return
    index = path.read_text()
    for f in found:
        if f not in index:
            faults.append(
                f"{SWEEPS} has no row for {f}, which imports the Go parser."
                f" A sweep nobody can find is a rule the next person deletes."
            )
    for line in index.splitlines():
        if not line.startswith("| ["):
            continue
        named = line.split("`")[1] if "`" in line else ""
        if named and named not in found:
            faults.append(
                f"{SWEEPS} has a row for {named} and no such sweep is in the"
                f" tree. An index describing a guard that has been deleted is"
                f" worse than no index."
            )


def main():
    faults = []
    check_the_map(faults)
    check_the_journal(faults)
    check_the_sweeps(faults)
    for f in faults:
        print(f)
    print(f"{len(faults)} record fault(s)")
    return 1 if faults else 0


if __name__ == "__main__":
    sys.exit(main())
