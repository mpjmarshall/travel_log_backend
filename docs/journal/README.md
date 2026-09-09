# The journal

**This index is the only new text in this directory. Everything else was moved
here verbatim from `CLAUDE.md` and must not be edited.**

`CLAUDE.md` was written step by step, in the same commit as the code, from the
first commit of this repository. It reached 414,310 bytes and 37 `##` sections,
and it was loaded into every agent session. It is append-only by design:
superseded sections are struck rather than rewritten, so it contradicts itself
on purpose — the Argon2 tuning argument is still in entry 19 and migration 0007
deleted Argon2.

That is what makes it evidence, and it is what made it unusable as an entry
document. `CLAUDE.md` is now a map of the system as it is today, under 12 KB.
This is the record.

**How to read it.** Each file is one `##` section, numbered by its position in
the original, which is what dates it. Nothing has been struck, reordered or
reconciled. A claim in a later entry corrects one in an earlier entry; both
stay. When an entry and `CLAUDE.md` disagree, the map is right about the code
and the entry is right about the reasoning that put it there.

**How to add to it.** A step appends a new numbered file, in the same commit as
the code it describes. Do not edit an existing one. If an entry is now wrong
about the system, the fix is in `CLAUDE.md`.

**The split is proved rather than asserted.** Concatenating entries **00 through
37** in numeric order reproduces the pre-split `CLAUDE.md` byte for byte.
Entries from 38 on are record written after the split, so they are not part of
that sum.

```bash
cat $(ls docs/journal/[0-9]*.md | head -38) | shasum -a 256
#   ba37a0ef2e3ec731ef3e2d91628fe8c4e956ddf350a9dee71b946460407fb13d
ls docs/journal/[0-9]*.md | wc -l
```

| file | section | lines |
|---|---|---|
| [`00-preamble.md`](00-preamble.md) | _(the file's own preamble, as it stood before the split)_ | 27 |
| [`01-comments.md`](01-comments.md) | Comments | 34 |
| [`02-the-gate.md`](02-the-gate.md) | The gate | 75 |
| [`03-the-spec-and-the-divergences-from-it.md`](03-the-spec-and-the-divergences-from-it.md) | The spec, and the divergences from it | 29 |
| [`04-the-go-1-25-0-literal-and-what-vs1-actually-measured.md`](04-the-go-1-25-0-literal-and-what-vs1-actually-measured.md) | The `go 1.25.0` literal — and what VS1 actually measured | 51 |
| [`05-layout.md`](05-layout.md) | Layout | 33 |
| [`06-the-stack.md`](06-the-stack.md) | The stack | 51 |
| [`07-what-vs1-shipped-that-it-was-not-asked-for-and-why.md`](07-what-vs1-shipped-that-it-was-not-asked-for-and-why.md) | What VS1 shipped that it was not asked for, and why | 28 |
| [`08-inherited-unfinished.md`](08-inherited-unfinished.md) | Inherited unfinished | 40 |
| [`09-vs1-what-was-decided-measured-and-declined.md`](09-vs1-what-was-decided-measured-and-declined.md) | VS1 — what was decided, measured and declined | 44 |
| [`10-vs1-mutation-proof-the-only-guard-vs1-has.md`](10-vs1-mutation-proof-the-only-guard-vs1-has.md) | VS1 mutation proof — the only guard VS1 has | 40 |
| [`11-vs1-backfill-the-tests-vs1-did-not-write.md`](11-vs1-backfill-the-tests-vs1-did-not-write.md) | VS1-BACKFILL — the tests VS1 did not write | 186 |
| [`12-vs2-config-the-redactor-the-pool-and-a-healthz-that-mean.md`](12-vs2-config-the-redactor-the-pool-and-a-healthz-that-mean.md) | VS2 — config, the redactor, the pool, and a /healthz that means something | 332 |
| [`13-vs1-image-tests-the-infrastructure-claims-exercised.md`](13-vs1-image-tests-the-infrastructure-claims-exercised.md) | VS1-IMAGE-TESTS — the infrastructure claims, exercised | 216 |
| [`14-vs1-fixes-the-review-s-eleven-findings-and-what-each-fix.md`](14-vs1-fixes-the-review-s-eleven-findings-and-what-each-fix.md) | VS1-FIXES — the review's eleven findings, and what each fix is proven by | 394 |
| [`15-vs3-the-envelope-twelve-words-an-etag-with-both-halves-a.md`](15-vs3-the-envelope-twelve-words-an-etag-with-both-halves-a.md) | VS3 — the envelope, twelve words, an ETag with both halves, and the chain | 350 |
| [`16-comments.md`](16-comments.md) | Comments | 70 |
| [`17-vs4-the-migration-runner-and-0001.md`](17-vs4-the-migration-runner-and-0001.md) | VS4 — the migration runner, and 0001 | 457 |
| [`18-vs6-argon2id-opaque-tokens-register-and-sign-in.md`](18-vs6-argon2id-opaque-tokens-register-and-sign-in.md) | VS6 — Argon2id, opaque tokens, register and sign in | 408 |
| [`19-vs7-one-conditional-read-one-trip-write-and-the-shape-th.md`](19-vs7-one-conditional-read-one-trip-write-and-the-shape-th.md) | VS7 — one conditional read, one trip write, and the shape the client already decodes | 361 |
| [`20-vs8-the-arc-the-gate-and-the-evidence.md`](20-vs8-the-arc-the-gate-and-the-evidence.md) | VS8 — the arc, the gate, and the evidence | 205 |
| [`21-vs8-sec-every-authenticated-route-had-no-ceiling-at-all.md`](21-vs8-sec-every-authenticated-route-had-no-ceiling-at-all.md) | VS8-SEC — every authenticated route had no ceiling at all | 247 |
| [`22-r1-the-shipped-code-was-wrong-in-eight-measured-ways.md`](22-r1-the-shipped-code-was-wrong-in-eight-measured-ways.md) | R1 — the shipped code was wrong in eight measured ways | 258 |
| [`23-r2-internal-media-and-a-signature-that-covers-everything.md`](23-r2-internal-media-and-a-signature-that-covers-everything.md) | R2 — internal/media, and a signature that covers everything the URL can do | 253 |
| [`24-r3-the-three-media-routes-and-a-reference-that-waits-for.md`](24-r3-the-three-media-routes-and-a-reference-that-waits-for.md) | R3 — the three media routes, and a reference that waits for the bytes | 363 |
| [`25-r4-the-seed-the-round-trip-four-lists-had-never-had-and.md`](25-r4-the-seed-the-round-trip-four-lists-had-never-had-and.md) | R4 — the seed, the round trip four lists had never had, and the first backup | 385 |
| [`26-r5-d3-s-cascade-a-stop-that-disarms-the-switch-and-a-tok.md`](26-r5-d3-s-cascade-a-stop-that-disarms-the-switch-and-a-tok.md) | R5 — D3's cascade, a stop that disarms the switch, and a token that is a hash | 393 |
| [`27-r6-cities-and-places-and-an-absent-key-that-means-leave.md`](27-r6-cities-and-places-and-an-absent-key-that-means-leave.md) | R6 — cities and places, and an absent key that means leave them alone | 467 |
| [`28-r7-photographs-and-walks-and-two-columns-that-are-not-on.md`](28-r7-photographs-and-walks-and-two-columns-that-are-not-on.md) | R7 — photographs and walks, and two columns that are not on a type | 426 |
| [`29-r8-the-public-read-and-the-reckoning-the-last-step-owes.md`](29-r8-the-public-read-and-the-reckoning-the-last-step-owes.md) | R8 — the public read, and the reckoning the last step owes | 218 |
| [`30-the-reckoning-what-is-guarded-by-nothing-whole-at-the-en.md`](30-the-reckoning-what-is-guarded-by-nothing-whole-at-the-en.md) | THE RECKONING: what is guarded by nothing, whole, at the end of the slice | 228 |
| [`31-agent-skills.md`](31-agent-skills.md) | Agent skills | 31 |
| [`32-arch-1-four-seams-deepened-and-three-of-my-own-claims-me.md`](32-arch-1-four-seams-deepened-and-three-of-my-own-claims-me.md) | ARCH-1 — four seams deepened, and three of my own claims measured wrong | 214 |
| [`33-arch-2-where-a-refusal-is-authored-and-the-line-r6-left.md`](33-arch-2-where-a-refusal-is-authored-and-the-line-r6-left.md) | ARCH-2 — where a refusal is authored, and the line R6 left to a human | 132 |
| [`34-arch-3-the-service-is-deleted-and-the-two-rules-it-was-h.md`](34-arch-3-the-service-is-deleted-and-the-two-rules-it-was-h.md) | ARCH-3 — the Service is deleted, and the two rules it was holding go home | 96 |
| [`35-deploy-1-one-flag-was-doing-two-jobs-and-only-one-of-the.md`](35-deploy-1-one-flag-was-doing-two-jobs-and-only-one-of-the.md) | DEPLOY-1 — one flag was doing two jobs, and only one of them could be off | 124 |
| [`36-review-3-the-stripped-possessives-and-the-review-s-eight.md`](36-review-3-the-stripped-possessives-and-the-review-s-eight.md) | REVIEW-3 — the stripped possessives, and the review's eight were forty | 51 |
| [`37-review-2-the-gate-runs-on-a-machine-now.md`](37-review-2-the-gate-runs-on-a-machine-now.md) | REVIEW-2 — the gate runs on a machine now | 60 |
| [`38-review-1-the-map-and-the-journal.md`](38-review-1-the-map-and-the-journal.md) | REVIEW-1 — the map and the journal, and a stamp that was already green | 107 |
