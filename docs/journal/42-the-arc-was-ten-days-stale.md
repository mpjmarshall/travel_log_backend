## REVIEW-2c — the arc had not run in ten days, and it is what found the bypass

`make slice` is this project's strongest evidence tier and **it had not run
since 29 August 2026**. `scripts/slice-arc.sh` was last touched at `97c1700`,
the same day migration 0007 deleted the passphrase column, and it still posted
`{email, passphrase}` to two routes that had stopped taking it.
`grep -c passphrase scripts/slice-arc.sh` answered **8**.

Nothing reported this. `make check` is green without it, the tier is opt-in, and
until `.github/workflows/slice.yml` no machine ran it. The nightly's first
dispatch is what surfaced the whole chain.

### What was wrong, beyond the field name

- **The stack could not boot.** No `MAIL_LOG_SENDER`, so the api restart-looped
  exactly as `test/image` did. Exported in `phase_arc` with the reason, rather
  than defaulted in compose — the default is 0 so that no deployment writes
  sign-in codes to its own log by accident.
- **A5 and A5b asserted registration was CLOSED after the first traveller.**
  0006 ended the one-traveller rule.
- **There was no invite anywhere**, and registration is invite-only.
- **The mailed sign-in code was not exercised at all**, at any tier that
  involved a running container.

### A5 GETS ITS DEC-65 PROOF BACK, HAVING LOST IT FOR A RELEASE

The old step read "the INDEX — not any Go code — is what refuses it", then
DEC-86 closed registration and `Service.Register` began refusing before the
INSERT was attempted — so `travellers_email_lower_key` was not reached through
that route at all. The step's own comment recorded that honestly at the time.
0006 reopened registration, so the uppercased duplicate reaches the functional
unique index again and **the index is what refuses it**. Lowercase that request
and it passes against a plain b-tree on `email`, which is what makes the case
the assertion.

**A fresh invite is spent on it deliberately.** A junk string would answer 409
just as well, because the address is checked before the invite — so it would
prove the ordering rather than the index.

### Two helpers, and one of them is a shape worth keeping

`arc_invite` runs `cmd/invite` against the stack's own database, because that is
how an operator lets somebody in; inserting a row by hand would prove the schema
rather than the path.

`mailed_code` requests a code and reads it out of the container log, which is
where the only `mail.Sender` that exists puts it. Two things in it are
deliberate:

- **It writes to `$WORK/code` rather than stdout.** `fail` calls `exit`, and
  inside a command substitution that kills only the subshell — the arc would
  carry on with an empty answer and blame the next step. This file already
  carries one recorded defect of exactly that family (the brace-expansion one),
  and this is the second.
- **The wait is deterministic, not a retry.** `CodeRequestInterval` is 60
  seconds and a second request inside it is answered **202 with nothing sent**,
  so a caller that simply asked again would poll a log line that is never going
  to arrive. A31 needs a second code, so the helper sleeps the remainder.

### AND A NEW STEP FOUND A LIVE SECURITY DEFECT

A5b2 asserts that an unknown invite answers the same bytes as a spent one — one
sentinel, because telling them apart says which codes exist. It came back **409
instead of 422**, because the address it used had been created by A5b, a step
that had just been refused. That is the invite-gate bypass, and it has its own
entry. **A step written to prove an enumeration property found an authentication
one**, which is the argument for asserting bodies rather than statuses.

Also new: **A6b replays the code that just worked and expects 401.** Nothing
else in this repository can see the difference between a single-use credential
and a password with six digits of entropy.

### The run

```
make slice   exit 0, 324 assertions, five phases green
             record gate arc testdb healthcheck
```

### Commands, not numbers

```bash
grep -c passphrase scripts/slice-arc.sh                       # 0
grep -cE '^\s*assert_(eq|contains) ' scripts/slice-arc.sh
COMPOSE_PROJECT_NAME=travellog-arcfix API_PORT=8097 POSTGRES_PORT=5497 \
  MINIO_PORT=9097 S3_PUBLIC_BASE_URL=http://127.0.0.1:9097 make slice
```
