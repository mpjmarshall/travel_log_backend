## SEC-1 — the invite gate did not hold, and the arc is what found it

Registration is invite-only. It was not. **Anyone who could reach the API could
make a working account with an invite they invented**, and the server told them
no while doing it.

Found while repairing `scripts/slice-arc.sh`, by a step written to prove
something else: A5b2 asserts that an unknown invite answers the same bytes as a
spent one, and it came back **409 conflict** instead of 422. The address it used
had been created by A5b — a step that had just been refused.

### THE BYPASS, RUN END TO END AGAINST A RUNNING CONTAINER

```
1. POST /v1/auth/register {"email":"…","invite":"NOTAREALINVITE01"}
   HTTP 422  {"code":"invalid_field","field":"invite"}
2. SELECT email FROM travellers WHERE email='…'
   nobody-ever-invited-me@example.com          <- the account exists
3. POST /v1/auth/code {"email":"…"}            HTTP 202, code 364285 mailed
4. POST /v1/auth/session {"email":"…","code":"364285"}
   HTTP 201  {"token":"fiQgTcE7iEFDJ7jbvCFj6YEPjhRa_8ILbv0QtSQe608", …}
5. GET /v1/logbook  with that token            HTTP 200
```

Five requests, no invite, a live session and a 600/min traveller budget behind
it. The 422 at step 1 is what makes it invisible: the caller is told the invite
is bad, and it is, and the account is made anyway.

### THE CAUSE IS TWO STATEMENTS IN THE WRONG ORDER, WITH NOTHING AROUND THEM

```go
tr, err := s.Register(ctx, email)                       // INSERT, committed
if err != nil { return Traveller{}, err }
if err := s.Store.ClaimInvite(ctx, HashInvite(invite), tr.ID); err != nil {
    return Traveller{}, err                             // the row stands
}
```

The doc comment says why the order was chosen: *"The claim comes after the
create, because the claim records who spent it."* That is a real constraint —
`ClaimInvite` wrote `used_by`, which needs an id that does not exist until the
INSERT has run — and it is the whole of the defect. There is no transaction, so
returning an error does not undo the INSERT.

### THE FIX NEEDS NO TRANSACTION, AND 0006'S OWN COMMENT IS WHY

`invite_codes` carries `used_at` **and** `used_by`, and 0006 already records
that they are not one fact:

> `used_at` ALONE IS THE SPENT MARKER, and there is deliberately no check
> pairing it with `used_by`. Pairing them makes account deletion fail: the
> foreign key nulls `used_by` and the check would then reject a row whose
> `used_at` is still set.

So the gate does not need the traveller id and never did. One method becomes
two:

- **`SpendInvite(ctx, hash)`** sets `used_at` and runs **before** the account is
  made. `WHERE used_at IS NULL` still makes it the single-use gate, and the
  racing-claims leg still admits exactly one of two.
- **`RecordInviteUser(ctx, hash, travellerID)`** sets `used_by` afterwards. It
  is provenance, and the schema already tolerates its absence.

**The behaviour change is stated rather than hidden: a good invite against an
address already registered is now SPENT, and the operator mints another.** That
is a credential failing closed, which is the direction to fail in — the
alternative is what shipped.

### The legs, red before the code

```
--- FAIL: TestAnInviteThatCannotBeSpentCreatesNoTraveller/an_invite_that_never_existed
    after a refused registration TravellerByEmail = <nil>, want ErrNoTraveller.
        The account exists. Registration answered 422 and created it
        anyway, so the invite gate can be walked straight past.
--- FAIL: TestAnInviteThatCannotBeSpentCreatesNoTraveller/an_invite_already_spent
--- FAIL: TestAnInviteIsSpentEvenWhenTheAddressIsTaken
    SECOND after the refused registration = <nil>, want ErrInviteSpent
```

Four legs at two tiers: two in `internal/auth` against the twin, two in
`internal/postgres` driving the real `auth.Service` against a real database.
`TestARefusedInviteLeavesNoTravellerRow` counts rows, which is the assertion
nothing in the suite was making.

### WHY EVERY EXISTING LEG WAS BLIND, AND IT IS ONE SENTENCE

`TestRegisterWithInviteRefusesASpentOne` registers `first@` with `ONCE`, then
`second@` with `ONCE`, and asserts the second answers `ErrInviteSpent`. **It
does.** What it never asks is whether `second@example.com` now exists — and it
did. Every invite leg in the tree asserted the ERROR and none asserted the
STATE, so the defect sat behind eight passing tests.

Same shape as this project's own recorded lessons: an assertion scoped to what
the mutation nulls cannot see it; a dangling check is not a filing check. Here
it is *a refusal is not an absence.*

### Verified closed, on a rebuilt container

```
POST /v1/auth/register  bad invite   422  {"code":"invalid_field","field":"invite"}
SELECT count(*) FROM travellers …    0
POST /v1/auth/code                   202, and NOTHING is mailed
POST /v1/auth/session                401  {"code":"unauthenticated"}
```

The 202 at step 3 is correct and stays: whether an address is registered is not
a thing this route may reveal.

### What this leaves guarded by nothing

- **The window between the two writes.** `SpendInvite` commits, then
  `CreateTraveller` runs. A process killed between them burns an invite and
  makes no account. That is the safe direction and it is not atomic; closing it
  needs a transaction in `auth_store.go`, which is a change to the rule that
  register opens none.
- **`RecordInviteUser` failing.** The account exists, the invite is spent, and
  only the provenance is missing. Nothing reports it.

### Commands, not numbers

```bash
go test ./internal/auth/ ./internal/postgres/ -run Invite -count=1 -v
grep -rn 'ClaimInvite' --include='*.go' .        # nothing
```
