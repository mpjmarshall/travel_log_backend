## DEPLOY-1 — one flag was doing two jobs, and only one of them could be off

The step that makes this deployable for a single operator. **It is a split, a
Makefile fix and a runbook**, and the split is the only one of the three that
was blocking.

**1146 legs, from 1142.** `make check` exit 0, `slice-arc.sh record` green.

### THE COUPLING, AND WHY IT WAS SOUND WHEN IT WAS WRITTEN

`DEVELOPMENT` decided two unrelated things: whether `mail.NewLogSender` may be
used, and — at `internal/admin/login.go` — whether the panel's session cookie
carries `Secure`. `docs/ADMIN-PANEL.md` recorded the reuse deliberately, as
*"the same seam the log mail sender already uses"*, and at the time that was a
reasonable trade: both meant "this is a development stack".

**What made it a defect is that mail never got an adapter.** `internal/mail`
has no `Sender` that delivers, so `MAIL_LOG_SENDER` — `DEVELOPMENT` as was — is
the ONLY way this build boots. A switch that meant "development" became a
switch that means "running at all", and it was still dropping `Secure` from the
admin cookie on its way past. **The only bootable configuration was one whose
panel session travelled in the clear.**

Nobody chose that. It is what a shared flag does when one of its two meanings
moves and the other does not.

### The split

| variable | what it weakens | default |
|---|---|---|
| `MAIL_LOG_SENDER` | sign-in codes go to the container log | `0` |
| `ADMIN_COOKIE_INSECURE` | the panel's cookie loses `Secure` | `0` |

Each names its own job, neither implies the other, and `DEVELOPMENT` is gone
rather than kept as a third thing that means both.

### THE TWO WIRING LEGS COULD NOT BE RED FIRST, AND THE MUTATION IS WHY THEY MEAN ANYTHING

Deleting `Config.Development` broke both call sites, so the compiler forced the
rewiring before the legs that assert it could compile. They are reported as
mutation-proven and not as test-first:

- `Dev: cfg.MailLogSender` — the old coupling wearing the new name — reddens
  `TestPermittingTheLogSenderLeavesTheAdminCookieSecure` and nothing else.
- `Dev: false` reddens `TestTheCookieSwitchIsWhatRelaxesTheCookie` and nothing
  else.

Each restored by file copy, verified byte-identical. **Which variable feeds
which is invisible from outside the process** — VS8-SEC's M9 recorded that for
the two rate limiters, and it is the same shape here.

The two `internal/config` legs WERE red first, for the honest reason: the
fields did not exist.

### `make invite` BUILT A DSN IT HAD MADE UP, AND THE RUNBOOK IS WHAT FOUND IT

The recipe read `postgres://$user:$user@...` — **the username as the
password.** It works only while `POSTGRES_PASSWORD` equals `POSTGRES_USER`,
which is true of the shipped defaults and false the moment an operator sets a
real one. Setting one is step one of `deploy/RUNBOOK.md`, so the first command
the runbook gives you would have failed on the first deploy that took its
advice.

`make seed` and `make test-db` both derive the password from the running
container; `invite` alone did not. **This is VS1-FIXES finding 4 recurring** —
that finding was the same defect in `make test-db`, and its lesson was that a
recipe restating a value prints the wrong one the first time somebody overrides
it.

Proven against a stack running a non-default password: the shipped recipe exits
1, the fixed one mints an invite.

### Verified in the container, not only in `go test`

A throwaway stack, built from this tree, with a **non-default** database
password:

```
no MAIL_LOG_SENDER          api unhealthy, "no mail provider is configured and
                            MAIL_LOG_SENDER is not set"
MAIL_LOG_SENDER=1           three healthy services, /healthz 200
make invite                 27F7FES6D5E4Y5J5
register / request code     201 / 202
the runbook's log command    "Your sign-in code is 599149"
sign in, then GET /v1/logbook 200

ADMIN_COOKIE_INSECURE=0     Path=/admin; HttpOnly; Secure; SameSite=Strict
ADMIN_COOKIE_INSECURE=1     Path=/admin; HttpOnly; SameSite=Strict
```

**The first cookie line is the whole step**: `Secure` is on while the log
sender is running, which no configuration could produce before.

### What this deploy still does not have, and none of it is code

`deploy/RUNBOOK.md` carries these where an operator is standing rather than
only here: no TLS (Caddy is its own slice), no mail adapter, a limiter that
keys on `RemoteAddr` and therefore does not bind behind a proxy, no scheduled
backup, and no bucket backup at all — a database restore without one gives a
log whose every photograph reference resolves and points at nothing.

### What this leaves guarded by nothing

- **`ADMIN_COOKIE_INSECURE` and `MAIL_LOG_SENDER` join the twenty-five.** The
  coverage legs assert both are PRESENT in compose and documented in the
  template; nothing asserts either is `0` there. Shipping `ADMIN_COOKIE_INSECURE=1`
  as the compose default would be a green suite and a panel cookie in the clear.
- **`make invite`'s fix has no standing leg.** It was proven by running it
  against a non-default password and by the mutation, both by hand. The arc's
  `testdb` phase is the shape that would hold it — it already asserts a derived
  URL connects — and this one is not in it.
- **The runbook itself.** Every command in it was run against a real stack at
  this commit, and nothing re-runs them. It is the same tier as
  `docs/CLIENT-PREREQUISITES.md`: guarded by somebody following it.

### Commands, not numbers

```bash
make check
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'   # 1146
grep -rn 'DEVELOPMENT' --include='*.go' --include='*.yml' . | grep -v '/.git/'  # prose only
grep -n 'pass=' Makefile          # seed, test-db, test-s3 AND invite
```

