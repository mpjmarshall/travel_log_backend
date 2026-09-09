## R6 — cities and places, and an absent key that means leave them alone

The sixth step of plan-v7. Three routes, of which one answers two different
shapes and one is D2's two branches — and the one field in this API where an
EMPTY value and an ABSENT value are different requests with different blast
radii.

**Six commits and one section**, which is R1's reading of DEC-23 and for its
reason. What is here was written as the step ran.

### The three routes, and what each answers

```
PUT    /v1/cities/{id}                     200 + a City        + ETag
PUT    /v1/cities/{id}  (with attachTo)    200 + THE ENVELOPE  + ETag
PUT    /v1/places/{id}                     200 + a Place       + ETag
DELETE /v1/places/{id}?photos=keep|delete  200 + THE ENVELOPE  + ETag
```

**Sixteen rows in the table now**, and three and not four: `?photos` rides on
the removal rather than claiming a second path, which is R5's `?scope=all`
precedent. Where it differs from that precedent is that this parameter is
**REQUIRED**, and R5 wrote down why in advance — there the path is singular and
the default is the smaller act; here the two branches destroy different amounts
and neither is obviously smaller from the caller's side.

**There is deliberately no `DELETE /v1/cities/{id}`.** The client has no
delete-a-city control, so no sheet copy authorises the cascade, and
`places_city_fk`, `photos_city_fk` and `walks_city_fk` are RESTRICT (DEC-57).
Whoever adds the control is stopped by the database at exactly the moment they
should be writing the sentence.

### THE VISITS CONTRACT, MEASURED IN FOUR STATES

The whole step turns on this. `visits` is the only ordered child collection in
this schema that something else references —
`photos_visit_fk … ON DELETE SET NULL (visit_id)` — so what a write does to it
decides whether thirty photographs keep the occasion they were taken on.
Measured against the client's own log at `fushimi-inari`, which holds **28
occasions and 30 photographs spanning 3 trips**:

| the body carries | occasions after | filings after | whole-log filings |
|---|---|---|---|
| `visits` **omitted** | 28 | 30 | 95 |
| the same 28, **re-sent unchanged** | 28 | 30 | 95 |
| the same 28, **reversed** | 28 | 30 | 95 |
| `visits: []` | **422**, 28 | **422**, 30 | 95 |

And what each of those does under the two implementations this step rejected:

| | delete-then-insert | the empty array, with PD-06's fix in place |
|---|---|---|
| occasions | 28 → **28** | 28 → **0** |
| photographs still filed there | 30 → **0** | 30 → **0** |
| naming a place with no occasion | 0 → **30** | 0 → **30** |
| the dangling-reference check | 0 → **0** | 0 → **0** |
| the only trace | `DELETE 28 / INSERT 0 28` | `UPDATE 28 / DELETE 28` |

**The dangling check answering 0 in both columns is the point.** The reference
is GONE rather than dangling, so R5's `expectNoDanglingReferences` cannot see
it, the place-without-occasion query sees no place, and a pair-agreement check
sees two NULLs that agree. What sees it is DEC-89's count that must not fall:
`SELECT count(*) FROM photos WHERE place_id IS NOT NULL`.

### THE OFFSET IS DERIVED, AND THE PLAN'S `+ 1000` IS A CONSTANT

R6's step text mandates `UPDATE visits SET ordinal = ordinal + 1000`, and it is
correct for every place holding fewer than a thousand occasions. Above it the
statement collides with itself: park {0..1100} at {1000..2100} and the row
moving 0 → 1000 meets a row still sitting at 1000, because
`visits_place_ordinal_uq` is checked per **ROW** during a statement.

What ships is `GREATEST($3::int, (SELECT max(ordinal) + 1 …))`, which has no
number in it, and both halves earn their place: `max(ordinal)+1` puts the
parked set entirely above the stored set so no per-row collision is possible in
any order, and the incoming length puts it entirely above the ordinals the
INSERT is about to write. Measured on this project's own postgres:17.11:

```
ordinal - 1000   ERROR: new row for relation "visits" violates check
                 constraint "visits_ordinal_ck"
1 - ordinal      ERROR: duplicate key value violates unique constraint
                 "visits_place_ordinal_uq"
ordinal + 28     OK, and all 30 photographs are still filed
```

**The subquery reads the table it is updating and that is safe**, which is
worth stating because it looks removable. It is uncorrelated, so the planner
makes it an InitPlan; and even re-evaluated it would answer the same, because a
statement's own changes are invisible to its own subqueries — the rows this
UPDATE writes carry its command id and are filtered out.

### THE PLAN'S OWN NAMED FAILING TEST CANNOT SEE ITS OWN MUTATION

This is the sharpest thing in the step, and it is the second time this project
has found it — R5 found the first, on a leg over a twin.

R6's test strategy writes out `TestRemovingAPlaceAndItsPhotographsActually
RemovesThem` in full and names the mutation it is for: reorder the delete
branch to drop the place first. Its only assertion about the photographs is

```go
if after := countPhotos(t, db, place); after != 0 { … }
```

**Run against that exact mutation, transcribed literally, it is GREEN.** With
the place deleted first, `photos_place_fk … ON DELETE SET NULL (place_id)`
clears `place_id` on every one of them — so `WHERE place_id = 'fushimi-inari'`
counts **0**, the assertion is satisfied, and both photographs are still there.
Measured:

```
### CORRECT CODE:      photographs left in the whole log: 0   --- PASS
### UNDER THE MUTATION: photographs left in the whole log: 2   --- PASS
```

What ships asserts the **whole-log** count beside it — `SELECT count(*) FROM
photos` — which can only be satisfied by the photographs actually being gone,
and the fixture-scale leg asserts `photos 284 → 254`. The general form is worth
more than the fix: **an assertion scoped by the column the mutation nulls
cannot see that mutation.**

### D2's TWO BRANCHES, ROW BY ROW, AT FIXTURE SCALE

Re-derived at this working tree by counting before and after, against the
client's own log:

| | before | `?photos=delete` | `?photos=keep` |
|---|---|---|---|
| photographs | 284 | **254** | 284 |
| places | 17 | 16 | 16 |
| occasions | 49 | 21 | 21 |
| walks | 2 | **2** | **2** |
| cities | 12 | 12 | 12 |
| naming a place | 95 | **65** | **65** |
| naming an occasion | 95 | 65 | 65 |
| naming a place with **no** occasion | 0 | 0 | 0 |
| captions | 2 | **1** | **2** |

**The caption row is the whole of the difference between the branches.** The
sheet names "the notes you wrote on them" on the destructive branch only, so
one caption goes with the thirty photographs and the other survives both ways.

**THE KEEP BRANCH IS NO GO AT ALL.** `DELETE FROM places` is the statement, and
everything the sheet promises is a foreign key: `visits_place_fk` CASCADE takes
the occasions, `photos_place_fk … ON DELETE SET NULL (place_id)` takes the pin,
and the visits cascading takes `visit_id` with them through `photos_visit_fk`.
That is exactly `Photo.copyWith(clearPlace: true)` — **both** columns — and the
date, the city and the caption are untouched because nothing touches them.

**THE DELETE BRANCH IS TWO STATEMENTS AND THEIR ORDER**, and it is the only
place in this step where Go decides anything the schema could have. The
photographs go first, or the FK above clears their `place_id` and the DELETE
matches nothing.

**AND THE WALKS ARE NOT TOUCHED ON EITHER BRANCH**, because `walks` has no
`place_id` at all. The absence IS D2's "the track stays with the day it was
recorded either way", and the leg asserts it on both branches rather than one.

### A REFUSAL THE PLAN DOES NOT NAME, ARGUED RATHER THAN ASSUMED

The mandated shape ends "DELETE only the ids absent from the incoming array".
A visit deleted that way takes `photos.visit_id` with it and leaves
`photos.place_id` standing — **the half-filed state the client's model has
never expressed**. Measured across all 284 fixture photographs: 95 carry both,
189 carry neither, place-only **0**, visit-only **0**.

That is SAF-MAJ-4's hazard at row granularity, and `visits: []` is simply its
n-row case. So a visits array that drops an occasion **still holding
photographs** is refused with a 422 naming the field, and an occasion with none
may go freely. Both refusals stand for their own reason: the empty array is
refused even when nothing is filed there, because clearing a place's whole
history is a destruction no sheet authorises.

**TRIGGER FOR REVISITING: a control that removes one occasion**, at which point
the sheet copy is written first and this follows it — which is the order every
other cascade in this app was decided in.

### `visits: []` IS THE RULING'S LETTER AND IT COSTS SOMETHING MEASURED

DEC-89 says: "`visits: []` = an explicit request to clear, which no client
control issues, so it is REFUSED with a 422 naming the field until one exists."
That is implemented exactly. **What it costs, measured against the seeded log
through the running container:**

```
17 places; re-send each one BYTE FOR BYTE as GET /v1/logbook emitted it
  -> 8 answer 200
  -> 9 answer 422 {"code":"invalid_field","field":"visits"}
```

**Nine of the seventeen places in the client's own log are wishlist places**,
so the document emits `"visits": []` for them — `EmitPlace` normalises the nil
slice — and sending that same document back is refused. `Place.toJson()` in the
client writes `'visits': instance.visits.map(...).toList()`, which is `[]` for
a new pin, so **C1's pin serialised as a whole entity is refused too.**

This is not a defect in the ruling as applied; it is the ruling's premise
becoming a client prerequisite. DEC-89's whole design is that a client sends
the fields it OWNS and omits the rest, and `docs/CLIENT-PREREQUISITES.md` §R6.3
now says so in terms. But it is worth writing down here because the plan's own
acceptance line asks for the state "after re-sending every place unchanged",
and on this contract nine of them cannot be re-sent at all.

**The narrowing that would close it, put and NOT taken:** refuse `visits: []`
only when the place currently HAS occasions — refuse the destruction rather
than the shape. It would make the whole-document round trip work and would
still fail the plan's own named leg's premise not at all (`fushimi-inari` holds
28, so it would still be a 422 with nothing removed). It is declined here
because DEC-89 is a shipped ruling and this worker does not narrow one; and
because the refusal would move out of `ValidatePlace`, which needs no database,
and into the store under the advisory lock. **It is a question for the human,
and it is in the report rather than settled here.**

### THE CITY ROUTE ANSWERS TWO SHAPES, AND THE SHAPE IS READ OFF THE ANSWER

Without `attachTo` one entity moved and DEC-32's bare City is the splice. With
it, the city was created AND a trip's `cityIds` grew — two entities — so the
phone cannot splice what it was not sent and the answer is the whole envelope.

**`CityWritten.Document` is nil exactly when the attach did not happen**, which
makes "which shape did this write earn" a property of the value rather than a
second reading of the request the handler has to get right. Asking
`body.AttachTo != nil` in the handler would be two readings of one fact, and
the failure mode is a 200 whose body is not the shape its own write implies.

**Two routes were the alternative and are worse.** `PUT /v1/cities/{id}` then
`PUT /v1/trips/{id}` is two round trips, two version bumps and a window in
which a city belongs to no trip — a state the client's own `createCity` cannot
even express, because it does both under one `_commit`.

**The new city lands at the END**, which is `t.withCities([...t.cityIds, id])`
and is travel order rather than a set: T1 and T4 draw the itinerary in the
order it was walked. And a **re-PUT does not attach twice** — `ON CONFLICT ON
CONSTRAINT trip_cities_pkey DO NOTHING`, because a PUT on a client-minted key
is retriable and a second append would be a 500 on a request that had already
succeeded.

**COUNTRY IS ONE WIRE FIELD OVER TWO COLUMNS (DEC-59), so it is one `sent`
flag.** Writing `country_code` without `country_name` is not a request this API
can receive and is not a state a row may hold, so a second flag would be a
second way to get it wrong for no expressible gain. The same holds for
`centre`.

**AND AN UNSENT FIELD CANNOT PROPOSE NULL.** The INSERT tuple is validated
against five NOT NULL columns and four CHECKs **before** ON CONFLICT resolves
it, so a rename naming only `{id, name}` has to propose the country and the
centre the row already holds. That is the lesson `readTripForWriteSQL` records
for trips, and it costs more here because there are more of them.

### `= ANY($2)` TAKES A PLAIN `[]string`, AND R4's REASON FOR AVOIDING IT IS WRONG

R4's record says: *"database/sql has no array type, so it needs pgx's `pgtype`,
and spec L20 says pgx is used solely as a blank import driver. The sweep was
right and the draft was wrong."* The conclusion R4 drew — one multi-row `VALUES`
per table — is fine and is not being changed. **The stated reason is wrong, and
it is wrong in a way that would stop the next worker reaching for the right
statement.** Measured at this commit, against this module's own driver and with
no new import anywhere:

```
[]string            n=2 err=<nil>      []bool             n=2 err=<nil>
[]*string (one nil) n=2 err=<nil>      []time.Time        n=2 err=<nil>
[]int64             n=2 err=<nil>      []sql.NullString   n=2 err=<nil>
[]int32             n=2 err=<nil>      []float64          n=2 err=<nil>
```

The precise sentence: `database/sql`'s own `driver.Value` set has no array, but
pgx's stdlib `Conn` implements `driver.NamedValueChecker` and accepts a Go
slice, converting it through pgx's type map. **The CALL SITE imports nothing**,
so `cmd/api/imports_test.go`'s monopoly is untouched — it forbids importing
anything under `jackc/pgx` outside `cmd/api/main.go`, and passing a slice
imports nothing.

That is what makes PERF-MIN-8 implementable as written. `PutTrip` does one
round trip per city, which is irrelevant at five cities and is not at 28
occasions inside the advisory lock; the existence check and the
already-held-elsewhere check are each **one statement for the whole array**.

**The INSERT is still one multi-row `VALUES`, and it is BATCHED.** The wire
protocol counts bind parameters in an int16, so the ceiling is 65,535. This
statement spends 2 on the traveller and the place and **5 per row**, so the
true ceiling is `(65535 - 2) / 5 = 13,106` and `maxVisitsPerStatement` is
**5,000** — a factor of two and a half of room, with the fixture's largest
place (28) still a single statement. It is a batch size and not a cap on the
array: a place visited every week for ten years is 520 occasions and is a log
somebody could really have.

### EmitPlace, AND THE SWEEP THAT MAKES THE RULE A MECHANISM

Measured on this module at this commit:

```
bare Place = {"id":"x","cityId":"kyoto","name":"n","coordinates":{"lat":0,"lng":0},
              "visits":null,"plan":null,"coverAsset":null}
bare City  = {"id":"kyoto","name":"Kyoto","country":{"code":"","name":""},
              "centre":{"lat":0,"lng":0},"coverAsset":null}
bare Traveller = {"name":"Matt"}
```

`place.g.dart:30-32` reads `visits` as `(json['visits'] as List<dynamic>)` —
non-nullable, no null branch — so the app throws on the answer to its own
write. **C1's pin is precisely that request**: a wishlist place has no visits,
so the nil slice is the ordinary create rather than an edge case.

**CITY AND TRAVELLER NEED NONE AND THE REASON IS IN THE CODE**: neither carries
a list field, so there is no nil slice for the marshaller to write as null. An
`EmitCity` would be the empty forwarding method DEC-62 warns against one layer
up. A leg asserts the negative — it walks both entities' emitted keys and
reddens the day either grows a list.

**THE AST SWEEP LANDS HERE AND NOT IN R8**, because R6 is the first step that
can violate the rule; a sweep written after two more steps of unguarded
handlers is a cleanup. It walks every non-test file in `internal/httpapi`,
finds every `httpx.WriteJSON`, and classifies the fourth argument: an
`logbook.Emit*` call is the rule being kept; a composite literal of a LOCAL
type is httpapi's own body shape and cannot be a domain entity; anything else
must be on `bareBodies` with the argument that its type carries no list field.
The list is **equality and not a ceiling**, on `jsonImporters`' precedent, so a
stale exemption reddens it too. A grep cannot make this check — it matches its
own source, it matches comments, and it cannot tell `logbook.EmitPlace(place)`
from the word in a sentence.

### Service.RemovePlace is DELIBERATELY THIN, AND THE THINNESS IS ARGUED

DEC-62 named three operations and warns in terms against "empty forwarding
methods for symmetry". This is the second of the three and it forwards.

**What it owns is the QUESTION, not the statements.** The statement order that
makes D2's delete branch mean what the sheet says has to live inside one
transaction and is therefore internal/postgres's. What is here is the thing no
layer below can hold: `?photos` is REQUIRED, and a `PhotoDisposition` with no
usable zero value is how "there is no default" stops being a rule a handler
remembers.

**The test of a forwarding method is whether deleting it changes anything.**
Delete this one and `photosUnspecified` reaches the store as
`deletePhotos == false`, which is D2's KEEP branch: a caller that never
answered the question gets one of the two answers, silently. That is the same
defect class as `[]Visit` making absent and empty one value, one route over —
and the mutation reddens `TestTheServiceRefusesADispositionNobodyChose` alone.

### THE MUTATIONS, AND THE TWO THAT SAID SOMETHING

Every one run against this working tree, restored by file copy, each checked to
have actually changed the file before the suite ran.

| mutation | reddens |
|---|---|
| the visits write as **delete-then-insert** | 4 legs, in two packages |
| the ordinal offset **downward** | 6 legs — `visits_ordinal_ck`, a red for a different reason |
| the offset is the plan's **fixed `+ 1000`** | **1 leg**: the 1,100-occasion reorder, and nothing else |
| D2's delete branch **drops the place first** | 2 legs, on the WHOLE-LOG count |
| the two branches collapse: the photographs **always** go | 2 legs, both on the keep branch |
| `?photos` **defaults to keep** | 3 legs, in two packages |
| the attached city is **prepended** | 3 legs |
| the trip_cities read stops being ordered | 4 legs, three of them older than this step |
| the **occupied-occasion** guard deleted | 2 legs |
| the **another-place** guard deleted | 1 leg |
| the Service's **disposition** guard deleted | 1 leg |
| `visits: []` **accepted** | 3 legs, in three packages |
| the route answers the **bare `Place`** | 2 legs: the emit leg and the AST sweep |

**The `+ 1000` row is the one that justifies a divergence.** It reddens exactly
the leg written for it and nothing else, which is what makes the derived offset
a correction rather than a preference.

**And one mutation the plan names is NOT CONSTRUCTIBLE HERE.** "Clear
`place_id` but not `visit_id` on the keep branch" has no source to mutate: the
keep branch is a single `DELETE FROM places` and both columns are cleared by
foreign keys. The four-field leg still guards it — a migration that changed
`photos_visit_fk` reddens it — but no Go mutation can, and saying so is the
honest form of the row.

### A LEG OLDER THAN THIS STEP IS FLAKY, AT A MEASURED 1 IN 64

`TestAuthenticateRefusesEveryShapeOfWrongToken` (VS6) builds its
"one character changed" case as `"Z" + issued.Token[1:]`. When the token's
first character already IS `Z`, the mutated token is the issued one,
`Authenticate` succeeds and the leg fails. Tokens are 32 bytes of
`crypto/rand` in `base64.RawURLEncoding`, so the first character is uniform
over a 64-symbol alphabet.

Measured, 64,000 tokens: **first character `Z` in 1,037 of them, 1.620%**,
against 1/64 = 1.563%. It fired twice during this step's mutation runs, both
times inside a full `go test ./...`.

**It is left alone deliberately.** It is VS6's leg, the fix is a decision about
what that case is for rather than a typo, and `make check` is the only gate —
so a worker seeing it go red should know it is this and not their change.

### Divergences from the step's file list, each deliberate

- **`internal/logbook/geography.go` is new and is not in the list.** The list
  names `emit.go` and `service.go` as edits and gives the write types nowhere
  to live. They are in a file of their own on `share.go`'s precedent, which
  holds `ShareWrite`, `ShareMint` and their validator together; `validate.go`'s
  own header says it is about a TRIP write.
- **`internal/logbook/store.go` is edited and is not in the list**, for the two
  new ports. The domain declares the contract and internal/postgres satisfies
  it (DEC-62), so a new store implementation with no interface above it would
  invert the layering the whole file exists to state.
- **`internal/httpapi/emit_sweep_test.go` is new.** The definition of done asks
  for the sweep in R6 and the file list stops at "+ tests".
- **`cmd/api/main.go` and `internal/httpapi/blocked_request_test.go` are
  edited**, because `Mount` panics on a nil port and both build a `Deps`.

### What R6 leaves guarded by nothing

- **`visits: []` against a place that has no visits.** The refusal is the
  ruling's letter and it refuses a request that destroys nothing — measured, 9
  of the client's own 17 places cannot be re-sent as emitted. Nothing tests the
  narrowed form because the narrowed form is not implemented; what is written
  down is the question, above and in the report.
- **`maxVisitsPerStatement`'s VALUE.** 5,000 is derived from the wire
  protocol's 65,535 in the comment, and the longest array any leg sends is
  1,100 — so the batching branch itself is never taken. Setting it to 5 would
  be a green suite and a correct result; setting it to 20,000 would be a green
  suite and a statement that fails on an array nobody has. It is the same hole
  `TouchInterval` and `MinShareTokenBytes` already have, and it is now five
  constants wide.
- **The `attachTo` ordinal against a REAL gap.** `ON CONFLICT DO NOTHING`
  consumes a number, so ordinals can gap — which is legal and asserted nowhere,
  because no fixture produces one. A read that silently sorted by value rather
  than by `ordinal` would still pass.
- **`MaxNoteBytes`'s VALUE.** 4,096 is this build's policy on `places.plan` and
  `visits.note`, both of which are unbounded `text`. The legs assert through
  the constant, so the number is defended by nothing.
- **The client's half of the `visits` contract.** `docs/CLIENT-PREREQUISITES.md`
  §R6.3 says the client must OMIT the key rather than send `[]`, and nothing in
  this repository can check what the client sends. Same tier as the iOS
  manifest flags: guarded by somebody reading it.

### Commands, not numbers

```bash
# the legs, and what the database variable buys, at this commit
                       go test ./... -count=1 -v | grep -c -- '--- PASS'   # 649
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'    # 872

# the counts this step moved, each re-derived rather than incremented
grep -c 'http.Method' internal/httpapi/routes.go                  # 16 routes
ls migrations/*.up.sql | wc -l                                    # 4 migrations
grep -cE '^\tCode[A-Za-z]+ +Code = ' internal/httpx/errors.go      # 13 codes
grep -cE '^\s*assert_(eq|contains) ' scripts/slice-arc.sh          # 168, was 132

# the arc, under its own project, so a live volume is untouched
COMPOSE_PROJECT_NAME=travellog-r6arc API_PORT=8097 POSTGRES_PORT=5477 \
  MINIO_PORT=9017 S3_PUBLIC_BASE_URL=http://127.0.0.1:9017 make slice

# the seed, on a SECOND stack, because the arc ends holding a traveller
COMPOSE_PROJECT_NAME=travellog-r6seed API_PORT=8098 POSTGRES_PORT=5478 \
  MINIO_PORT=9018 S3_PUBLIC_BASE_URL=http://127.0.0.1:9018 make seed

# the state the model has never expressed, before and after re-sending
psql "$DSN" -c "SELECT count(*) FROM photos
                WHERE place_id IS NOT NULL AND visit_id IS NULL"     # 0
psql "$DSN" -c "SELECT count(*) FROM photos WHERE place_id IS NOT NULL" # 95

# DEC-57, and the key that fires depends on what still points at the city
psql "$DSN" -c "DELETE FROM cities WHERE id='kyoto'"
#   -> trip_cities_city_fk   (the itinerary is checked first)
# with the itinerary, the photographs and the walks cleared inside a
# transaction that rolls back:
#   -> places_city_fk        (which is the key R6's acceptance check names)
```

