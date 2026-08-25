#!/usr/bin/env python3
"""agent-graph-spec-V4 §6.5 — mechanical invariants over docs/plan-v7.json.

An agent's claim to have re-derived something is not evidence. Every count,
cross-reference and id rule this plan states is checked here, by code, and the
exit status is the gate. Run it before the first critic pass and again after
every revision.

    python3 scripts/check-plan.py [docs/plan-v7.json]

IT READS THE RULINGS FILE RATHER THAN TRUSTING A FIELD, and that is the whole
of the v7.1 addition. v7.0 certified a hash for docs/rulings-v3-pending.json
that did not match the file, never read its last three rulings, and then reused
all three of their ids for unrelated decisions of its own — and this script
reported 0 failures, because the rulings file was not in its uniqueness set. The
consequence was not procedural: the plan cited "DEC-77" for a privacy-critical
share-flag reset while the authoritative file has DEC-77 as capturing two PNGs,
so a worker resolving the citation would have implemented the wrong thing while
believing they followed the ruling.

Two rules follow, and the second is the one that removes the class:
  * the plan's own decisions live in the PD- namespace and may not claim a DEC-
    id, which belongs to the human;
  * base.inputs' stamp for the rulings file is RECOMPUTED here, so a stamp that
    has gone stale is a gate failure rather than a lens finding. It went stale
    twice in one revision.

WHAT v7.2 ADDS, AND WHY THE v7.1 ADDITION WAS NOT ENOUGH. The stamp check WORKED:
it was red at d781d29, on the size and the hash, exactly as designed. It caught a
stale FIELD and it could not catch a stale READING — the plan re-ran the hash at
v7.1 and left the word THIRTY-TWO standing beside it while DEC-84..DEC-102, all
nineteen of them, had zero mentions across 261 KB. Six checks follow from that
and from the seven lens reports:
  * the rulings stamp carries a COUNT, a FIRST id and a LAST id, all recomputed,
    and its `command` must carry the count it produced (§6.4);
  * CLAUDE.md's stamp is recomputed too — it was silently stale by 14,565 bytes
    at v7.1 and no lens reported it. Two recomputed stamps, not one;
  * `claimed_fixes` is counted against ALL SEVEN lens reports with the id set
    DERIVED positionally, not against two. At v7.1 this passed at 28 entries
    while five reports and 65 findings sat unread;
  * every claimed_fix names `landed_in`, and a BLOCKER may not land in
    `next_slice`. A blocker quietly deferred is the same failure as an unread
    ruling, from the other direction;
  * no acceptance check runs `make seed` before `make slice`, because slice's
    first step is `docker compose down -v` against the live project and five of
    eight steps taught that order;
  * `internal/logbook/emit.go` appears in some step's file list, and the
    migration count is DERIVED from the file lists rather than stated.

WHAT v7.2.2 ADDS. Four rulings landed (DEC-103..DEC-106; 55 now, DEC-43..DEC-106)
and the four-field stamp caught ALL FOUR fields before a word of the revision was
written — size, hash, count AND id range. That is the return on v7.2's three
extra fields: the v7.1 gate could not have seen the count or the range, and the
three previous re-stamps each went wrong in a way it could not see.

DEC-103 then reopened a set DEC-12 had closed, so "the twelve codes" became false
in eleven places at once — the third time in three revisions that a ruling
narrowing a literal left the old one behind (OE-17's phrase survived in four,
the content-type regex in five). The first two got a bespoke check each. THE
THIRD GOT A MECHANISM: `plan.superseded_literals` names each literal, the ruling
that superseded it and the marker a surviving mention must carry, and one loop
drives all three. Three instances of one shape is a class, and a fourth bespoke
check would have been the sixth count this document polices by reading.

WHAT v7.2.1 ADDS, AFTER AN INDEPENDENT FixVerifier READ v7.2 AGAINST THE SEVEN
LENS REPORTS. It graded 81 LANDED, 5 PARTIAL, 3 FICTION, 3 CONTAINS-ITS-OWN-DEFECT
and 1 GUARD-CANNOT-FAIL, and re-ran all eleven checks above and reproduced every
red. Two of its findings are about this file:

  * THE HEADLINE "BEFORE" TRANSCRIPT WAS NOT REPRODUCIBLE FROM THE COMMIT IT
    NAMES. `git show d781d29:scripts/check-plan.py` is 81 lines with 14 check()
    calls and NO rulings block, and run against that commit's plan it prints
    `0 failure(s); 48 ids; 23 routes; 8 steps; 10 deletions` and exits 0 — the
    same four counts the record quotes beneath two FAIL lines it cannot emit.
    The transcript is REAL: the v7.1 rulings block was in the working tree,
    UNSTAGED, while d781d29 committed docs/ only, and 5c34406 then swept the
    script in as 293 insertions. So the finding is not fabrication, it is that
    THE COMMIT IS NOT SELF-CONTAINED. `plan.gate_transcripts` now carries the
    sha256 of the script that produced the output, this file hashes ITSELF and
    compares, and the summary line's ids/routes/steps/deletions are recomputed —
    so editing the gate without re-running and re-stamping is a gate failure.
    The d781d29 entry is marked `reproducible_from_the_commit: false` with the
    reason, because the script that produced it no longer exists anywhere and it
    is the one case the rule cannot retrofit.

  * A DELETION RECORDED AGAINST A LOCATION IS A CLAIM ABOUT THAT LOCATION.
    OE-17 deleted the phrase "four sentences"; its first draft named three sites,
    missed two, and one of the three had moved. The phrase may now be NAMED in
    order to be corrected and may not be ASSERTED, tested as: any string carrying
    it also carries "eighteen".

THAT SECOND CHECK'S OWN FIRST DRAFT WAS AN ALLOWLIST OF TOP-LEVEL KEYS AND WENT
RED ON THREE CORRECT SITES — a permitted-location list cannot tell "still says
four" from "says four was wrong". Rewritten as a predicate about the claim, it
then found THREE MORE SURVIVALS beyond the two the verifier listed. That is the
fourth instance in this file of a check that could not fail as first written, and
the first where fixing it immediately paid.

EVERY ONE OF THE TEN WAS MUTATION-PROVED, and one of them failed that proof on
its first draft: the migration-count prose check asserted `word in note`, and the
note recites its own history ("v7.1 said THREE and v7.0 said TWO"), so every
candidate word was already a substring and the mutation went green. It now
asserts exactly one `<WORD> up-files`. That is the third instance in this file of
a check that could not fail, and the reason each is written down rather than
quietly fixed.
"""
import glob
import hashlib
import json
import os
import subprocess
import re
import sys

path = sys.argv[1] if len(sys.argv) > 1 else "docs/plan-v7.json"
# Kept under its own name because `path` is rebound by the stamp loop below.
PLAN_PATH = path
plan = json.load(open(path))
failures = []

# v6's own decision list, DEC-01..DEC-54, which this plan legitimately cites
# (DEC-12's vocabulary, DEC-24's allowlist, DEC-31's one read, and so on). The
# rulings file starts at DEC-43, so the two overlap by design and a citation is
# valid if either holds it.
KNOWN_V6_DECISIONS = {f"DEC-{n:02d}" for n in range(1, 55)}


def check(cond, message):
    if not cond:
        failures.append(message)


text = json.dumps(plan)
steps = {s["id"] for s in plan["steps"]}

# Every step reference resolves to a step that exists.
for ref in sorted(set(re.findall(r"\bR[1-8]\b", text))):
    check(ref in steps, f"reference {ref} resolves to no step")

# No id appears twice, anywhere.
ids = [x["id"] for k in ("decisions", "deferred_decisions", "deletions",
                         "open_rulings", "steps") for x in plan[k]]
check(len(ids) == len(set(ids)),
      f"duplicate ids: {sorted(i for i in set(ids) if ids.count(i) > 1)}")

# THE CHECK v6 NEEDED FOR SIX REVISIONS AND NEVER HAD. DEC-71 and DEC-74
# renamed all four of these and every one of v6's 23 step file lists is stale.
STALE = ("internal/api/", "internal/rest/", "internal/store/", "internal/objects/")
for s in plan["steps"]:
    for f in s["files"]:
        for name in STALE:
            check(name not in f, f"{s['id']} names the renamed package {name}: {f}")
        check("Flutter" not in f,
              f"{s['id']} writes to the read-only client: {f}")

# The route arithmetic equals the table it counts.
rt = plan["sequencing"]["route_table_at_the_end_of_R8"]
check(len(rt) == 23, f"route table holds {len(rt)} rows, the notes claim 23")
check(sum(1 for r in rt if "LIVE" in r) == 5,
      "the count of LIVE rows disagrees with entry_state")
built = sum(1 for r in rt if re.search(r"— R\d\.", r))
check(built == 18, f"{built} routes are attributed to a step, want 18")

# §6.7 — a behavioural step carries its failing test, not a description of one.
for s in plan["steps"]:
    ts = s.get("test_strategy", "")
    check(bool(ts), f"{s['id']} has no test_strategy")
    check("func Test" in ts or ts.startswith("none —"),
          f"{s['id']}'s test_strategy has neither a test body nor 'none — <reason>'")
    check("HOW IT CAN FAIL" in s.get("acceptance_check", ""),
          f"{s['id']}'s acceptance check does not say how it could fail")

# THE RULINGS FILE IS READ, NOT TRUSTED. Two failures live here and both fired
# in the real run: an id the plan claimed that the human already owns, and a
# base.inputs stamp that had gone stale while the plan was being written.
RULINGS = "docs/rulings-v3-pending.json"
if os.path.exists(RULINGS):
    raw = open(RULINGS, "rb").read()
    ruling_ids = {r["id"] for r in json.loads(raw)["rulings"]}

    own = {x["id"] for x in plan["decisions"]}
    check(not (own & ruling_ids),
          f"the plan claims ids the human's rulings own: {sorted(own & ruling_ids)}")
    check(all(i.startswith("PD-") for i in own),
          f"a plan decision is outside the PD- namespace: "
          f"{sorted(i for i in own if not i.startswith('PD-'))}")

    # Every DEC- the plan CITES must exist in the rulings file. A citation that
    # resolves to nothing is how a worker implements the wrong ruling.
    # A named exemption list, not a loosened pattern: the arc's own record phase
    # works the same way, and the reason for each entry is written down.
    #
    # THE EXEMPTION IS SCOPED TO ITS LOCATION AND THE FIRST DRAFT WAS NOT.
    # Exempting the ids GLOBALLY made this check unable to fail: it passed while
    # two real dangling citations stood in a step's file list. That is the class
    # this repository has recorded seven times from the other direction — an
    # artefact check going red against correct work, and a worker weakening the
    # check rather than the code. So the exempted FIELD is removed from the scan
    # and every other field is still read.
    exempt_paths = {
        e["where"] for e in plan.get("mechanical_invariants", {})
        .get("citation_exemptions", [])
    }
    for e in plan.get("mechanical_invariants", {}).get("citation_exemptions", []):
        check(len(e.get("reason", "")) > 80,
              f"citation exemption {e['id']} has no challengeable reason")
        check("where" in e, f"citation exemption {e['id']} names no location")

    def cited_ids(node, path=""):
        """Every DEC- id in the plan, minus the exempted locations.

        The exemption register itself is not scanned: it is metadata ABOUT
        citations and necessarily names the ids it exempts, so scanning it would
        make every entry self-defeating.
        """
        if path == "mechanical_invariants.citation_exemptions":
            return
        if isinstance(node, dict):
            for k, v in node.items():
                here = f"{path}[{node['id']}]" if k != "id" and "id" in node else path
                yield from cited_ids(v, f"{here}.{k}" if here else k)
        elif isinstance(node, list):
            for v in node:
                yield from cited_ids(v, path)
        elif isinstance(node, str):
            if path not in exempt_paths:
                for m in re.findall(r"\bDEC-\d+\b", node):
                    yield m, path

    for cited, where in sorted(set(cited_ids(plan))):
        check(cited in ruling_ids or cited in KNOWN_V6_DECISIONS,
              f"the plan cites {cited} at {where}, which is in neither the "
              f"rulings file nor v6's own decision list")

    stamped = next((i for i in plan["base"]["inputs"] if i["path"] == RULINGS), None)
    check(stamped is not None, "base.inputs does not stamp the rulings file at all")
    if stamped:
        check(stamped["size"] == len(raw),
              f"the rulings stamp says {stamped['size']} bytes; the file is {len(raw)}")
        real = hashlib.sha256(raw).hexdigest()
        check(stamped["sha256"] == real,
              f"the rulings stamp says {stamped['sha256'][:12]}…; the file is {real[:12]}…")

        # ADDED AT v7.2, AND THE REASON IS THAT THE SIZE AND THE HASH ALONE DID
        # NOT CATCH WHAT HAPPENED. v7.1 re-ran the hash correctly and left the
        # word THIRTY-TWO standing beside it, while DEC-84..DEC-102 — nineteen
        # rulings, two of which move work INTO scope and one of which is a fix to
        # shipped code — had zero mentions across the whole plan. A stamp check
        # catches a stale FIELD. These three catch a stale READING: the count and
        # the endpoints are what a prose sentence has to agree with.
        order = [r["id"] for r in json.loads(raw)["rulings"]]
        check(stamped.get("count") == len(order),
              f"the rulings stamp says count={stamped.get('count')}; the file holds {len(order)}")
        check(stamped.get("first") == order[0] and stamped.get("last") == order[-1],
              f"the rulings stamp says {stamped.get('first')}..{stamped.get('last')}; "
              f"the file runs {order[0]}..{order[-1]}")
        check(str(len(order)) in stamped.get("command", ""),
              "the rulings stamp's command does not carry the count it produced — §6.4 "
              "asks for the command AND its output")

# THE SAME RECOMPUTATION FOR CLAUDE.md, WHICH WAS SILENTLY STALE BY 14,565 BYTES
# AT v7.1 AND WHICH NO LENS REPORTED. It is the authority on what was built and
# it outranks v6 everywhere they disagree, so a plan certifying a hash it did not
# run is making a claim rather than a measurement. Two recomputed stamps, not one.
#
# READ AT THE COMMIT THE STAMP NAMES, NOT IN THE WORKING TREE, and the difference is
# not a convenience. This stamp is a claim about PROVENANCE — the planner read
# this file and this is what it held — and every step from R1 on has CLAUDE.md in
# its file list, because recording what was built is part of building it. Against
# the working tree the check therefore goes red on CORRECT work, and the only ways
# out are both wrong: re-point the stamp, which makes the plan assert a hash the
# planner never read, or stop editing CLAUDE.md, which is the deliverable. R1 hit
# this at 82bd3ab and refused both, which is the right refusal and is why this
# reads the blob instead. The commit is NAMED in the stamp (`at_commit`) rather
# than inferred from the plan's history: inferring it means any later plan edit
# silently re-anchors the check onto a file the planner never read.
#
# The original defect is still caught, because it was a defect at the plan's own
# commit: a planner that stamps a hash it did not run is red here whether or not
# anybody has touched the file since.
def _blob_at(path, rev):
    """`path` as it stood at `rev` — the commit the stamp NAMES, not one inferred.

    Inferring it from `git log -1 -- <plan>` was the first attempt and it is
    wrong: any later commit touching the plan for ANY reason re-anchors the
    check. The one that caught it was this commit itself, whose only plan edit
    was re-stamping a transcript — and that alone moved the anchor onto R1's
    edited CLAUDE.md, which no planner ever read.

    Falls back to the working tree outside a git checkout, or when the stamp
    carries no `at_commit` — a first revision has no commit to read a blob from,
    and refusing to check at all would be worse than checking what is here.
    """
    if not rev:
        return open(path, "rb").read(), "the working tree (the stamp names no commit)"
    try:
        out = subprocess.run(["git", "show", f"{rev}:{path}"],
                             capture_output=True, check=True).stdout
        return out, f"{rev[:7]}, the commit the stamp names"
    except (subprocess.CalledProcessError, FileNotFoundError):
        return open(path, "rb").read(), f"the working tree ({rev[:7]} is unreadable here)"


for path in ("CLAUDE.md",):
    if os.path.exists(path):
        st = next((i for i in plan["base"]["inputs"] if i["path"] == path), None)
        check(st is not None, f"base.inputs does not stamp {path} at all")
        if st:
            check("at_commit" in st,
                  f"the {path} stamp names no at_commit, so the hash it certifies "
                  f"cannot be located — a provenance claim with no provenance")
            body, where = _blob_at(path, st.get("at_commit"))
            check(st["size"] == len(body),
                  f"the {path} stamp says {st['size']} bytes; at {where} it is {len(body)}")
            real = hashlib.sha256(body).hexdigest()
            check(st["sha256"] == real,
                  f"the {path} stamp says {st['sha256'][:12]}…; at {where} it is {real[:12]}…")

# §6.2 — a reviser does not grade its own fixes, so every finding must have a
# claimed_fix a FixVerifier can open.
#
# COUNTED AGAINST ALL SEVEN REPORTS, NOT TWO. At v7.1 this read `lens-database`
# and `lens-security` only, passed at 28 entries, and five reports holding 65
# findings — including 16 blockers — sat unread beside it. The id set is DERIVED
# positionally from the reports rather than transcribed, so a report gaining a
# finding is a gate failure rather than a silent gap.
LENS_PREFIX = {"database": "DB", "security": "SEC", "operations": "OPS",
               "performance": "PERF", "safety": "SAF", "client-fidelity": "CF",
               "storage": "STO"}
SEV_TAG = {"blocker": "BLO", "major": "MAJ", "minor": "MIN"}

expected, blockers = {}, set()
for lens_path in sorted(glob.glob("docs/lens-*.json")):
    name = os.path.basename(lens_path)[len("lens-"):-len(".json")]
    if name not in LENS_PREFIX:
        continue
    for i, finding in enumerate(json.load(open(lens_path))["findings"], 1):
        fid = f"{LENS_PREFIX[name]}-{SEV_TAG[finding['severity']]}-{i}"
        expected[fid] = finding["severity"]
        if finding["severity"] == "blocker":
            blockers.add(fid)

if expected:
    claimed = {cf.get("finding_id"): cf for cf in plan["claimed_fixes"]}
    missing = sorted(set(expected) - set(claimed))
    extra = sorted(set(claimed) - set(expected))
    check(not missing, f"{len(missing)} lens findings have no claimed_fix: {missing}")
    check(not extra, f"claimed_fixes names findings no report holds: {extra}")
    check(len(plan["claimed_fixes"]) == len(expected),
          f"{len(plan['claimed_fixes'])} claimed_fixes against {len(expected)} lens "
          f"findings — §6.2 wants one per finding")
    for cf in plan["claimed_fixes"]:
        fid = cf.get("finding_id")
        check(len(cf.get("where", "")) > 10,
              f"claimed_fix {fid} names no location a verifier could open")
        # EVERY FINDING SAYS WHERE IT WENT, AND A BLOCKER MAY NOT GO NOWHERE.
        # A blocker silently dropped is the failure this plan has already had
        # from the other direction, with nineteen unread rulings.
        landed = cf.get("landed_in", "")
        check(landed in steps or landed in {"base", "decisions", "next_slice", "DECLINED"},
              f"claimed_fix {fid} has landed_in={landed!r}, which is neither a step "
              f"nor base/decisions/next_slice/DECLINED")
        if landed == "DECLINED":
            check(len(cf.get("why_declined", "")) > 80,
                  f"claimed_fix {fid} is DECLINED with no challengeable reason")
            check(cf.get("verdict_expected", "").startswith("DECLINED"),
                  f"claimed_fix {fid} is DECLINED but does not tell a verifier so")
    for fid in sorted(blockers):
        cf = claimed.get(fid, {})
        check(cf.get("landed_in") in steps or cf.get("landed_in") == "base",
              f"blocker {fid} lands in {cf.get('landed_in')!r} — a blocker goes into a "
              f"step (or into base, for a plan-level defect), or it is DECLINED with "
              f"a reason and stops being a blocker quietly")

    # AND THE SUMMARY TABLE IS RECOMPUTED, NOT READ. The first draft of the prose
    # beside it got three of its seven numbers wrong while the per-finding table
    # was correct — this project's record already carries four counts that went
    # wrong from being carried rather than run, so the summary is a field.
    bl = plan["sequencing"].get("blocker_landing")
    check(bl is not None, "sequencing carries no blocker_landing table to recompute")
    if bl:
        derived = {}
        for fid in sorted(blockers):
            derived[fid] = claimed.get(fid, {}).get("landed_in")
        check(bl.get("by_finding") == derived,
              "sequencing.blocker_landing.by_finding disagrees with claimed_fixes")
        counts = {}
        for v in derived.values():
            counts[v] = counts.get(v, 0) + 1
        check(bl.get("counts") == counts,
              f"sequencing.blocker_landing.counts says {bl.get('counts')}; "
              f"claimed_fixes derives {counts}")
        check(bl.get("total") == len(blockers),
              f"sequencing.blocker_landing.total says {bl.get('total')}; the seven "
              f"reports hold {len(blockers)} blockers")

# DEC-92 — THE DOCUMENTED PROCEDURE MUST NOT TEACH `make seed` AND THEN
# `make slice`. Step A0 of the arc is `docker compose down -v` against the LIVE
# project, and five of the eight steps' acceptance checks read
# `make seed && make check && make slice`, so the plan was teaching a developer
# that seeding and then wiping is normal on a stack that will hold the only copy
# of somebody's photographic record. Prose that QUOTES the old order is fine;
# an acceptance check that runs it is not, so only acceptance_check is scanned.
for s in plan["steps"]:
    ac = s.get("acceptance_check", "")
    for line in ac.splitlines():
        if "make seed" in line and "make slice" in line:
            check(line.index("make seed") > line.index("make slice"),
                  f"{s['id']}'s acceptance check runs `make seed` before `make slice` "
                  f"on one line: {line.strip()!r}")
    if "make seed" in ac and "make slice" in ac:
        check(ac.index("make slice") < ac.index("make seed"),
              f"{s['id']}'s acceptance check seeds before it slices, and slice's first "
              f"step destroys the volume")

# CF-BLO-3 — `internal/logbook/emit.go` WAS IN NO STEP'S FILE LIST, while the
# definition_of_done carried the rule that every route answering a bare entity
# goes through an EmitX function. A rule with nowhere to land is a different
# failure from a rule being wrong, and it is not one a green suite can see.
check(any("internal/logbook/emit.go" in f for s in plan["steps"] for f in s["files"]),
      "no step's file list names internal/logbook/emit.go, and the definition_of_done "
      "requires every bare-entity route to go through an EmitX function")

# THE MIGRATION COUNT IS DERIVED FROM THE FILE LISTS, NOT STATED. It has been
# TWO, then THREE, then FOUR across three revisions and only the last was derived.
mc = plan["sequencing"].get("migration_count")
check(mc is not None, "sequencing carries no migration_count to check against the file lists")
if mc:
    named = sorted({m for s in plan["steps"] for f in s["files"]
                    for m in re.findall(r"migrations/(\d{4})_[^\s]*\.up\.sql", f)})
    derived = len(named) + 1  # 0001 is shipped and is never in a step's file list
    check(mc["stated"] == derived,
          f"sequencing.migration_count says {mc['stated']}; the step file lists name "
          f"{named} plus the shipped 0001, which is {derived}")
    # AND THE PROSE MUST AGREE WITH THE DERIVATION. THE FIRST DRAFT OF THIS COULD
    # NOT FAIL and is recorded because it is the class this file already carries
    # two instances of: it asserted `mc["word"] in note`, and the note recites its
    # own history — "v7.1 said THREE and v7.0 said TWO" — so every candidate word
    # was already a substring and the mutation went GREEN. The predicate has to be
    # about the CLAIM, not about the characters: exactly one `<WORD> up-files`.
    note = next((n for n in plan["sequencing"]["notes"] if n.startswith("MIGRATION COUNT")), "")
    NUMBER_WORDS = ("ONE", "TWO", "THREE", "FOUR", "FIVE", "SIX", "SEVEN", "EIGHT")
    check(f"{mc['word']} up-files" in note,
          f"the MIGRATION COUNT note does not claim {mc['word']} up-files, so the prose "
          f"and the derivation disagree")
    for w in NUMBER_WORDS:
        if w != mc["word"]:
            check(f"{w} up-files" not in note,
                  f"the MIGRATION COUNT note claims {w} up-files as well as "
                  f"{mc['word']} — two counts in one sentence")

# ADDED AT v7.2.1 — A RECORDED GATE TRANSCRIPT MUST BE REPRODUCIBLE FROM THE
# COMMIT IT IS RECORDED AGAINST. It was not: the "before" transcript this plan
# quotes as its headline was produced by a working tree in which this file's
# rulings block sat UNSTAGED, so `git show d781d29:scripts/check-plan.py` is 81
# lines with no stamp recomputation at all and prints `0 failure(s)` against the
# very plan the transcript reports two failures for. The transcript is real; the
# commit is not self-contained, and those are different things.
#
# The mechanical form is the cheapest one that works: the transcript carries the
# sha256 of the script that produced it, and the script hashes ITSELF. Edit the
# gate without re-running it and re-stamping, and the gate fails — which turns
# "re-run the gate after every script edit" from a discipline into a
# precondition. §6.5 already says a mutation count is evidence only if it was run
# at a stated commit; this is that rule applied to the gate's own output.
gt = plan.get("gate_transcripts")
check(gt is not None, "the plan records no gate_transcripts, so its quoted gate output is "
                      "unattributable to any version of this script")
if gt:
    cur = gt.get("current", {})
    mine = hashlib.sha256(open(__file__, "rb").read()).hexdigest()
    check(cur.get("script_sha256") == mine,
          f"gate_transcripts.current stamps script {str(cur.get('script_sha256'))[:12]}…; this "
          f"script is {mine[:12]}… — re-run the gate and re-stamp, or the recorded output "
          f"belongs to a file that no longer exists")
    check(bool(cur.get("output")) and cur.get("exit_code") is not None,
          "gate_transcripts.current records no output or no exit code, which is a citation "
          "rather than a transcript")
    # AND THE FOUR COUNTS IN THE RECORDED LINE ARE THE FOUR THIS RUN DERIVES.
    # Not circular: the failure count is not checked (that would be), but the
    # ids/routes/steps/deletions in the summary line are facts about the plan and
    # are recomputed here. A transcript stamped against the right script and
    # carrying the wrong counts is a transcript from a different plan — which is
    # exactly what happened once already, when four counts were quoted correctly
    # beneath two FAIL lines the script could not emit.
    m = re.search(r"(\d+) ids; (\d+) routes; (\d+) steps; (\d+) deletions",
                  cur.get("output", ""))
    check(m is not None,
          "gate_transcripts.current.output carries no summary line to check against")
    if m:
        want = (len(ids), len(rt), len(plan["steps"]), len(plan["deletions"]))
        got = tuple(int(x) for x in m.groups())
        check(got == want,
              f"gate_transcripts.current records {got} ids/routes/steps/deletions; this run "
              f"derives {want} — the transcript belongs to a different plan")
    for h in gt.get("historical", []):
        if h.get("reproducible_from_the_commit"):
            check(len(str(h.get("script_sha256", ""))) == 64,
                  f"gate_transcripts historical entry at {h.get('at_commit')} claims to be "
                  f"reproducible and stamps no script hash")
        else:
            check(len(h.get("why_not", "")) > 80,
                  f"gate_transcripts historical entry at {h.get('at_commit')} is not reproducible "
                  f"and says nothing about why")

# SUPERSEDED LITERALS — ONE MECHANISM, THREE ENTRIES, ADDED AT v7.2.2.
#
# A ruling that narrows a literal leaves the old one behind in several places,
# and this project has now had that three times in three revisions: "four
# sentences" survived in FOUR places after OE-17 deleted it, "the twelve codes"
# in ELEVEN when DEC-103 reopened the set, and `^image/(jpeg|png|heic)$` in FIVE
# when DEC-104 narrowed it. The first two got a bespoke check each. The third got
# this instead — three instances of one shape is a class, and a fourth bespoke
# check would have been the sixth count this document polices by reading.
#
# THE PREDICATE IS ABOUT THE CLAIM, NOT THE LOCATION, and that distinction was
# paid for: the first draft of the "four sentences" check was an allowlist of
# top-level keys and went RED ON THREE CORRECT SITES, because a permitted-key
# list cannot tell "still says four" from "says four was wrong". A superseded
# literal may be NAMED wherever its correction is beside it, and may not be
# ASSERTED anywhere.
#
# THE EXEMPTIONS ARE EACH A STRING THAT IS A RECORD RATHER THAN A CLAIM:
# `claimed_fixes[*].finding` is a verbatim copy of a lens report's `target` and is
# equality-checked against that report in this same run; `open_rulings[*].question`
# is what was asked, not what is true. Rewording either to satisfy a checker would
# falsify a record — the failure this repository has logged seven times from the
# other direction, a worker weakening the artefact instead of the code.
sl = plan.get("superseded_literals")
check(sl is not None, "the plan records no superseded_literals table")
if sl:
    for e in sl["entries"]:
        check(len(e.get("why", "")) > 80,
              f"superseded literal {e['id']} carries no challengeable reason")
        check(bool(e.get("superseded_by")) and bool(e.get("must_accompany")),
              f"superseded literal {e['id']} names no ruling or no required marker")

    def literal_sites(node, path=""):
        if isinstance(node, dict):
            for k, v in node.items():
                yield from literal_sites(v, f"{path}.{k}" if path else k)
        elif isinstance(node, list):
            for i, v in enumerate(node):
                yield from literal_sites(v, f"{path}[{i}]")
        elif isinstance(node, str):
            yield path, node

    # The table describes itself, necessarily — it must name the literals it
    # governs — so it is not scanned, on the same footing as the citation
    # exemption register.
    for where, text in literal_sites(plan):
        if where.startswith("superseded_literals"):
            continue
        low = text.lower()
        for e in sl["entries"]:
            needles = [e["literal"]] + list(e.get("also_matches", []))
            if not any(n.lower() in low for n in needles):
                continue
            if any(re.fullmatch(pat, where) for pat in e.get("exempt_paths", [])):
                continue
            check(e["must_accompany"].lower() in low,
                  f"{e['id']}: the superseded literal {e['literal']!r} survives at {where} "
                  f"without {e['must_accompany']!r} beside it — {e['superseded_by']}")

# §6.3 — a revision deletes something.
check(len(plan["deletions"]) > 0, "deletions is empty and carries no stated reason")

# §6.1a — a required lens answered NO must carry a reason, never silence.
for row in plan["review_lenses_required"]["table"]:
    if row["required"] == "YES" and row["has_run"] == "NO":
        check(row["by_whom"].startswith("—"),
              f"required lens {row['lens']} is unrun with no stated blocker")
    if row["required"] == "NO":
        check(len(row.get("by_whom", "")) > 40,
              f"lens {row['lens']} answered NO without a challengeable reason")

for f in failures:
    print("FAIL:", f)
print(f"{len(failures)} failure(s); {len(ids)} ids; {len(rt)} routes; "
      f"{len(plan['steps'])} steps; {len(plan['deletions'])} deletions")
sys.exit(1 if failures else 0)
