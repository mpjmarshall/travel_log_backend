# Hosted multi-tenant Travel Log — the spec

*Settled 29 August 2026, by interview. Nothing here is implemented.*

This is the **product decision record** for turning Travel Log from one
person's logbook on one box into a hosted service many people sign up for. It
is not a plan: it says what is being built and why, and the phase map at the
foot says what order. **Each phase needs its own implementation plan** — this
document is deliberately too large to execute directly, and the subsystems are
independent enough that one plan covering all of them would be wrong by the
time the second was started.

The operational half of a public deploy is **not** repeated here. It is in
`BEFORE-A-PUBLIC-DEPLOY.md`, which already covers sizing, the bucket's
absent lifecycle rules, restore ordering, the S3 secret, TLS, and the nine
unticked checks on `GET /l/{token}`. Where a decision below touches one of
those, it names the section rather than restating it.

---

## What this is, and what it is not

**It is:** many people, each with a private logbook, sharing by public link.

**It is not a social product.** No following, no feed, no discovery, no likes,
no comments. That was put and deferred as *a separate product decision rather
than a later phase of this one*, because it is not this codebase plus
features: it is a second backend service, and the client's whole-log-in-one-GET
assumption does not survive it.

**The single-user mode is retired.** It stays runnable for development and
stops being a supported product. Keeping both means making every future
decision twice, and two products maintained by one person means both are
neglected.

**Demand is untested.** This was described as aspirational — the app is good
and it feels like it should be shared — rather than a response to anyone
asking. The phase map carries a checkpoint for that, and the checkpoint is
before the expensive half.

---

## What already works, measured rather than assumed

The substrate is multi-tenant today. This was not taken on trust:

- **Ten travellers were seeded and exercised end to end.** Each signed in,
  read a log containing only their own trip, signed out with a `204`, and the
  retired token answered `401`. Ten for ten.
- **Composite primary keys isolate correctly.** `t02` was made to claim the
  exact trip id and city id `t01` already held. Both rows coexisted and
  `t01`'s data was untouched — `(traveller_id, id)` throughout.
- **Bucket keys are already namespaced per traveller**: `media/keys.go`
  addresses objects as `<traveller-uuid>/<sha256>`, with `travellerRe`
  deliberately narrow so a `%` cannot let one traveller's key reach outside
  their prefix. No two travellers ever share a stored object, so deletion is a
  clean prefix delete and one person's purge cannot break another's
  photographs. It costs duplicate storage when two people upload the same
  bytes, which is the right trade.
- **The register route is the only thing that says single-user.** DEC-86
  refuses once any traveller row exists. The schema has no such constraint —
  `travellers` is unique on `lower(email)` and nothing else.

So the transformation is at the edges: identity, money, lifecycle, and the
web. The core is already shaped for it.

---

## The decisions

### Identity

1. **Magic code, not magic link, and not a passphrase.** Six digits, mailed,
   typed into the app. Chosen over a tapped link because a link needs
   Universal Links, a verified domain and an `apple-app-site-association`
   file, and this project has **no deep linking at all** — `routes.dart`
   navigates by name only. A code works identically on every platform and has
   far less that can silently break, which matters when a broken login means
   nobody can get in.

2. **The passphrase path is deleted, not disabled.** Argon2id, the
   `passphrase_hash` column, `POST /v1/auth/register`'s current shape and A1's
   passphrase field all go. Dead auth code is the worst kind to keep: nobody
   exercises it, so nobody notices when it rots, and it stays a live attack
   surface. It is in git.

   **This retires DEC-107** — the check-then-insert race on first registration
   — because first registration stops working that way. The ruling named "a
   second traveller" as its own trigger; the trigger fires and the fix is not
   the unique index it predicted, it is that the code path goes.

3. **The code's protection is per account, not per address.** Ten-minute
   expiry, invalidated on use, and at most five wrong attempts before that
   code dies and a new one must be requested. Per account is load-bearing: the
   existing limiter keys on client address, so an attacker rotating IPs is not
   slowed by it at all. **Requesting a code is rate-limited too**, or the
   service becomes a mail cannon pointed at someone else's inbox.

4. **Sessions stay 30 days and become sliding.** Extended on use, so an active
   user is never logged out and an abandoned device still expires. This
   directly softens the mail dependency: the fewer sign-ins needed, the less a
   delivery outage costs. `DELETE /v1/auth/sessions` — sign out everywhere —
   already exists and is unchanged.

5. **Email is a hard dependency and a single point of failure.** With magic
   codes, a delivery outage means *nobody* can sign in, not merely that resets
   are blocked. Mitigated by long sliding sessions and by alerting on delivery
   failure, because silent mail failure looks exactly like a broken app. A
   transactional provider (Postmark, Resend); not self-hosted, because
   deliverability is a specialist job and a solo operator's links will land in
   spam without them knowing why.

### Registration and abuse

6. **Invite codes replace DEC-86's land-grab guard.** Single-use, no expiry,
   minted by hand from a Make target. At ten users automation is not worth
   writing, and by-hand minting means you always know who let each person in.
   DEC-86's *reason* — a stranger registering first on a deployed instance
   gets an authenticated account with a 600/min budget and `?photos=delete` —
   is satisfied by the invite, not by the one-traveller rule.

7. **Public share links stay, and ship with a way to stop them.** A reachable
   report route, the ability to kill a share link, and the ability to suspend
   an account, all before the first stranger signs up. Invite-only keeps the
   exposure small while this is young.

   Note the interaction already documented: revocation is not immediate. A
   photograph in a share envelope is a presigned URL with a fifteen-minute
   public life, and stopping a share stops new links at once and stops nothing
   already open. Four sheets already carry that sentence.

8. **Account deletion is immediate.** Rows cascade, the bucket prefix goes,
   every share link they issued stops resolving. It collides with the same
   fifteen-minute window, and the honest copy says so. The export already
   exists and writes `logbook.json`; it deliberately carries no photographs,
   which must be stated plainly rather than implied.

### Money

9. **Freemium, metered by storage, 1GB free.** Photographs are the cost, so
   the thing that costs is the thing that is metered. Roughly 250 phone
   photographs.

10. **Over the cap, only uploads refuse.** Enforced at `POST /v1/media`, where
    `byteSize` is already in the request and known before any bytes move — the
    cheapest possible place to refuse. Everything else keeps working: read,
    notes, trips, sharing. Deleting photographs frees space immediately.
    **Never make someone read-only over storage** — that punishes the wrong
    action and, for a personal record, reads as hostile.

11. **Billing is Stripe on the web; the app never mentions buying.** Apple
    generally requires In-App Purchase for digital subscriptions consumed in
    the app, at 15% under the Small Business Program or 30% above. Web-only
    checkout keeps the full price and avoids receipt validation and
    restore-purchases entirely. The app reflects the account's tier and offers
    no way to change it.

    **VERIFY THE CURRENT RULES DIRECTLY BEFORE BUILDING THIS.** Anti-steering
    and external-link policies moved during 2025 and this decision was taken
    on a recollection that was explicitly flagged as unreliable. It is the one
    decision here resting on a fact nobody has checked.

### Scale and the read model

12. **Design for 10,000, build for 100.** One Postgres, one API instance, R2
    for media. Make the choices that are expensive to reverse; do not build
    machinery that is not needed.

13. **Reserve the paginated shape without paginating.** `cursor` and `hasMore`
    go into the read envelope now, always returning the whole log with
    `hasMore` false.

    This is the sharpest decision in the document. The whole log arrives in one
    unpaginated `GET` — **measured at 392 bytes per photograph**, so 3.7 MiB
    for a 10,000-photograph log on *every* read. That is not a defect: it is
    what lets `logbookProvider.build()` stay synchronous, what makes the
    ETag-inside-the-document cache work, and why there is no `AsyncValue` in
    45 providers and 26 screens. Changing the read's *shape* later moves
    `logbookFormatVersion` and throws away every cached document on every
    device. Reserving the fields costs almost nothing and buys the expensive
    half of that reversal.

14. **The rate limiter goes behind an interface, implementation unchanged.**
    In-process and keyed on `RemoteAddr` is genuinely fine at 100 users on one
    box. The interface means a shared backend drops in without touching call
    sites. **The per-account code cap is not part of this** — it must be in the
    database, because it must not be per-process.

    See `BEFORE-A-PUBLIC-DEPLOY.md` §8: the limiter keys on `RemoteAddr`, which
    is correct for a direct connection and wrong the moment a proxy appears.
    Caddy landing and this interface landing are the same conversation.

### The web

15. **A marketing site: pages, pricing, Stripe checkout, policy, terms.** Days
    of work, no second client to maintain.

16. **Plus a server-rendered share viewer, which is not optional.** Measured:
    `GET /l/{token}` answers `application/json`, so **a shared link opened in
    a browser today shows raw JSON.** Without a viewer the feature is visibly
    broken to every recipient who does not already have the app — which is
    most of them, and which undercuts the only outward surface the product
    has. The envelope already embeds presigned photograph URLs, so the data
    side is done.

    It is also the only organic growth path and part of the abuse surface. It
    is the piece to watch.

### Platform and operations

17. **iOS first.** One store, one review, one set of device checks — and there
    are already 19 untested device checks outstanding for iOS alone. Android
    is a Flutter build away technically and a whole second surface to verify.

18. **Managed Postgres and Cloudflare R2.** Never run your own database as the
    only operator. R2 over S3 specifically because it charges no egress, and
    this app serves photographs on every screen; egress is the bill that
    surprises people. The media layer is S3-compatible already, so R2 is
    configuration. `BEFORE-A-PUBLIC-DEPLOY.md` §4 and §5 — the absent bucket
    lifecycle rules, and restore ordering — apply unchanged and are not
    solved by the move.

19. **A keyed tile provider, with the key issued server-side.** OSM's public
    tiles are ruled out by their own usage policy for a hosted product with
    real users, which `CLAUDE.md` and `BEFORE-A-PUBLIC-DEPLOY.md` §3 both
    already say. MapTiler or Stadia is one provider line, and it removes the
    invert-and-grayscale filter because a real dark basemap arrives.
    **The key must not be baked into the app** — a key in a shipped binary is
    a key anyone can extract and bill you for. That is the actual work.

20. **CI before the first stranger.** `check.sh` and the Go suite on every
    push, merge blocked on red. The suites exist and pass; this is wiring. It
    is the thing that stops a solo operator shipping a broken migration at
    midnight, and there has already been one seven-hour silent hang from a
    step that failed quietly.

21. **Privacy policy, terms and a stated retention period before anyone who is
    not you has an account.** A travel log reveals where someone was and when,
    which is more sensitive than most photo apps carry. Deletion and export
    already exist as mechanisms; what is missing is saying plainly what is
    held, why, for how long, and who else can see it.

---

## What is deleted

- Argon2id and the `passphrase_hash` column
- `POST /v1/auth/register` in its current shape
- A1's passphrase field
- DEC-107's race, along with the code path that had it
- DEC-86's one-traveller rule, replaced by invites
- `make seed`'s any-traveller-exists guard (DEC-97) needs its own answer once
  registration is no longer single-slot

## What is untouched

The media pipeline, per-traveller advisory locks, `logbook_version` and the
ETag, the migration discipline, the thirteen-code error taxonomy, the write
model, and every screen in the client except A1.

---

## Phases

Each needs its own implementation plan. **Do not write them all up front** —
each phase's plan should be written when the one before it has landed, for
the reason the package split was declined during the port: planning a boundary
while it is still moving means guessing, and every wrong guess costs a day.

**Phase 0 — the checkpoint.** Before anything expensive: find ten people who
say they want this. A landing page, or ten conversations. Demand is untested
and this document is built on an aspiration. If the answer is no, everything
below is wasted and the app is still excellent as it is.

**Phase 1 — the ground.** CI, managed Postgres, R2, Caddy and TLS. Nothing
user-visible. Everything after lands on it.

**Phase 2 — identity.** Magic codes, mail delivery, invites, sliding sessions,
the passphrase path deleted, A1 rewritten. The largest client change.

**Phase 3 — lifecycle.** Storage accounting and the cap, account deletion,
the report route, the share-link kill switch, account suspension.

**Phase 4 — the web.** Marketing, pricing, Stripe, policy, terms, and the
share viewer. The viewer could move earlier: it is the only thing that makes
sharing work, and it is independent of phases 2 and 3.

**Phase 5 — the reversible-shape work.** `cursor` and `hasMore` reserved in
the envelope, the rate limiter behind its interface, the keyed tile provider.
Small, and each item is independent.

**Phase 6 — launch.** The 19 outstanding device checks, the nine unticked
checks on `GET /l/{token}`, and the first invite sent to somebody who is not
you.

---

## Not decided

- Whether social ever happens. Deliberately left open.
- Android.
- What replaces `make seed`'s DEC-97 guard.
- Whether the free tier converts, which is unknowable until phase 0 answers.
- **Apple's current rules on external checkout**, which decision 11 rests on
  and which nobody has verified.
