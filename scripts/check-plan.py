#!/usr/bin/env python3
"""agent-graph-spec-V4 §6.5 — mechanical invariants over docs/plan-v7.json.

An agent's claim to have re-derived something is not evidence. Every count,
cross-reference and id rule this plan states is checked here, by code, and the
exit status is the gate. Run it before the first critic pass and again after
every revision.

    python3 scripts/check-plan.py [docs/plan-v7.json]
"""
import json
import re
import sys

path = sys.argv[1] if len(sys.argv) > 1 else "docs/plan-v7.json"
plan = json.load(open(path))
failures = []


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
