# TODO

The tracker. Conventions are in [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)
and they are what make this file worth reading:

- **A box is ticked only when somebody ran something.** An untickable box stays
  untickable.
- **A closed entry is rewritten, not deleted**, and says what closed it.
- **A decision that fires its trigger is MOVED, not ticked** — ticking deletes
  the reasoning along with the box.
- **Counts are re-derived, never incremented.** Every number below carries the
  command that produced it. Four count-drift errors in this repository's history
  came from not doing that.

Seeded 9 September 2026 from the R8 reckoning
([`docs/journal/30-…`](docs/journal/30-the-reckoning-what-is-guarded-by-nothing-whole-at-the-en.md))
and every *guarded by nothing* section after it. It is the first time they have
been in one place with the reckoning's own counts re-derived.

Three tiers, and an entry says which it is in: **a named leg** (a test reddens),
**an artefact check** (a command asserts a fact about a built artefact), **a
human with a device** (nothing in the repository can reach it).

---

## Deployment blockers — needs a human decision

Each is a design decision or an external service, not a fix. They are on the
critical path before a stranger can send a request.

- [ ] **TLS and a reverse proxy.** Caddy has been deferred twice, and it is not
      only a certificate. All three rate limiters key on `RemoteAddr` through
      `httpx.ClientKey`, so behind any proxy each becomes **one bucket for the
      whole internet** — including the public share read, which is the only
      route with no identity at all. The `X-Forwarded-For` trust decision and
      the limiter-behind-proxy leg arrive together, and neither exists.
      `grep -rn 'ClientKey' internal/httpx/ | grep -v _test`

- [ ] **A mail adapter that delivers.** `internal/mail` has one `Sender` and it
      writes the code to the container log. **`MAIL_LOG_SENDER=1` is therefore
      the only configuration in which the binary boots at all** — measured: with
      it unset the api restart-loops with *"no mail provider is configured"*, and
      that is what broke `make test-image` and the arc for ten days. Which
      provider is the human's call. `ls internal/mail/`

- [ ] **A bucket backup.** `make backup` dumps PostgreSQL only. A database
      restore without a bucket restore is a log every reference of which
      resolves and points at nothing — **308 references against two objects** at
      fixture scale. The order is already written in the recipe: bucket first,
      database second.

- [ ] **Off-box and scheduled backups.** `make backup` writes into `backups/` on
      the same machine as the volume it protects, and nothing runs it.

- [ ] **Anything that reclaims a media object beyond `cmd/sweep`.** A begin that
      is never uploaded leaves a row and no bytes; a successful upload never
      committed leaves bytes nothing will ever reference; a seed that fails
      between the upload and the commit leaves two objects with no row.
      `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries the arithmetic.

---

## Guarded by nothing — a named leg would do it

- [ ] **Eighteen of the twenty-eight shipped defaults.** Ten are pinned by
      `TestTheShippedDefaultsAreWhatTheyAreDocumentedToBe`; the rest — ports,
      credentials, pool sizes, memory ceilings, `LOG_LEVEL`, `S3_REGION` — are
      asserted **present** and never **equal to anything**. Each is a judgement
      about whether a wrong value is a security or correctness problem.
      `grep -oE '\$\{[A-Z_0-9]+:-[^}]*\}' deploy/docker-compose.yml | sort -u | wc -l` → **28**

- [ ] **Seven constants asserted through themselves.** `migrateLockTimeout`,
      `IdleInTransactionTimeout`, `TouchInterval`, `MinShareTokenBytes`,
      `MaxNoteBytes`, `MaxCaptionBytes`, `maxVisitsPerStatement`. Every leg reads
      the constant, so each is self-consistent by construction and no number is
      defended by anything. Setting `TouchInterval` to thirty days is a green
      suite and a `last_used_at` that says every live session was last used at
      sign-in.

- [ ] **`auth.Memory` and `postgres.AuthStore` are not checked against each
      other.** Nothing asserts the twin refuses what the real adapter refuses.
      One divergence is named and deliberate (`Memory.AcceptInvite`); any other
      would be silent, and a handler leg over a twin that is wrong is evidence
      about nothing. `internal/media` has had the same gap since R2.

- [ ] **The two transactional refusals are database-only.**
      `refuseVisitsHeldElsewhere` and `refuseDroppingAnOccupiedOccasion` push the
      whole decision into SQL and guard state the same transaction is about to
      mutate. They have legs; those legs need PostgreSQL, so a developer running
      `TRAVELLOG_SKIP_DB=1` is not running them.

- [ ] **`make invite`'s derived DSN has no standing leg.** DEPLOY-1 fixed it
      building `postgres://$user:$user@…` — the username as the password — and
      proved it by hand against a non-default password. The arc's `testdb` phase
      is the shape that would hold it; it does not.

- [ ] **The window between `SpendInvite` and `CreateTraveller`.** The invite is
      spent, then the account is made, and there is no transaction. A process
      killed between them burns an invite and makes no account — the safe
      direction, and not atomic. Closing it needs a transaction in
      `auth_store.go`, which changes the rule that register opens none.
      **Trigger: a second operator, or an invite that costs something to mint.**

- [ ] **`RecordInviteUser` failing** leaves the account, the spent invite and no
      provenance. Nothing reports it.

- [ ] **Two shapes of sweep damage that no rule can catch.**
      `internal/auth/service.go:16` reads *"untuned in the same sense's Argon2
      parameters are"* — `sense` is a noun that can own, so only a grammar
      checker tells it from correct prose. `internal/auth/token.go:13` reads
      *"TokenBytes is the 32."* — a noun phrase removed from the middle, leaving
      a sentence that parses as English and says nothing. Rule 4 of
      `check-comments.py` catches a shape, not a class.
      `grep -rnE "//.*\b[A-Za-z]+s's\b" --include='*.go' .`

- [ ] **The narrowing of `Deps` is structural, not semantic.**
      `TestNoHandlerTakesTheWholeDepsBag` refuses the whole bag; nothing stops a
      handler being handed a port it does not need. `unguardedDeps` exempts `Now`
      on the argument that `Deps.Clock()` defaults it, and nothing checks that
      stays true.

- [ ] **The moved refusal messages are not asserted.** ARCH-2 moved 24 field
      refusals into `internal/logbook`; the legs assert `Field` always and `Why`
      only where it carries a measurement. A rewrite that made every reason
      unhelpful keeps the suite green. `Why` never reaches a client — the wire
      envelope is `{code, field}` — so this is a log inconsistency, not a
      client-facing one.

- [ ] **`refusalsAuthoredHere` is keyed on file and function.** Renaming a
      function reddens it, which is intended. Moving a refusal *within* an
      allowed function is invisible.

- [ ] **The timing half of the public read's equal-work property.** Revoked and
      unknown share tokens do the same number of store calls and mints —
      **counted**, `{lookups:1 reads:0 mints:0}` for both, with a positive
      control. Nothing measures them on a clock, deliberately: a timing assertion
      on a shared runner is a flake. A future difference costing no store call
      and no mint would not be seen.

- [ ] **A second traveller.** Every leg in the public-read work is one
      traveller's log, and 0006 ended the one-traveller rule, so one can now
      exist. The digest index is global and the row carries the traveller, so a
      token resolving to somebody else's trip is refused by construction rather
      than by a leg. **Trigger fired** — this was blocked by DEC-86 and is not
      any more.

- [ ] **`logbook.Objects` is satisfied structurally by `media.Store`** and
      nothing asserts the two stay compatible. The compiler catches it at the one
      call site, which is enough today and is one call site.

---

## Guarded by nothing — an artefact check would do it

- [ ] **Nothing checks that `docs/journal/` is still verbatim.** The hash was
      computed once, at the split. An edit to a moved section is a green gate.
      `cat $(ls docs/journal/[0-9]*.md | head -38) | shasum -a 256` →
      `ba37a0ef2e3ec731ef3e2d91628fe8c4e956ddf350a9dee71b946460407fb13d`

- [ ] **The map's accuracy.** `scripts/check-record.py` bounds `CLAUDE.md`'s
      size and says nothing about whether the package table, the route count or
      the auth flow are still true. Every number in it carries the command that
      produced it, which is the most a document can do for itself.

- [ ] **Nothing in `go test` reads either workflow.** A step deleted from
      `.github/workflows/check.yml` is a green suite and an ungated repository.
      The evidence for those two files is the runs themselves.
      `grep -rn 'TRAVELLOG_SKIP_DB' .github/` → nothing, on purpose.

- [ ] **`deploy/.env.example`'s values** are compared to nothing. The two
      artefact legs assert every variable compose interpolates is documented
      there; neither asserts a number matches.

- [ ] **`imageryNames` is a literal pair.** A third file in the client's bundle
      would be read by nothing and addressed by nothing, silently. Nothing
      compares the list to the directory.

---

## Guarded by nothing — a human with a device

- [ ] **`make backup`'s rotation beyond one file.** Keep-7 is written and one
      rehearsal produced one dump, so the `tail -n +8` branch has been read and
      not executed. A human with eight days.

- [ ] **`make seed`'s success path inside the arc.** The arc proves the
      *refusal* — exit code, message, traveller, database, unchanged row count.
      The loading half was run by hand, with the output in `docs/EVIDENCE.md`,
      and it cannot go in the arc as it stands: **the arc registers a traveller,
      so a successful seed and the arc are mutually exclusive in one project by
      construction.**

- [ ] **`-skip-media`** writes the `media_objects` rows without the bytes, which
      is a log whose covers all 404 at mint. Nothing asserts what it does and
      nothing passes it.

- [ ] **The `/dev/tty` half of the image tier's skip notice.** Proven through a
      temporary file, because this environment has no pty. What is proven is that
      the helper writes what it is given; not that a developer at a terminal sees
      the line.

- [ ] **The public envelope against a real reader.** There is no share page, so
      nothing has ever rendered the document. Whether fifteen minutes is long
      enough to read a trip, and what a browser does with a `no-store` response
      full of cross-origin image URLs, are questions a human with a browser
      answers.

- [ ] **`Vary: Accept-Encoding` against a real cache.** The leg asserts the
      header is set; nothing puts a cache in front of this server. Same step as
      Caddy.

- [ ] **Real S3.** Every S3 code asserted anywhere here is MinIO's. DEC-43's
      asymmetry cuts the right way for refusals — a stricter server also refuses
      — but `If-None-Match: *` on S3 is documentation, not a measurement, and so
      is S3's answer to a chunked PUT.

- [ ] **The client's half of every contract.**
      `docs/CLIENT-PREREQUISITES.md` is guarded by somebody in the other
      repository reading it: the `visits` omit-versus-empty rule, the cleared
      caption, the fifteen-minute wall, `log_image.dart`'s silent plate. Nothing
      here can check what the client sends.

- [ ] **`deploy/RUNBOOK.md` itself.** Every command in it was run against a real
      stack at `eef6a34`, and nothing re-runs them.

---

## Closed since the reckoning

Rewritten rather than deleted, with what closed each.

- [x] **`-race` is not in `make check`.** Closed 9 September 2026 by
      `.github/workflows/check.yml`, which runs
      `go test -race -count=2` over `internal/httpx`, `internal/auth`,
      `internal/httpapi`, `internal/admin` and `cmd/api` on every push and pull
      request. It is still deliberately out of `make check`, which stays four
      commands and fast.

- [x] **The image has only ever run on `arm64`.** Closed 9 September 2026 by
      `.github/workflows/slice.yml`, which runs `make test-image` and `make
      slice` on `ubuntu-latest` — x86_64. The probe cross-compiles for the
      daemon's architecture, so the tier follows the machine.

- [x] **`ADMIN_COOKIE_INSECURE` and `MAIL_LOG_SENDER` join the unpinned
      defaults.** Closed by task 4: both are in
      `TestTheShippedDefaultsAreWhatTheyAreDocumentedToBe`, pinned at `0`.

- [x] **Ten mangled doc comments (`is's`, `of's`).** Closed 9 September 2026,
      and the count was wrong: there were **forty**, then eight more of a shape
      the first rule could not see. Rule 4 of `scripts/check-comments.py` is the
      guard. Two shapes survive and are open above.

- [x] **`make slice` and `make test-image` are only as fresh as the last time
      somebody ran them.** Closed by the nightly workflow — which found both of
      them broken on its first run, along with a case-sensitivity bug in the
      record phase that could only fail on a machine nobody used.
