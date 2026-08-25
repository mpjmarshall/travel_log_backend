# plan-v7.2 — what changed, and what forced each change

Revised from 7.1 on 24 August 2026, at HEAD `d781d29`. Every change below names the finding or the
ruling that forced it. Nothing here is a preference.

**Revised to 7.2.1 on 24 August 2026** after an independent FixVerifier read v7.2 against the seven
lens reports and graded all 93 `claimed_fixes`: 81 LANDED, 5 PARTIAL, 3 FICTION, 3
CONTAINS-ITS-OWN-DEFECT, 1 GUARD-CANNOT-FAIL. Every one of the twelve non-LANDED verdicts is acted on
— see [What v7.2.1 fixed](#what-v721-fixed). None is argued with.

The gate is the arbiter: `python3 scripts/check-plan.py` was **red** before this revision and is
**green** after it, and fourteen of its checks are new (eleven at v7.2, three at v7.2.1).

```
$ python3 scripts/check-plan.py          # working tree at d781d29, before any edit
FAIL: the rulings stamp says 41823 bytes; the file is 80096
FAIL: the rulings stamp says e43a15e1d308…; the file is 6552054a5bb6…
2 failure(s); 48 ids; 23 routes; 8 steps; 10 deletions          exit 1

$ python3 scripts/check-plan.py          # after
0 failure(s); 65 ids; 23 routes; 8 steps; 14 deletions           exit 0
```

> **Read the word "working tree" above, because v7.2 did not write it and the omission was a real
> defect (NF-1).** That transcript is real — I ran it — but it is **not reproducible from d781d29**.
> The v7.1 rulings block sat in `scripts/check-plan.py` **unstaged** while d781d29 committed `docs/`
> only, so `git show d781d29:scripts/check-plan.py` is 81 lines with 14 `check()` calls and no stamp
> recomputation at all, and running *that* against d781d29's plan prints
> `0 failure(s); 48 ids; 23 routes; 8 steps; 10 deletions`, exit 0 — the same four counts, beneath
> two FAIL lines it cannot emit. `git log --oneline -- scripts/check-plan.py` returns two commits,
> `7b47bee` and `5c34406`, with nothing between them. **The transcript is real and the commit is not
> self-contained, and those are different things.** v7.2.1 adds the invariant that follows:
> `gate_transcripts` carries the sha256 of the script that produced the output, and the script hashes
> itself and compares.

---

## What v7.2.1 fixed

The FixVerifier confirmed the load-bearing things first, and that is what bounds the work below: the
blocker count of 21 is real (recounted one-to-one from the seven lens files, no id dropped or
invented), the four-migration derivation holds, `emit.go` is genuinely in R1/R6/R7 with the
City/Photo statement, DEC-89 lands in R1 with its three legs written against the client's own body,
**all seventeen `base.inputs` stamps re-measured by hand and all seventeen match**, and **all eleven
gate checks were mutated and all eleven went red**, plus two more of the verifier's own.

### The three FICTION entries — all three landed for real, none declined

Each cited a location whose text did not contain the claim. None was wrong on the merits; what was
wrong was the record saying it was done. That is §6.2's own incident happening three times inside a
revision that had just written the incident into its own risks.

| entry | the verifier's evidence | what v7.2.1 did |
|---|---|---|
| **OPS-MIN-11** | `time.Since` 0 occurrences; R1 item 7 says nothing about the startup ping | **Landed.** R1 gains item 12b — print the elapsed duration with the budget as a second field, so a DNS failure that took 250 ms stops reading as a 10-second wait. The cmd/api file-list entry names it. |
| **PERF-MIN-10** | `DEF-06` byte-identical to d781d29, field by field; `104.6` 0 occurrences | **Landed.** DEF-06 gains the shape that makes "untuned" actionable: 20 ms per verify at 64 MiB/t=1/p=4 (so the open risk is a weak *work factor*, not a resource ceiling), 104.6 ms and 256 MiB peak at m=128/t=2, and a trigger that says the number needed is a login rate and nothing here has one. |
| **CF-MIN-14** | `belt-and-braces` 0, `WHERE NOT EXISTS` 0, `exists-branch` 0 — "the claim's only evidence is the claim" | **Landed.** CLIENT-PREREQUISITES item 15: `_freshId`'s guard is belt-and-braces now that `PUT /v1/trips/{id}` is an unconditional upsert; the upsert is right and is not changed, and the `WHERE NOT EXISTS` split is named as a DEC-33 conversation. |

### The guard that could not fail, and the scope statement that was false about it

**SEC-MAJ-4 (GUARD-CANNOT-FAIL) and STO-BLO-1 (CONTAINS-ITS-OWN-DEFECT) are one leg.** PD-19 says
"**every** presign leg in R2 does three things it did not" and the lens target names two legs; v7.2
rewrote the checksum leg and left `TestABodyLongerThanTheSignedLength` byte-identical to v7.1 — no
header replay, only `resp.StatusCode < 400`. Under DEC-87 and DEC-88 the URL now signs `content-type`
and `if-none-match` too, so an unreplayed request answers **400 AccessDenied whatever the body
length**: the leg was green with `Content-Length` removed from the signature entirely, over the exact
mutation it existed to catch.

Rewritten. It now replays every signed header; asserts the S3 code **`SignatureDoesNotMatch`** with
the three neighbouring codes named in the failure message (`AccessDenied` = a header was not
replayed and the leg measures nothing; `XAmzContentChecksumMismatch` = the checksum caught it and the
length is unsigned, which is the mutation); carries a positive control that also proves the replayed
header set is **complete**; uses a **fresh key** for the oversized PUT, because DEC-88 makes the
first write-once and a second PUT there answers 412 for an unrelated reason; and adds a
chunked-bypass leg asserting `411 MissingContentLength`. The mutation "drop the length from the
signature" now reddens the length leg and leaves the checksum leg green — the pair v7.1 and v7.2 both
claimed and neither had.

### CF-MAJ-6 — the count was right and the deletion was not

The verifier re-derived the eighteen independently and confirmed every line number. What it found is
that **OE-17's deletion did not happen where OE-17 said**: `risks[7]` was byte-identical to v7.1 and
still read "four sentences of copy"; RULE-07's `what_is_already_true` was a **fourth survival OE-17
did not list**; and OE-17's own pointer was stale, because R1 work item 6 became DEC-94's gzip item
when R1 was rewritten.

All four corrected, OE-17 re-pointed — **and made mechanical**, which is the part worth having. The
phrase may now be *named in order to be corrected* and may not be *asserted*, tested as: any string
carrying it also carries "eighteen". Its own first draft was a location allowlist and went red on
three sites that were correct — a permitted-key list cannot tell "still says four" from "says four
was wrong". Rewritten as a predicate about the claim, **it then found three more survivals beyond
the two the verifier listed.**

### Two counting errors inside the correction to a counting error

- **NF-3 / item 5 — eleven screens is ten.** The three greps touch eleven screen *files*, and one of
  them is `settings_screen.dart`, the site correctly excluded. So it is **nineteen sentences across
  eleven screens, or eighteen across ten** — and v7.2 paired the post-exclusion sentence count with
  the pre-exclusion screen count, which is the same mixing error the count itself was correcting. The
  stamped fourth command is annotated with its real output (10) and the union command that produces
  11 is written beside it. PD-16's title now reads "eighteen client sentences across ten screens".
- **NF-4 / item 6 — the nineteenth site.** PD-16's class (ii) had absorbed `delete_sheets.dart:195`,
  which **none of the three greps matches**. It reads *"Your note goes with it. Nothing is
  recoverable — there is no bin"* — D1's **finality** claim, made on the way in, not a failed-save
  sentence. Counting it inside a set derived by greps that do not find it gives a headline of
  eighteen with nineteen items of work under it, and a designer handed the eighteen line numbers will
  never locate it. It is now **PD-23**, the retention correction, with its own reason
  (`photos_asset_fk` is RESTRICT, nothing in R1–R8 deletes a bucket object, and the sweep "has a
  schedule of nobody") and its own fix. Same pass, different item.

### NF-1 — the transcript, with the mechanism corrected

The verifier's evidence is right and its conclusion overshoots: it reads the unreproducible
transcript as fabricated. It was not — see the note under the transcript above. The finding stands
with the wording changed: **the commit is not self-contained.** Corrected in four places (the gate
docstring, `base.inputs[CLAUDE.md].status`, `M-GATE-MUTATIONS`, and SEC-MAJ-8's self-grade), and the
invariant it implies is now mechanical rather than stated.

### The five PARTIALs

| entry | what was missing | what v7.2.1 did |
|---|---|---|
| **OPS-MAJ-4** | "`-migrate-only` already works" — 0 occurrences in the plan | R1 item 8 names the split-migration alternative and declines it in the same sentence, with the reason (it changes the deploy *procedure*, not one line). |
| **SAF-MAJ-6**, **PERF-MAJ-4** | both pointed at "R3 work item 9 for the lower bound", which enumerated three additions to 0003 and not `CHECK (jsonb_array_length(points) > 0)` — and R7's file list holds no migration file, so no step wrote it | R3 item 9 now enumerates **four**, with the reason DEC-89's contract makes the bound necessary. |
| **OPS-MIN-13** | its own text said "one comment in the compose file is the whole of the fix" and then deferred the whole item with nothing to fire on | Split. The comment lands in **R2 item 14** (R2 already edits compose); restart-on-unhealthy stays in `next_slice` as a seventh-service conversation with the trigger it lacked — the first unattended box. |
| **SEC-MIN-12** | carried to an unscheduled list with no trigger | Gains SEC-MIN-9's trigger: before anything is published beyond loopback. |

### The remaining new findings

- **NF-2** — "five acceptance checks" is **four**, in four places at once. Recounted at d781d29: the
  string occurs exactly four times, all in R5–R8. R4 ran `make seed` first on its own line, so it
  needed the same reorder and never carried the string. **Five steps reordered, four carried the
  string** — the distinction the count kept losing.
- **NF-5** — `place.g.dart:33-35` is wrong on both branches; the visits decode is at **30-32**
  (33-35 is `plan:`, `coverAsset:`, `);`). `photo.g.dart:46-48` is **47-49**. Both corrected, because
  this plan's own standard is that a line number is a measurement.
- **NF-7** — DEC-99's twice-run guard runs against a `testdata` fixture migration that no file list
  named. R3's file list now names the pair.
- **NF-8** — R1 stated "each mutation names the OTHER leg that must stay green" and eight of nine
  did. The ninth now names leg five, and the pairing is load-bearing: if removing the *migrator's*
  lock timeout reddens the request-bound leg too, the three bounds are one bound wearing three names,
  which is OE-19's whole point.
- **NF-9** — R2 said `MEDIA_MAX_BYTES` joins "the other eight"; it is nine after R1 adds
  `REQUEST_TIMEOUT`.

### Three more mechanical checks, all mutation-proved

| # | mutation | result |
|---|---|---|
| M12 | remove `gate_transcripts` | `the plan records no gate_transcripts, so its quoted gate output is unattributable` |
| M13 | transcript records 64 ids, plan derives 65 | `records (64,23,8,14); this run derives (65,23,8,14) — the transcript belongs to a different plan` |
| M14 | an unreproducible historical entry with a one-line reason | `is not reproducible and says nothing about why` |
| M15 | append a comment to the script without re-stamping | `stamps script 012596dec393…; this script is 92f86e25433c…` |
| M16 | revert `risks[7]` to the deleted phrase — **the verifier's own finding, replayed** | `'four sentences' survives at risks[7].risk without its correction beside it` |
| M17 | revert RULE-07's `what_is_already_true` — **the second survival, replayed** | `'four sentences' survives at open_rulings[6].what_is_already_true` |

M16 and M17 are the ones that matter: a check earns the claim that it would have caught something
only by being run against that something.

### What turned out to be wrong when I looked

Two things, and both are refinements rather than reversals.

1. **NF-1's mechanism**, as the coordinator anticipated: the transcript is real, produced by an
   unstaged working tree, not fabricated. The finding stands; the wording changes.
2. **The first draft of the "four sentences" check** — my own — was a location allowlist and went red
   on three *correct* sites. A permitted-key list cannot express the distinction the finding is
   about. Recorded in the gate docstring as the fourth instance in that file of a check that could
   not fail as first written, and the first where fixing it immediately paid.

Everything else on the list reproduced exactly as stated when I ran it: `risks[7]` byte-identical,
RULE-07's fourth survival, the twin presign leg byte-identical, DEF-06 byte-identical,
`git show d781d29:scripts/check-plan.py` at 81 lines printing `0 failure(s)` and exit 0, the fourth
grep returning 10, and `delete_sheets.dart:195` matching none of the three commands.

---

## The blocker count, and where each one landed

**Ninety-three findings across seven lens reports: 21 blockers, 33 majors, 39 minors.**

```
$ python3 -c "import json,glob,collections; print([(f.split('lens-')[1][:-5],
  len(json.load(open(f))['findings']),
  dict(collections.Counter(x['severity'] for x in json.load(open(f))['findings'])))
  for f in sorted(glob.glob('docs/lens-*.json'))])"
client-fidelity 15 (5 blocker / 4 major / 6 minor)
database        15 (2 / 4 / 9)
operations      13 (3 / 6 / 4)
performance     12 (1 / 4 / 7)
safety          12 (3 / 5 / 4)
security        13 (3 / 5 / 5)
storage         13 (4 / 5 / 4)
```

**All 21 blockers land in a step. None is declined. None is silently dropped** — the gate now fails
if a blocker's `landed_in` is `next_slice`, and it fails if any lens finding has no `claimed_fix` at
all.

| blocker | lands in | what it is |
|---|---|---|
| SAF-BLO-1 | R1 | `PUT /v1/trips/{id}` answers 200 to `{id, name}` and leaves `trip_cities` at 0 with both dates null |
| CF-BLO-1 | R1 | the client's only date input produces a string the server refuses — 400 on every 'Add dates' |
| CF-BLO-2 | R1 | the write response splices `shareLinkId: null` over the phone's only copy of a live token |
| CF-BLO-4 | R1 | 18 unbuilt routes answer `not_found`, which the client's three deletes read as success |
| CF-BLO-5 | R1 | the format versions cannot exchange a document (already ruled; confirmed unlanded) |
| PERF-BLO-1 | R1 | `GET /v1/logbook` answers 200 with a valid ETag and a body cut mid-token |
| OPS-BLO-1 | R1 | nothing bounds a request; ten concurrent reads returned `http=000` while Docker said healthy |
| STO-BLO-1 | R2 | R2's own presign leg omits the signed header, so it is green with no digest checked |
| STO-BLO-3 | R2 | `MEDIA_MAX_BYTES` cannot be signed as a ceiling — SigV4 pins an exact value |
| STO-BLO-4 | R2 | nothing creates the bucket |
| STO-BLO-2 | R3 | the begin response never tells the client which headers to replay |
| SEC-BLO-3 | R3 | the content-type allowlist is referenced three times and enumerated nowhere |
| OPS-BLO-2 | R3 | a `-- migrate:no-transaction` failure is an unrecoverable boot loop with no re-runnability guard |
| OPS-BLO-3 | R4 | DEC-86 collides with `make seed` and locks the owner out with a published credential |
| SAF-BLO-3 | R4 | no backup anywhere, and `make slice` destroys the live volume |
| DB-BLO-1 | R6 | delete-then-insert of an identical `visits` array unfiles every photograph filed there |
| CF-BLO-3 | R6 (+R7) | `emit.go` is in no step's file list; bare `Place` and bare `Walk` marshal `null` lists |
| DB-BLO-2 | R8 | a published place carries every other trip's visits, with their dates and their notes |
| SEC-BLO-1 | R8 | the share capability reaches the log at four sites, not one |
| SEC-BLO-2 | R8 | the capability headers are wired into R3 and the capability route is built in R8 |
| SAF-BLO-2 | base | the plan certifies a rulings snapshot that does not exist — **this revision** |

Derived, not written: `sequencing.blocker_landing` carries the same table and the gate recomputes it
from `claimed_fixes`.

```
R1 7   R2 3   R3 3   R4 2   R6 2   R8 3   base 1        total 21
```

### What was DECLINED

**One finding, and it is a major rather than a blocker: SAF-MAJ-7** — requiring the trip's name in
the body of `DELETE /v1/trips/{id}`, mirroring the gate D3's sheet puts on the human.

It is declined along the branch the lens itself offers ("if that is judged over-strict for a
single-user loopback stack, then say so IN R5 with the reason"), and the reason is written into R5's
own work text rather than into a report, because *"D3 makes you type the name and the API does not"*
is exactly the asymmetry that reads as an oversight to the next worker. Three reasons, in PD-22:

1. The sheet's gate is a gate on the **human**, and its value is the pause before the typing. A body
   field gates the **client** — and the only client is the one that already drew the sheet.
2. It makes the API's guard and the sheet's guard two copies of one string that can drift. Rename a
   trip on one device and the other device's cached name no longer arms the delete: a failure mode
   the sheet does not have.
3. The compounding half of the threat is closed elsewhere. DEC-86 shuts registration after the first
   traveller, so a stranger cannot hold a token at all — and the lens's own note that "an
   unconfirmed destructive route behind an OPEN registration surface is two gaps that compound" is
   right about the half that was compounding.

The recovery side the lens also flagged **is** strengthened: R5 builds `DELETE /v1/auth/session`
*and* a sibling that revokes them all.

`claimed_fixes` records it as `landed_in: DECLINED` with a `why_declined`, and the gate refuses a
DECLINED entry that carries no challengeable reason — because a decline a verifier grades as a fix
is worse than a defect on a list.

Eight further findings are labelled **CARRIED, NOT FIXED** (OPS-MIN-13, PERF-MIN-10, SEC-MIN-9,
SEC-MIN-11, SEC-MIN-12, SEC-MIN-13, STO-MAJ-8, STO-MIN-13). They are deferrals with triggers, not
declines, and each says so in its own `verdict_expected`.

---

## The rulings stamp — re-derived, not copied

```
$ wc -c < docs/rulings-v3-pending.json
80096
$ shasum -a 256 docs/rulings-v3-pending.json
6552054a5bb676615b4241e905b04386e7001eecc55d26cb60029923dc9c1b07
$ python3 -c "import json;r=json.load(open('docs/rulings-v3-pending.json'))['rulings'];print(len(r), r[0]['id'], r[-1]['id'])"
51 DEC-43 DEC-102
```

`base.inputs` now says **FIFTY-ONE rulings, DEC-43..DEC-102**, with `count`, `first` and `last` as
separate stamped fields.

**Nineteen of them — DEC-84 through DEC-102 — had zero mentions across all 261 KB of plan-v7.1.**
That is the third stale certification in this namespace and the largest. The first reused three
ruling ids; the second re-ran the hash correctly and left the word THIRTY-TWO standing beside it.

**A second stale stamp was found while there, and no lens reported it:** `CLAUDE.md` was certified
at 193,119 bytes / `badfeb31…` against a file that is **207,684 / `335a39ca…`**. It is now recomputed
by the gate too. Two recomputed stamps, not one.

---

## What each of the nineteen unread rulings changed

| ruling | what it forced | where |
|---|---|---|
| DEC-84 | `S3_PRESIGN_TTL_PUBLIC = 15m`. **RULE-07 is CLOSED** — it was a stated blocker on R2 — and the leg OE-15 deleted is now *mandated* by the ruling that ordered the deletion | R2, RULE-07 |
| DEC-85 | share tokens hashed at rest; DEC-10's plaintext half reversed. **Migration 0004**, which is why the count moves to four | R5 |
| DEC-86 | registration closes after the first traveller. **RULE-10 CLOSED**; moves out of `next_slice` and into scope | R5 |
| DEC-87 | Content-Type SIGNED into the presigned PUT, so DEC-51's allowlist reaches the object and not only the row | R2 |
| DEC-88 | `If-None-Match: *` signed (write-once addresses) **and** `uploadHeaders` in the begin response | R2, R3 |
| DEC-89 | **absent means leave alone.** Every write field becomes a pointer. A fix to SHIPPED code | R1, R6, R7 |
| DEC-90 | the client normalises with `.toUtc()`; the server keeps refusing a zoneless string | R1 |
| DEC-91 | the emitted Trip gains `"shared"`, so revocation survives DEC-85 | R1 |
| DEC-92 | a `pg_dump` target in R4, one restore rehearsal, `make slice` under its own project name | R4 |
| DEC-93 | `Walk.points` capped low; the track stays in the log document | R7 (PD-21) |
| DEC-94 | gzip now, and WriteTimeout stops being the only bound on the read | R1 |
| DEC-95 | `restart: always`, log rotation, `mem_limit`, and the sizing numbers written down | R2 |
| DEC-96 | a request gets a bound, a statement gets a bound, an outage answers 503 + `Retry-After` | R1 |
| DEC-97 | `make seed` refuses when any traveller row exists; the passphrase is generated per run | R4 |
| DEC-98 | the media store creates the bucket at boot | R2 |
| DEC-99 | a no-transaction migration must be proved re-runnable | R3 |
| DEC-100 | `TouchSession` leaves the traveller's advisory lock and becomes granular | R5 |
| DEC-101 | `durationUs`, `travellerId`, the route pattern, and one ERROR line per 500 | R1 |
| DEC-102 | the emitted body is 99,271 bytes and the read is ten queries — both corrected where written | base, R1, R4 |

---

## The changes with the sharpest consequences

### R1 stops being the migration step

It was `SMALL` and is now `LARGE`, and that is the largest single change v7.2 makes to the plan's
shape. **Seven of the twenty-one blockers are defects in code that is already running**, and R1 is
the only step before R2 adds a service. DEC-89 says it in terms: *"THIS IS A FIX TO SHIPPED CODE, so
it lands in R1 and not in R6."*

R1's title, size, why_first, file list, work, test strategy, acceptance check, verified_by and commit
message are all rewritten. Eleven legs are written out in full, and **every mutation names the other
leg that must stay green** — a pair rather than a single assertion, because "this reddens" is
satisfiable by a mutation that breaks everything.

DEC-100 deliberately does **not** land in R1. It is auth work and it goes to R5 with DEC-86: putting
every measured defect into one step is how a step stops being reviewable.

### `emit.go` gets a file list (CF-BLO-3, PD-15)

```
$ python3 -c "import json;d=json.load(open('docs/plan-v7.json'));print([f for s in d['steps'] for f in s['files'] if 'emit' in f.lower()])"
[]        # at v7.1
```

The definition_of_done carried the rule and there was nowhere to land it. `EmitPlace` joins R6,
`EmitWalk` joins R7. **City and Photo need none, and the reason is stated in the same breath**:
neither carries a list field, so there is no nil slice for the marshaller to write as `null`. Adding
two more for symmetry would be the empty forwarding method DEC-62 warns against one layer up. The AST
sweep moves to R6 — the first step that can violate the rule — rather than R8.

The gate now fails if `internal/logbook/emit.go` is in no step's file list.

### The "four client sentences" are eighteen (CF-MAJ-6, PD-16, OE-17)

Re-derived on branch `wipe/mock-data` with the lens's three commands rather than transcribed:

```
$ grep -rn 'could not be saved' lib/src/screens/ | grep -v '///' | wc -l   # -> 16
$ grep -rn 'your log is unchanged' lib/src/screens/ | grep -v '///' | wc -l   # -> 2
$ grep -rn 'stops working' lib/src/screens/ | wc -l                        # -> 1
$ grep -rn 'could not be saved' lib/src/screens/ | grep -v '///' | cut -d: -f1 | sort -u | wc -l   # -> 11 screens
```

Nineteen sites, eleven screens — one per writing control, which is the cross-check that the count is
right. Split into three classes:

- **(i) seventeen that assert an outcome** a network write cannot guarantee. `trip_screen` 263;
  `edit_sheets` 383, 453, 514, 603; `to_file_screen` 101, 102; `add_city_screen` 136 and 164;
  `share_sheet_screen` 240; `refile_photo_sheet` 197; `edit_traveller_sheet` 190; `profile_screen`
  241; `delete_sheets` 165, 366, 608; `new_trip_screen` 164.
- **(ii) one that promises revocation**, `delete_sheets` 649, written against DEC-84's fifteen
  minutes. SAF-MIN-10 puts D1's *"Nothing is recoverable — there is no bin"* in the same class, on the
  same mechanism.
- **(iii) one that needs NO change and is named as excluded**: `settings_screen.dart:423`, which
  writes `preferences.json` and genuinely stays local. Naming the exclusion is the point — a list of
  eighteen with no exclusion sends a designer to rewrite a sentence that is still true.

`delete_sheets.dart:608` — D3's *own* failed-save line, on the sheet where the user types a trip's
name out to destroy it — is in neither earlier list. **This is the fourth instance of the client's
rule 9: recount the whole table, not the row somebody names.** v4 corrected v3's misattribution of
D3's "stops working" from H1 and did not recount the set, which is how the row below it was missed.

### `docs/CLIENT-PREREQUISITES.md` — five items added, one struck

R1 creates it and R1's outline is rewritten. **Added:** DEC-85's `shareLinkId` consequence; DEC-84's
number with PD-16's corrected count; DEC-61's *actual* consequence (register does not sign you in —
two calls, and the first screen after must render a traveller with no name); the `refilePhoto`
signature change (it gains `visitId` and `visitAt`, at one call site, M2.2); DEC-34's cache and the
three things logout clears.

**Struck:** the fixture removal and the pinned clock. Both are already done on `wipe/mock-data` and
`main.dart`'s own comment records it. Listing it as work sends somebody to undo nothing, so it is
stated as *"already done — verify, do not redo."*

Also folded in: DEC-90's `.toUtc()` **and its local-format consequence**; DEC-91's `shared` field and
H1's new third state; DEC-88's *412-means-success* retry rule; DEC-93's client-side track decimation;
the twelve-code error table in three lanes (RETRY / REPORT / RE-AUTHENTICATE) with
`unsupported_format` in its own lane that must never retry (PD-17); DEC-47's real consequence —
**five `AssetImage` constructions and zero declared assets, so without it every image in the app is
the failure plate** (PD-16, M-ASSETS-ZERO); and the `LogbookSource` mapping (PD-18), which the plan
mentioned zero times and without which a user whose server is unreachable is told to begin, about a
record that exists.

### The storage lens's four blockers

- **STO-BLO-1** — R2's leg replays every signed header, asserts the S3 error **code**
  (`XAmzContentChecksumMismatch`) rather than `StatusCode < 400`, and carries a **positive control**
  in the same function. Four failure modes land in the 4xx class and only the last is the control;
  the leg's message names all four.
- **STO-BLO-2** — DEC-88's `uploadHeaders`, with the leg that keeps it honest: the map's key set
  **equals** the URL's `X-Amz-SignedHeaders` minus `host`, in both directions.
- **STO-BLO-3** — OE-18 deletes the sentence and PD-20 replaces it with the two that are both needed:
  `MEDIA_MAX_BYTES` is an API-side refusal to **mint**, and the signature pins the **exact** declared
  `byteSize`.
- **STO-BLO-4** — `EnsureBucket` at boot, `depends_on: minio: service_healthy`, the shipped tag
  pinned, and the definition of done asserting a round-trip PUT+GET rather than three healthy
  processes.

### The operations lens's three blockers

- **OPS-BLO-1** — OE-19 **deletes the belief** that `lock_timeout` closes the stall class. It bounds
  the migration's wait and nothing else. DEC-96's three bounds land together in R1, plus the 503
  branch and `start_period: 150s`.
- **OPS-BLO-2** — DEC-99's four parts in R3. **The vacuity is named rather than hidden:** 0003 as
  specified is entirely transactional, so the twice-run test's subject today is a fixture migration,
  and the leg asserts the subject set is non-empty before running it.
- **OPS-BLO-3** — DEC-97 in R4 and DEC-86 in R5, together, because together they were the collision.

### DEC-92, and `make slice`

`make backup`, one recorded restore rehearsal, `make slice` under its own `COMPOSE_PROJECT_NAME`, and
the bucket-before-database ordering stated for when R2's copy lands. **The five acceptance checks
that read `make seed && make check && make slice` now read `make check && make slice && make seed`**,
and the old order is a gate failure rather than a convention.

### Two numbers corrected (DEC-102)

**99,271 bytes, not 85,422.** The second figure is the *client's* format-1 file; DEC-46's 64-hex ids
replaced 31–32 character bundle paths across 284 photographs plus three cover columns. Every size
argument was ~16% optimistic and the drift is structural. **This is the number DEC-31 rests on.**

**Ten queries, not six.** `read_tx.go`'s "six lists" is a fair description of the document;
`logbook_store.go`'s "six queries" is a count and it is wrong. Ten is also what makes the 3 ms 304
legible: nine round trips at ~0.3 ms over 0.176 ms of server work.

### The migration count, derived

```
$ python3 -c "import json,re;p=json.load(open('docs/plan-v7.json'));
  v=sorted({m for s in p['steps'] for f in s['files'] for m in re.findall(r'migrations/(\d{4})_[^ ]*\.up\.sql', f)});
  print(['0001 (shipped)']+v, len(v)+1)"
['0001 (shipped)', '0002', '0003', '0004'] 4
```

**FOUR**, not three. 0002 (R1), 0003 (R3), 0004 (**R5 — DEC-85's `token_hash`**). The three
candidates that do *not* add a file, checked rather than assumed: DEC-91's `shared` is a correlated
subquery; DEC-102's date fix is three scan types; DEC-99's guard is a test plus a greppable header
rule, because 0003 carries no `-- migrate:no-transaction` directive at all.

---

## The open RULE-xx items, re-checked against the nineteen unread rulings

| | status |
|---|---|
| RULE-01 | CLOSED (DEC-76) — unchanged |
| RULE-02 | CLOSED (DEC-77) — unchanged |
| **RULE-03** | **OPEN, and WIDENED.** GATE-L12's clearance now has *two* candidates, not one: OE-7 deletes `GET /v1/me`, **and DEC-86 changes an error state on the approved `register` sequence diagram** — whose request body the clearance already lists as KNOWN OPEN. DEC-91's additive `shared` field is judged NOT material and is recorded as judged. |
| RULE-04 | CLOSED (DEC-78) — unchanged |
| **RULE-05** | **OPEN, and NARROWED.** Its compression half is closed: DEC-94 puts gzip in the Go server, so this is no longer blocking. The proxy hop remains — the public read's limit does not bind until `ClientKey` learns X-Forwarded-For. |
| RULE-06 | CLOSED (DEC-78) — unchanged |
| **RULE-07** | **CLOSED by DEC-84.** Fifteen minutes. R2 is unblocked and the replacement leg is restored — and it is *mandated* by the ruling, which names the deletion OE-15 made before it saw the ruling that ordered it. |
| **RULE-08** | **CLOSED by DEC-85.** Share tokens hashed. Read it with DEC-91: neither is complete alone. |
| **RULE-09** | **OPEN.** Checked against all nineteen unread rulings; none touches the allowlist, so it stays open rather than being closed by accident. DEC-87 raises its stakes slightly — the allowlist now reaches the object and not only the row — which makes an unused entry worth slightly less. |
| **RULE-10** | **CLOSED by DEC-86.** Registration closes. It moves out of `next_slice` and into R5. |
| **RULE-11** | **NEW, OPEN.** All 18 unbuilt routes answer `not_found`, the same word as "that trip is not in your log", and the client's three delete methods read an unknown id as success. The client half is scheduled regardless; whether the closed twelve-code vocabulary wants a thirteenth word is a DEC-12 decision and this plan does not open a closed ruling on its own initiative. |

---

## §6.3 — what this revision deleted

Four new deletions, OE-16 through OE-19:

- **OE-16** — `DEF-03`, the deferral of `statement_timeout` and
  `idle_in_transaction_session_timeout`, and R4 item 8's conditional clause that carried it. DEC-96
  chose the numbers. A deferral whose trigger has fired and whose number has been ruled is not a
  deferral.
- **OE-17** — the phrase *"DEC-56's four sentences"* wherever this plan carried it.
- **OE-18** — PD-11's sentence that `MEDIA_MAX_BYTES` goes into the signature. SigV4 signs an exact
  value; a worker reading it literally ships a URL that accepts one file size.
- **OE-19** — the claim that `lock_timeout` closes the database lens's stall finding.

`deferred_decisions` goes 6 → 5 → 8: DEF-03 deleted, DEF-07 (media backup), DEF-08 (the two orphan
classes, with the arithmetic), DEF-09 (per-resource ETags, with the trigger as a *number*) added.
**The numbering is left with a hole rather than closed up**, because renumbering is what produced the
collision PD-00 exists for.

---

## Two places the inputs contradicted each other

**1. Does presigning touch the network?** The storage lens measured
`PresignHeader("no-such-bucket-xyz", …)` returning *"The specified bucket does not exist"*. DEC-98
and the operations lens report that presigning against a missing bucket **succeeds**, because it is
offline signing, and the failure surfaces on the phone as `NoSuchBucket` on the PUT.

**Both are true under different preconditions**, and STO-MAJ-7 is the reconciliation: minio-go
resolves the bucket region with a real network round trip on the **first** presign per bucket and
caches it. Cold cache → the region lookup fails. Warm cache → offline signing succeeds. The
resolution is written into R2's own text with both measurements quoted, **and the plan does not rest
on it**: `EnsureBucket` at boot is correct under both readings, because it makes the bucket exist
*and* warms the cache. That is the honest reason to record the disagreement rather than adjudicate
it.

**2. Does the plan's `lock_timeout` close the stall class?** The database lens (DB-MIN-7) asked for
`lock_timeout` and v7.1 treated adding it as closing the finding. The operations lens (OPS-BLO-1)
executed what that leaves and it is not a refinement — ten concurrent requests returned no status at
all. Resolved in favour of operations, with `lock_timeout` **kept**: DEC-96 says it is "the third of
three and not the whole." The over-claim is deleted as OE-19 and DB-MIN-7's `claimed_fix` records the
correction.

---

## §6.5 — the eleven new mechanical checks, each mutation-proved

Every check below was run against a mutated copy of the plan and produced a distinct message.
Mutations applied to copies in a scratch directory; the source file was never edited (the client's
own rule: restore by file copy, never `git checkout`).

| # | mutation | result |
|---|---|---|
| M1 | rulings stamp `count=32`, `last=DEC-83`, hash still correct | `count=32; the file holds 51` + `DEC-43..DEC-83; the file runs DEC-43..DEC-102` |
| M2 | `CLAUDE.md` stamp back to 193119 | `the CLAUDE.md stamp says 193119 bytes; the file is 207684` |
| M3 | drop one `claimed_fix` (OPS-BLO-2) | `1 lens findings have no claimed_fix` + count mismatch + blocker-lands-nowhere |
| M4 | a blocker's `landed_in` set to `next_slice` | `blocker STO-BLO-4 lands in 'next_slice'` |
| M5 | remove `why_declined` from SAF-MAJ-7 | `claimed_fix SAF-MAJ-7 is DECLINED with no challengeable reason` |
| M6 | R6's acceptance check back to `seed && check && slice` | both the one-line and the whole-field ordering checks |
| M7 | remove `emit.go` from every file list | `no step's file list names internal/logbook/emit.go` |
| M8 | `migration_count.stated = 3` | `says 3; the step file lists name ['0002','0003','0004'] plus the shipped 0001, which is 4` |
| M9 | `migration_count.word = THREE` | `does not claim THREE up-files` + `claims FOUR up-files as well as THREE` |
| M10 | strip the count from the stamp's `command` | `the rulings stamp's command does not carry the count it produced` |
| M11 | `blocker_landing.counts["R8"] = 4` | `says {…'R8': 4…}; claimed_fixes derives {…'R8': 3…}` |

### One of the eleven failed its own proof on the first draft

M9 went **green**. The check asserted `mc["word"] in note`, and the MIGRATION COUNT note recites its
own history — *"v7.1 said THREE and v7.0 said TWO"* — so every candidate number-word was already a
substring of the text being checked. It now asserts exactly one `<WORD> up-files`, and both
directions of the mutation redden with distinct messages.

**That is the third instance in `check-plan.py` of a check that could not fail**, and it is written
into the file's docstring rather than quietly corrected. The other two are on record: v7.1's citation
exemption, which exempted three ids *globally* and passed while two real dangling citations stood in
a step's file list; and v7.0's whole rulings stamp, which reported 0 failures because the rulings
file was not in the uniqueness set.

### One count in this revision went wrong the same way

The first draft of the prose note summarising where the blockers land said **R8 FOUR, R6 ONE, R3
TWO** — three of seven numbers wrong, while the per-finding table beside it was correct. That is the
fifth count in this project's history to go wrong from being carried rather than run. It is now
`sequencing.blocker_landing`, a derived field, and M11 is its mutation proof.

---

## What a FixVerifier should check hardest

§6.2: the planner does not grade its own fixes, and 93 entries is 93 chances for a fix to be
*believed* rather than made.

1. **SAF-MAJ-7** — the one DECLINED entry. Grade the *reason*, not the absence.
2. **The eight CARRIED, NOT FIXED entries.** "Carried to next_slice" is the shape a deferral takes
   when it is really an omission.
3. **SEC-MAJ-4.** It was landed at v7.1 and *corrected at v7.2 by a different lens*: the control
   survives and the sentence describing it did not. That is the §6.2 incident happening in slow
   motion inside one document.
4. **SEC-MAJ-8.** The namespace split held and the recomputed stamp held — and the failure they were
   written for recurred anyway, because a stamp check catches a stale field and nothing catches a
   stale reading.
5. **CF-BLO-5, DB-BLO-1, DB-BLO-2 and the six SEC entries marked "UNCHANGED FROM v7.1."** An entry
   that says nothing changed is the easiest kind to write and the easiest kind to be wrong about.

---

## What is still open, and needs a human

- **RULE-03** — does GATE-L12's clearance survive `GET /v1/me`'s deletion *and* DEC-86's change to the
  approved `register` diagram's error states?
- **RULE-05** — Caddy, still on its second deferral rather than a third: v7.2 does not defer it again, it takes the compression half out and does it here.
- **RULE-09** — should `heic` be in the allowlist?
- **RULE-11** — does the closed twelve-code vocabulary want a thirteenth word for "this build does
  not have that route"?
- **PD-21's number.** DEC-93 rules "a few hundred" points and this plan implements **500**, with the
  derivation written out. It is provisional in the same tier as the Argon2 parameters — asserted as a
  value so it cannot move unnoticed, with nothing claiming it is the right value.
- **Nothing has read v7.2.** The two lenses that produced v7.1 read v7.0; the other five read v7.1. So none has seen R1's
  new shape, DEC-89's pointer contract, the fourth migration, or the reordered acceptance checks. A
  lens finding is evidence about the artefact it read.
