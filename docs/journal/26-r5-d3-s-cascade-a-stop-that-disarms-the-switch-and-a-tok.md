## R5 — D3's cascade, a stop that disarms the switch, and a token that is a hash

The fifth step of plan-v7. Six routes, of which one is a seven-row cascade and
three are a privacy surface — and two rulings that share a file with them and
nothing else.

**Four commits and one section, which is R1's reading of DEC-23 and for its
reason:** a step this size written as one commit is unreviewable, and a section
per commit would be four sections about one step. What is here was written as
the step ran.

### The six routes, and what each answers

```
DELETE /v1/trips/{id}          200 + THE WHOLE ENVELOPE + ETag
PUT    /v1/trips/{id}/share    200 + a whole Trip + ETag
POST   /v1/trips/{id}/share    201 + a whole Trip + ETag
DELETE /v1/trips/{id}/share    200 + a whole Trip + ETag
PATCH  /v1/me                  200 + {"name": …} + ETag
DELETE /v1/auth/session        204, no body, no ETag
```

**Six rows and not seven, because "revoke them all" is a query parameter.**
`?scope=all` rides on `DELETE /v1/auth/session` rather than claiming a
`DELETE /v1/auth/sessions` the plan's 23-row table does not hold. The precedent
is R6's `?photos=keep|delete`: one destructive act, a parameter choosing how far
it reaches. **Where it differs from that precedent is that this parameter is
OPTIONAL**, and the reason is which way the default is safe — R6's two branches
destroy different amounts and neither is obvious, while here the path is
singular and the default is the smaller act. An **unknown** value is still a 422
naming the field, because `?scope=al` signing one device out while the user
believes every device is out is the one failure this parameter can have.

### D3's cascade is one statement, and the schema is the rest of the sheet

The route runs `DELETE FROM trips WHERE traveller_id = $1 AND id = $2` and
nothing else. Every other row D3 itemises goes because a foreign key says so:

| D3's line | the key |
|---|---|
| "N photos and their notes" | `photos_trip_fk` CASCADE |
| "N recorded walks" | `walks_trip_fk` CASCADE |
| "N pins in …" — **kept** | nothing. `places` has no `trip_id`; `visits_trip_fk` takes the visits |
| "The shared link stops working" | `share_links_trip_fk` CASCADE |
| the itinerary | `trip_cities_trip_fk` CASCADE |
| the cities | nothing from `trips` reaches `cities` at all |
| another trip's photograph | `photos_visit_fk ON DELETE SET NULL (visit_id)` |

Writing any of them in Go would be a second implementation of the sheet, and
**the easy one to write is the wrong one.**

### The numbers, re-derived here rather than copied from the lens

Seven legs in `internal/seed/cascade_test.go`, at fixture scale, against the
client's own log. Deleting `autumn-crossing`, counted before and after:

```
photos 284 -> 188      walks   2 -> 1       visits 49 -> 44
trips    7 -> 6        cities 12 -> 12      places 17 -> 17
trip_cities 18 -> 13   share_links 1 -> 0   media_objects unchanged
```

`gamcheon` — the fixture's **one** place whose every visit was on that trip —
survives in Busan with zero visits, which is a wishlist place and is what the
sheet promised. Through the running container it comes back in the answer as
`{"id":"gamcheon","cityId":"busan","name":"Gamcheon","visits":[]}`.

### THE MUTATION THE PLAN NAMED DID NOT REDDEN, AND THE ORDER IS WHY

`DELETE FROM places WHERE id IN (SELECT place_id FROM visits WHERE trip_id=$1)`
— the CRUD reflex, named in v6 as reachable and never run — placed **after**
the trip delete is a **no-op**: the visits have already cascaded, the subquery
matches nothing, every leg stays green. Placed where a CRUD implementation
actually writes it, children before the parent, it takes five places and
reddens three legs:

```
places 17 -> 12                      (the pin count)
gamcheon is gone                     (the wishlist pin, by name)
16 surviving photographs still filed (want 64)
```

**The dangling-reference query answers 0 throughout both orderings.** That is
the whole reason the filing count sits beside it: zero has to be zero for the
right reason, and unfiling every photograph in the log satisfies a dangling
check perfectly. 95 photographs named a place and an occasion before the
cascade, 64 do after, and the 31 that left are the deleted trip's own.

**A mutation that does not change behaviour is a green suite proving nothing —
and one ORDERING of a mutation can be that while the other is not.**

### A second mutation survived, and the leg that catches it is new

Deleting the revoke from inside `NewShareLink` left **every** leg green.
Stop-then-new goes through a `StopSharing` that has already revoked, so the
mint's own revoke is never exercised by the obvious sequence. H1 offers 'New
link' whether or not one is live, so
`TestNewLinkOnATripThatIsAlreadySharedKillsTheOldOne` is the sequence the
client actually has — and it reddens on the INSERT raising against
`share_links_one_live`, which is why it asserts the 201 and the row counts
rather than the absence of an error.

### THE SHARE-RESET MUTATION THE PLAN NAMES IS GREEN, AS DB-MAJ-4 PREDICTED

The plan's test strategy says "a DEFAULT does not reach an UPDATE" and names
`SET col = DEFAULT` as a mutation that reddens on two of three flags. **Run at
this working tree it reddens nothing.** `UPDATE … SET share_photos = DEFAULT`
is legal SQL, it reaches the column default, and after migration 0002 that
default is exactly `true` — the correct answer. The sentence is true only in a
narrower form: **an UPDATE that does not NAME a column does not reach its
default.**

DB-MAJ-4's replacement is the one that works — implement the stop as a bare
UPDATE touching only `share_links` — and it reddens on all three flags.

**The literals are still right, for a different reason than the plan's.** The
three values are the CLIENT's — `Trip.defaultSharePhotos`, `defaultShareNotes`,
`defaultShareCoordinates` — and they agree with the column defaults only
because 0002 made them agree. Leaning on the default would let a future
migration silently redefine what "stop sharing" means.

### AND THE PLAN'S OWN NAMED FAILING TEST CANNOT SEE ITS OWN MUTATION

`TestStoppingSharingDisarmsTheSwitchesForTheNextLink` is a HANDLER leg over a
fake store, and the mutation it is written against is in the STORE. Measured:
with the reset deleted from `ShareStore.StopSharing`, `go test
./internal/httpapi/` is **ok** and `go test ./internal/postgres/` is red. The
plan's leg is carried and is worth having — it is the sequence end to end — but
the guard on the privacy claim is
`TestStoppingSharingResetsTheThreeFlagsToTheClientsDefaults`, against a real
database. **A leg over a twin cannot guard a statement the twin does not
execute.**

### DEC-100: a 304 is four round trips, and the counting is the measurement

Measured on the running container with `log_statement='all'`, between two
marker queries, one conditional GET:

```
SELECT s.id, s.traveller_id, s.token_hash, …          the session lookup
begin isolation level repeatable read read only
SELECT logbook_version FROM travellers WHERE id = $1::uuid
rollback
                                              -> 4 statements, 0 advisory locks
```

The performance lens measured **nine** and named the five that stamp the
timestamp — `begin`, `pg_advisory_xact_lock`, `SELECT 1 FROM travellers`, the
`UPDATE`, `commit`. Nine minus five is four, which is what the log says.

**And it is not "never".** With `last_used_at` pushed ten minutes back, the same
request costs **five** — one extra UPDATE, still no transaction, still no
advisory lock, still no existence read.

**The parameterised statements do not appear under `statement:`.** pgx uses the
extended protocol, so they log as `execute <unnamed>:` — a `grep 'statement:'`
counts **two** of these four and both of them are the transaction's bookends.
An instrument that under-reports by half is an instrument that would have made
this look better than it is.

### The granularity decision is in Go and not in the UPDATE, and that is not the obvious place

`UPDATE … WHERE last_used_at < $4` is one fewer branch and it destroys
something: `TouchSession` answers `ErrNoSession` when its UPDATE matches
nothing, which is how a session deleted between the lookup and the write is
noticed — and under that predicate a **fresh** session matches nothing too. The
two states become one answer and the one that is a 401 wins. So the decision is
taken in `Service.Authenticate` with `Session.LastUsedAt` in hand, which the
lookup was already scanning.

### A leg had carried the wrong name since VS6

`TestCreateSessionAndTouchSessionBothTakeTheTravellerLock` **never touched a
session.** TouchSession was in its name and nowhere in its body, so it stayed
green when DEC-100 took the lock off — the rule leg in `tx_sweep_test.go` is
what went red. It is rewritten to assert both directions against one held lock.
That is defect class 9 landing on a leg nobody had re-read.

### DEC-86: where the rule lives, and what it deliberately leaves open

Registration closes after the first traveller, **in `Service.Register` and not
in `createTravellerSQL`**. Both placements work and each costs something:

- `WHERE NOT EXISTS (SELECT 1 FROM travellers)` in the INSERT makes the
  DATABASE enforce it — and makes every second `CreateTraveller` answer "no
  row", which is the same answer a duplicate address gives. DEC-65's
  `lower(email)` unique index would then be exercised by **nothing at all**:
  `TestASecondRegistrationOfOneAddressInAnotherCasingIsRefused` is the only
  thing that reaches it, and it reaches it by calling the store twice.
- In the service it answers **before Argon2**, which is 64 MiB a call. A closed
  instance that hashes every attempt is an unauthenticated memory sink behind a
  route nobody can succeed on. `TestAClosedRegistrationRefusesBeforeItHashesAnything`
  counts calls rather than timing them.

**What that leaves open, stated rather than silent: check-then-insert is not
atomic.** Two registrations whose statements overlap can both find an empty
table — under READ COMMITTED each takes its snapshot at statement start, so the
second does not see the first's uncommitted row — and **putting the predicate
inside the INSERT would not close it either**. Closing it needs a transaction
with an advisory lock (which is DEC-50's one named exception giving up its
exception, and `TestRegisterTakesNeitherHelperAndOpensNoTransaction` says in
terms that this "needs a design decision and not an allowlist entry") or a
unique index on a constant expression (a fifth migration, against a plan whose
count of four is derived on DEC-85). The window is between the owner's first
registration and a stranger's, on a fresh instance, and the loser is refused.

**The oracle SHRINKS.** `ErrEmailTaken` told a caller that THAT ADDRESS is
registered here; `ErrRegistrationClosed` tells them the instance is in use,
which the sign-in page already tells them. They share a branch in
`writeAuthFailure` so the two are **byte-identical** — asserted by comparing the
bodies, in Go and in the arc.

**Three legs were replaced rather than extended, and each for the same reason:**
their names promised a behaviour that has stopped being the reason.
`TestRegisteringAnAddressTwiceInAnotherCasingIs409` would now pass against a
build that still handed a stranger an account;
`TestBothAuthRoutesTakePOSTAndNothingElse` refused a verb that is now served;
and the arc's A5 said "the INDEX — not any Go code — is what refuses it", which
is no longer true of that request.

**And R4 had already written the sentence.** `cmd/seed/main.go:414` prints
"Registration is CLOSED behind this account (DEC-86)" — R4 anticipated the
ruling in the one place where it decides what a developer does next.

### DEC-85: the token is a hash, and the down file refuses

Migration 0004 replaces `share_links.token text` with `token_hash bytea`, moves
the primary key and `share_links_token_key` onto the digest, and drops the
plaintext column — which takes `share_links_token_present_ck` with it.

**The backfill is the statement that matters**, not the ALTER: a row written
under 0003 holds a live capability and has to come out holding that
capability's digest, or every link ever issued stops resolving.
`sha256(convert_to(token,'UTF8'))` is the one spelling that produces the bytes
Go hashes, and the leg computes its expectation by CALLING
`logbook.HashShareToken` rather than restating a hex literal.

**`HashShareToken` is a second function and not a reuse of `auth.HashToken`.**
auth's base64-decodes and refuses anything that is not 32 raw bytes, because a
session token is minted by this server. A share token is minted by the CLIENT —
twelve characters of `abcdefghjkmnpqrstuvwxyz23456789` — so decoding it first
resolves a different row and refuses most real tokens outright. The leg asserts
the negative as well as the positive, which is the only way that mistake is
visible.

**The down file REFUSES while `share_links` holds a row**, and it is the only
one here that does. sha256 is one-way: restoring `token` as nullable leaves half
a primary key, and deleting every row destroys DEC-67's history silently inside
a file called "down". On an empty table it is an exact inverse, which is where a
developer rolling back a migration they have just applied actually stands.

**Its reason moved out of `USING HINT` and into the MESSAGE, after a leg
measured it.** psql prints a HINT and `database/sql` does not: the driver's
error string carries the MESSAGE alone, so the first draft's refusal reached Go
as a bare count. A refusal whose reason is invisible to half its readers is a
refusal somebody deletes.

### DEC-67's own premise, re-derived rather than carried

DEC-67 chose `PRIMARY KEY (traveller_id, token)` for two reasons. The first
stands: with the natural key, 'Stop sharing' then 'New link' fails outright. The
second was *"history is kept, which matters because DEC-10 stores the token in
plaintext"* — **and 0004 makes that sentence false.** The record no longer shows
which token was live; it shows which digest was. The key stays, and the
re-derived reason is in 0004's own comment.

**SAF-MIN-9 is accepted in writing beside it.** `share_links_trip_fk` is
CASCADE, so D3 destroys a trip's whole revocation history — the safety lens
executed it (three rows for `autumn-crossing`, `share_link_rows_left = 0`). It
is accepted: the trip is gone, so "which token was live on a trip that no longer
exists" is not a question anyone asks, and D3's sheet could not reasonably
itemise a server-side artefact the client's model never held. It is in 0004
rather than 0001 because 0001 cannot be edited, and **the point of putting it
there is that the next reader finds DEC-67's argument and its correction
together rather than in contradiction.**

### SAF-MAJ-7's confirmation gate is DECLINED, and the reason is in the code

The reason lives at the top of `internal/httpapi/trip_handlers.go` rather than
in a lens report, because that is where somebody about to add it will be. In
short: the sheet's gate is a gate on the HUMAN and its value is the pause before
the typing; a body field is a gate on the CLIENT, which is the software that
already drew the sheet. It would make the API's guard and the sheet's guard two
copies of one string that can drift — rename on one device and the other
device's cached name no longer arms the delete, a failure the sheet does not
have. And DEC-86, in this same step, closes the half of the threat the lens
itself called compounding.

**Trigger for revisiting: a second traveller, or any caller of this route that
is not the sheet.**

### An acceptance check that cannot pass against correct work

R5's own block says:

```bash
psql "$TEST_DATABASE_URL" -c "\d share_links" | grep -c 'token_hash'   # -> 1
```

**It answers 4.** `\d` prints the column, the primary key, the unique index and
the CHECK constraint, and every one of those mentions `token_hash` — correctly.
The narrowed form is the one that means what the plan meant:

```bash
psql … -tAc "SELECT count(*) FROM information_schema.columns
             WHERE table_name='share_links' AND column_name='token_hash'"   # -> 1
```

Both were run and both are reported. This is the plan's own rule 10 landing on
the plan.

**And the falsifiable half of DEC-85's claim is not that grep at all.** A
migration that ADDED `token_hash` beside `token` satisfies both forms.
`TestThePlaintextShareTokenColumnIsGone` is what says `token` is not a column of
this table, and `TestAMintedTokenIsNowhereInTheClear` renders the whole row as
text so that a `token` somebody adds back under any name is caught by the same
assertion.

### What R5 leaves guarded by nothing

- **The residual registration race.** Two overlapping first registrations can
  both succeed. Nothing tests it, because a test for it would be a test of
  PostgreSQL's snapshot isolation rather than of this code, and the two ways to
  close it are named above with what each costs.
- ~~**D3's "pins are kept" row, IN THE CONTAINER.**~~ **CLOSED at R6**, which
  is where this entry said it would be. A31-A33 create the city, the pin and
  its two occasions through `PUT /v1/cities/{id}` and `PUT /v1/places/{id}`,
  and A29 now asserts that a pin whose every occasion was on the deleted trip
  survives the cascade with `visits: []` — the `gamcheon` shape, against the
  deployed image. The row COUNTS are still `go test ./internal/seed/`'s, at
  fixture scale.
- **`docs/PUBLIC-ENVELOPE.md`.** It is a specification and nothing executes it
  until R8. Every fixture number in it was run, and the key sets are a claim no
  test can check until there is a handler to check them against. Same tier as
  the iOS manifest flags in the client: guarded by somebody reading it.
- **`TouchInterval`'s VALUE.** Five minutes is asserted through the constant, so
  the legs are self-consistent by construction and the number is defended by
  nothing. Setting it to 30 days is a green suite and a `last_used_at` that says
  every live session was last used at sign-in. It is the same hole
  `migrateLockTimeout` and `IdleInTransactionTimeout` already have, and it is
  now four constants wide.
- **`MinShareTokenBytes`'s VALUE.** Twelve is derived from the client's own
  generator in the comment; nothing measures it against the client. Setting it
  to 4 reddens one leg — the expression string — and that leg would be updated
  by whoever lowered it.
- **The `?scope=all` route reaching a REAL second device.** The leg holds two
  tokens for one traveller, which is what a phone and a tablet are from the
  server's side, and the arc holds one. Nobody has watched a second device
  discover it is signed out.

### Commands, not numbers

```bash
# the legs, and what the database variable buys, at this commit
                       go test ./... -count=1 -v | grep -c -- '--- PASS'   # 618
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'    # 814

# the counts this step moved, each re-derived rather than incremented
grep -c 'http.Method' internal/httpapi/routes.go                  # 13 routes
ls migrations/*.up.sql | wc -l                                    # 4 migrations
grep -cE '^\tCode[A-Za-z]+ +Code = ' internal/httpx/errors.go      # 13 codes
psql "$DSN" -tAc "SELECT count(*) FROM pg_constraint
                   WHERE contype='f' AND connamespace='public'::regnamespace"   # 26
psql "$DSN" -tAc "SELECT count(*) FROM information_schema.tables
                   WHERE table_schema='public' AND table_type='BASE TABLE'"     # 12

# the arc, under its own project, so a live volume is untouched
COMPOSE_PROJECT_NAME=travellog-r5 API_PORT=8095 POSTGRES_PORT=5474 \
  MINIO_PORT=9015 S3_PUBLIC_BASE_URL=http://127.0.0.1:9015 make slice

# the seed, on a SECOND stack, because the arc ends holding a traveller
COMPOSE_PROJECT_NAME=travellog-r5-seed API_PORT=8096 POSTGRES_PORT=5475 \
  MINIO_PORT=9016 S3_PUBLIC_BASE_URL=http://127.0.0.1:9016 make seed

# D3's cascade, end to end, against the seeded log
curl -sS -X DELETE -H "Authorization: Bearer $TOKEN" "$BASE/v1/trips/autumn-crossing" \
  | jq '.logbook.places[] | select(.id=="gamcheon")'

# zero for the RIGHT reason: the dangling check AND the filing count
psql "$DSN" -c "SELECT count(*) FROM photos p
                LEFT JOIN visits v ON (p.traveller_id,p.visit_id)=(v.traveller_id,v.id)
                WHERE p.visit_id IS NOT NULL AND v.id IS NULL"          # 0
psql "$DSN" -c "SELECT count(*) FROM photos WHERE place_id IS NOT NULL" # 64, was 95

# how many round trips a 304 costs (pg_stat_statements is not preloaded here)
psql "$DSN" -c "ALTER DATABASE travellog SET log_statement='all'"
docker compose … restart api
# … one conditional GET between two marker queries, then:
docker compose … logs postgres | grep -E 'LOG: +(statement|execute)'
#   -> 4 statements, 0 pg_advisory_xact_lock. A grep for 'statement:' alone
#      counts 2 of the 4: pgx uses the extended protocol.
```

