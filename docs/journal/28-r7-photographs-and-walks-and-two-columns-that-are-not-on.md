## R7 — photographs and walks, and two columns that are not on a type

The seventh step of plan-v7. Five routes, of which one answers two different
shapes and one is the most intricate write in the system — and the step where
DEC-89's pointer contract stops being a convention and becomes a fact about a
struct.

**Seven commits and one section**, which is R1's reading of DEC-23 and for its
reason. What is here was written as the step ran.

### The five routes, and what each answers

```
POST   /v1/photos/snooze          200 + the rows it wrote   + ETag
PUT    /v1/photos/{id}            200 + a bare Photo        + ETag
DELETE /v1/photos/{id}            204                       + ETag
POST   /v1/photos/{id}/refile     200 + a Photo, OR the WHOLE ENVELOPE
PUT    /v1/walks/{id}             200 + a Walk              + ETag
```

**Twenty-one rows in the table now**, and five and not six: N1's two walk
controls are two fields of one body on one path. `setWalkName` and
`dismissWalk` write two columns of one row and DEC-89's contract is what tells
them apart — the same reason R5's six was not seven and R6's three was not
four.

**There is deliberately no `DELETE /v1/walks/{id}`.** N1's 'Discard' is a flag:
"Discarding the nudge and discarding the recording are different things, and
only the first is drawn on N1." D2's sheet promises the track stays with its
day on **both** branches, and `walks` has no `place_id` at all. Nothing in this
app authorises destroying a recording of a day, so no route offers one.

**That is the same argument that leaves `DELETE /v1/cities/{id}` out of R6 and
it is weaker in one respect, which is worth naming.** There the database is the
backstop — `places_city_fk`, `photos_city_fk` and `walks_city_fk` are RESTRICT
(DEC-57), so whoever adds the control is stopped at exactly the moment they
should be writing the sheet copy. Here there is no such key. `walks` is
referenced by nothing, so `DELETE FROM walks` would simply work, and the only
thing standing between a future worker and a destroyed track is this paragraph.

**Two PUTs are creates, and that is DEC-33 rather than a liberty.** Nothing
else in this API creates a photograph or a walk — the media routes create an
OBJECT, and the row that references it has to arrive somehow — so both are
upserts on client-minted keys, exactly as `PUT /v1/places/{id}` and
`PUT /v1/cities/{id}` are. It is also what the plan's own route table implies:
"validates that `asset` names a COMMITTED object" is a check that only has work
to do when the body may carry an asset.

### `PhotoWrite` HAS NO `placeId` AND NO `visitId`

This is the step's worst defect made unreachable, and it goes further than
DEC-89 asks on purpose.

**The defect, measured by the safety lens on postgres:17.11.** `ph-0` carried
`place_id=bukchon, visit_id=v-bukchon-0` and a caption. Under the whole-state
convention a body of `{caption}` — M2's 'Write a note', which owns the note and
nothing else — writes both to NULL alongside it, and the log has no record that
the photograph was ever at Bukchon. **All three of this plan's standing guards
pass on it:**

| the guard | why it is blind |
|---|---|
| `expectNoDanglingReferences` / `no_dangling_references.sql` | the reference is GONE, not dangling |
| R6's `place_id IS NOT NULL AND visit_id IS NULL` | there is no place left to be occasion-less |
| the pair-agreement assertion | both columns are NULL, and two NULLs agree |

**The pointer contract alone closes it. There are two ways to get the contract
wrong and only one of them is a pointer.** `TripWrite` set the precedent: the
four sharing fields are not on it AT ALL (SF6), because `PUT /v1/trips/{id}`
does not own them — H1's three switches do, through their own route. The filing
is the same kind of thing. Exactly three things in this system write
`photos.place_id`:

```
POST /v1/photos/{id}/refile   M2.2's 'Change' — sets BOTH, together
DELETE /v1/places/{id}        D2 — clears BOTH, through two foreign keys
`make seed`                   the load, which writes whole rows
```

and no control in the client sets a photograph's pin any other way. A slot on
this type would be a FOURTH writer of a field three things already own, and
DEC-83 makes the cost concrete: the pair is coherent by a GO rule and not by
the schema — the paired CHECK was executed and aborts D2's keep branch — so
every extra writer is another place it can be written incoherently.

**What that costs in evidence is stated rather than claimed as a win.** The
plan predicts a mutation for the caption leg — "make one write field a value
rather than a pointer in `PhotoWrite`" — and against this type that mutation
cannot touch the filing, because there is no field to demote. The leg is
guarded by a STORE mutation instead, which is strictly stronger and was run:
adding `place_id = NULL, visit_id = NULL` to `upsertPhotoSQL`'s SET clause
reddens it. The pointer mutation still has work on the fields that ARE there,
and it was run on `Caption`.

### THE COUNT THAT MUST NOT FALL, AND WHERE IT MOVES

`SELECT count(*) FROM photos WHERE place_id IS NOT NULL` is **95** on the
seeded log. Measured at this working tree, one route per row:

| route | the count |
|---|---|
| `PUT /v1/photos/{id}` (caption only) | 95 → **95** |
| `POST /v1/photos/snooze` | 95 → **95** |
| `PUT /v1/walks/{id}` (either control) | 95 → **95** |
| `POST /v1/photos/{id}/refile`, between pins | 95 → **95** |
| `POST /v1/photos/{id}/refile`, of an unfiled photograph | 95 → **96** |
| `DELETE /v1/photos/{id}` of a filed one | 95 → **94** |

**It is the only assertion that can see the destruction, and the three zeroes
are asserted beside it rather than instead of it.** A dangling count of zero is
satisfied by unfiling every photograph in the log; the count is not.

### THE PLAN'S OWN NAMED FAILING TEST WAS GREEN 10 TIMES OUT OF 10

This is the sharpest thing in the step, and it is the **third** time this
project has found it — R5 found the first, R6 the second.

R7's test strategy writes the refile leg out in full, names the mutation it is
for (an unordered `SELECT … LIMIT 1` that picks the occasion itself), and adds
`-count=10` because "an unordered SELECT can return the right row by luck and a
single green proves nothing". Transcribed literally and run against that exact
mutation:

```
mutation applied, the plan's leg as written    10 PASS / 10
```

**The luck is not random.** The leg names ONE occasion — "deliberately NOT the
newest" — and the planner returned that very row, every time, so the picker
agreed with the client and the assertion could not tell them apart. `-count=10`
buys nothing against a deterministic plan.

**"Not the newest" is not the property that matters.** The property is "not
whichever row the planner returns", and nothing in a test can know which that
is. What ships files the SAME photograph to EACH occasion in turn and asserts
it lands on the one named every time: a picker answers with one row and cannot
answer with four.

```
mutation applied, the corrected leg            10 FAIL / 10
control                                        10 PASS / 10
```

**And the fixture is stronger than the plan knew.** `nishiki` holds FOUR
occasions on `japan-2026` at **one instant** — 2026-09-18T09:10:00.000Z, four
times over — not merely on one day. So nothing about the data can break the
tie: not the date, not the timestamp. Only the id the client sent can. That is
why `renumberVisitsByTimeSQL` orders by `at DESC, id` and not by `at` alone
(DEC-26).

**A second correction of the same class, on the cap.** The 500/501 leg read
`logbook.MaxWalkPoints` in every position, so raising the constant to 21,600
raised its own inputs with it and the leg stayed green — the mutation reddened
the byte-ceiling leg alone. DEC-106 fixes the number, so it is a literal now.

### `visitAt` IS USED ONLY WHEN THE OCCASION IS NEW

An occasion is SHARED: thirty photographs at `fushimi-inari` hang off
twenty-eight of them. Applying `visitAt` to an occasion that already exists
would re-time it for every other photograph filed there, reorder the place's
visits array, and move `lastVisited` on P1 — which is the one-thing-too-many
defect, from a control whose whole promise is that it moves ONE photograph.

**The refile's four refusals, each for its own reason:**

- **The photograph must exist** — 404, not a create. The client's own
  asymmetry: `setPhotoCaption` and `refilePhoto` answer false for an unknown
  id and `deletePhoto` answers true, so the same id is a 404 here and a 204 on
  the delete.
- **The place must be in the photograph's city.** The client refuses it too and
  the server is not entitled to assume the client did. Measured: 0 of 284
  fixture photographs name a place in another city.
- **An existing occasion must belong to that place.** `visits_pkey` is
  `(traveller_id, id)`, so a visit id is unique across the whole log and naming
  another place's would file the photograph somewhere nobody mentioned — the
  hazard `refuseVisitsHeldElsewhere` refuses one route over.
- **An existing occasion must be on the photograph's trip.** Measured: 0 of 284
  do otherwise, and the client's `place.visitsOn(photo.tripId)` cannot produce
  one. A photograph filed to another trip's occasion lands in the wrong year
  row on P1 and in that trip's cascade.

**And the answer carries two shapes.** `PhotoRefiled.Document` is nil exactly
when no occasion was minted — `CityWritten`'s own device, for its reason: a
minted occasion moves the PLACE as well as the photograph and rewrites every
one of that place's ordinals, so the phone cannot splice what it was not sent.

### THE ORDINAL REWRITE IS A PERMUTATION, AND IT PARKS FIRST

`visits_place_ordinal_uq` is checked per **ROW** during a statement, so writing
0..n-1 over a set that already holds 0..n-1 collides mid-statement even when
the final state is unique. Three statements, in order:

```
INSERT the occasion at max(ordinal)+1      strictly above everything
offsetVisitOrdinalsSQL                     everything parked above n
renumberVisitsByTimeSQL                    0..n-1 by `at DESC, id`
```

Deleting the park reddens the renumber leg — run, and it is the same lesson
`offsetVisitOrdinalsSQL` already records for the visits array.

### THE TRACK IS BUILT IN SQL AND GO NEVER TOUCHES JSON

`walks.points` is jsonb. Three candidates, and the one that ships costs
nothing:

- `json.Marshal` here would make internal/postgres the **third** entry on
  internal/httpx's encoding/json monopoly list — a list whose whole value is
  that a new entry has to argue for itself. Spending that on a column value is
  a bad trade.
- Hand-rendering the array as text is what `internal/seed` does, and its own
  comment concedes it exists only because of the same monopoly. A second
  implementation of a format.
- `unnest($7::float8[], $8::float8[]) WITH ORDINALITY` pairs two arrays,
  `jsonb_agg(… ORDER BY ord)` makes the order EXPLICIT, and the read already
  unnests the same way. The write and the read now use one mechanism in one
  direction.

Round-tripped **float for float at full float64 precision, 500 points**, in
through `jsonb_build_object` and out through `->>` and a cast — three
conversions the type system does not vouch for.

### THE NUMBERS DEC-93 RESTS ON REPRODUCE, AND ONE OF DEC-106's DOES NOT

Measured through the shipped types at this commit, on **unrounded**
coordinates — which is what a location plugin hands out, and the single thing
the byte count is most sensitive to:

```
   500 points     25,629 B     41x inside httpx.MaxBodyBytes
21,600 points  1,099,622 B     six hours at 1 Hz, and OVER 1 MiB
```

The second reproduces DEC-93's own 1,099,390 B to within the walk's scalar
fields, so the ruling was measured the same way. **DEC-106 calls 26 KB "TWO
ORDERS OF MAGNITUDE inside the 1 MiB body ceiling" and that arithmetic does not
survive recomputation**: 1,048,576 / 25,629 is **41**, between one order and
two. The size is right, the multiplier is not, and the conclusion is untouched.

**And the byte count of a track is a claim about coordinate precision.** The
same 21,600 fixes rounded to seven decimal places are **794,666 B** and FIT
inside the ceiling. That does not reopen the cap — the ceiling is one of
DEC-93's two arguments and the read path's growth term is the other — but it is
what stops somebody re-deriving "it fits" from a rounded fixture.

### `points: []` IS REFUSED ON SHAPE, AND THAT IS NOT DEC-109 UN-LEARNED

DEC-109 narrowed `visits: []` from a shape refusal to a destruction refusal,
and the reason was measured: nine of the client's seventeen places are wishlist
places, `EmitPlace` writes `"visits": []` for every one of them, and the server
refused a document it had itself produced.

**There is no wishlist walk.** `walks.points` is NOT NULL and 0003's
`walks_points_present_ck` bounds it below, so no stored walk has ever had an
empty track, `Emit` cannot produce one, and every empty array reaching this
route IS the destruction. The half only a database can check is asserted
against the client's own log: `count(*) FROM walks WHERE
jsonb_array_length(points) = 0` is **0**. Both halves are in one leg anyway,
because from the refusing side a guard that cannot tell two cases apart looks
identical to a correct one.

### THE SNOOZE IS THE FIRST ROUTE THAT TAKES A COLLECTION

One statement, one transaction, one advisory lock, **one version bump**. There
is no partial.

**One bump and not one per photograph**, because `logbook_version` is the
ETag's second half: N bumps for one user action hand the client N-1 versions it
can never have held and invalidate its cached document N times for one write.

**An unknown id is SKIPPED**, matching the client — "the row was derived from
the log a frame ago and a photograph deleted since is one that no longer needs
filing". **A group that matches nothing writes nothing, moves no version, and
answers 200 with `"photos": []`**, which is `snoozeUnfiledPhotos` returning
false without committing.

**The answer is in ID ORDER and never nil.** An UPDATE's `RETURNING` order is
the order rows happened to be visited, and two reads of one write that differ
are two bodies under one ETag. The slice is MADE rather than appended to from
nil, because a nil slice marshals to `null` even inside httpapi's own body
type.

**And it answers a local wrapper rather than a bare array**, on `mintBody`'s
precedent — the other route that takes a collection. A top-level JSON array is
the one shape that cannot grow a sibling key without breaking every client that
reads it, and this one has an obvious future sibling: how many ids were
skipped.

### D1 IS THE ONLY DESTRUCTIVE ROUTE IN THIS PLAN THAT ANSWERS 204

Nothing in this schema references a photograph, so there is no cascade, no
sheet copy to implement, no statement order that matters and no `_repointed` to
write. D2 and D3 answer an envelope because "the cache cannot splice a
cascade"; one row leaving is exactly what a cache CAN splice.

**It still carries an ETag, and that is what the 204 is FOR.** The phone has
just spliced a deletion into a document it caches under a version; without the
new tag its next conditional GET either refetches the whole log or keeps
serving a body that still holds the photograph. An unknown id is a 204 that
moves nothing, which is what stops a retried delete throwing that cache away
(DEC-103).

### A GREP IN R6's OWN RECORD IS WRONG AT THIS COMMIT

R6's 'Commands, not numbers' block carries
`grep -c 'http.Method' internal/httpapi/routes.go # 16 routes`. At R7 that
answers **22** against **21** rows, because the sentence documenting the
command matches it. Anchored — `grep -cE '^\t\t\{http\.Method'` — it answers
21. That is rule 10's own failure, a grep matching its own source, turning up
in a comment rather than in an acceptance check. The guard that depends on no
pattern at all is `TestEveryRouteInTheTableReachesTheMux`.

### THE ACCEPTANCE CHECK CANNOT PASS AGAINST CORRECT WORK, IN FOUR PLACES

The fourth step in a row. Run verbatim and reported in full in the step report;
in summary:

1. `make check && make slice && make seed` — `make slice` under the default
   project is REFUSED by DEC-92's own guard, which R4 added and which five
   acceptance checks still ask for. exit=2. Under its own project name it is
   exit=0.
2. `psql "$TEST_DATABASE_URL" -f scripts/no_dangling_references.sql` — the file
   **did not exist**; three acceptance checks have named it since v7.0 and no
   step's file list ever created it. And `TEST_DATABASE_URL` is the *test*
   database, which holds no schema at rest, so the query answers
   `relation "photos" does not exist`.
3. **`psql -f` exits 0 on a failed statement.** Measured before the fix: the
   file printed `ERROR: relation "photos" does not exist` and answered
   **exit=0**. `\set ON_ERROR_STOP on` is what makes it able to fail (exit=3 on
   an empty database, 0 on a seeded one). `psql -c` gets it right by itself.
4. The curl names `kyoto-walk-1` on `127.0.0.1:8080`. The client fixture holds
   `w-busan` and `w-kibune` and no `kyoto-walk-1`; 8080 is the live stack. Run
   against the verbatim id on a safe stack, the route answers
   `422 {"code":"invalid_field","field":"tripId"}` — correctly, because DEC-33
   makes an unknown id a CREATE — and `jq '.points | length'` on that body is
   `null`, which is what the check says must never happen.

`go test ./internal/httpapi/ -run Refil -count=10` is the one line that is
FINE, and it is worth saying why it is nearly not: `-run Refil` does select
three legs in that package, but the leg that can see the mutation lives in
`internal/postgres` and `internal/seed`, over a real database. The narrowed
form is `go test ./internal/postgres/ ./internal/seed/ -run Refil -count=10`.

### Divergences from the step's file list, each deliberate

- **`internal/logbook/photo.go` and `internal/logbook/walk.go` are new and are
  not in the list.** The list names `emit.go` and `service.go` as edits and
  gives the write types nowhere to live — the same gap R6 filled with
  `geography.go`. They are TWO files and not one because photographs and walks
  are not one contract: `geography.go` holds a city and a place together
  because their refusals reference each other, and a walk references nothing.
  It has no `place_id` at all, and that absence is D2's own promise.
- **`internal/logbook/store.go` is edited and is not in the list**, for the two
  new ports, on R6's precedent.
- **`internal/httpapi/routes_test.go`, `emit_sweep_test.go`,
  `auth_handlers_test.go`, `logbook_handlers.go`, `cmd/api/main.go` and
  `blocked_request_test.go` are edited.** The route count is a literal, the
  sweep's exemption list is equality, the harness needs a twin, four sentinels
  now share one 404 branch, and `Mount` panics on a nil port.
- **`scripts/no_dangling_references.sql` is new**, and it is a gap rather than
  an addition — see above.

### What R7 leaves guarded by nothing

- **The absence of `DELETE /v1/walks/{id}`.** Nothing in the schema stops one:
  `walks` is referenced by nothing, so a future `DELETE FROM walks` would
  simply work. R6's equivalent decision has three RESTRICT keys behind it and
  this has a paragraph. **Trigger:** a control that deletes a walk, at which
  point the sheet copy is written first.
- **`MaxCaptionBytes`'s VALUE.** 4,096 is this build's policy on
  `photos.caption`, which 0003 bounds only for emptiness. The legs assert
  through the constant, so the number is defended by nothing. It is now the
  sixth constant in this position, beside `MaxNoteBytes`, `MaxNameBytes`,
  `MaxSummaryBytes`, `TouchInterval`, `MinShareTokenBytes` and
  `maxVisitsPerStatement`.
- **The client's half of the caption contract.** `{"caption":null}` is
  INDISTINGUISHABLE from an absent key — measured on go1.26 — so M2's cleared
  note has to send `{"caption":""}`. `docs/CLIENT-PREREQUISITES.md` §R7.2 says
  so and nothing in this repository can check what the client sends. Same tier
  as the iOS manifest flags.
- **`float64Slice`'s parser against a hostile array literal.** It reads
  `{1.5,2.5}` and nothing else — no quoted elements, no NULLs — because its
  only producer is the LATERAL above it. A different producer would need a
  different reader and nothing says so but its own comment.
- **The re-file under concurrency.** Two re-files of one photograph serialise
  on the traveller's advisory lock, which is right; two travellers cannot
  collide because every statement is keyed on `traveller_id`. Nothing tests
  the first claim, and the only concurrency leg in this project is
  `TestCreateSessionWaitsForTheTravellerLock`.

### Commands, not numbers

```bash
# the legs, and what the database variable buys, at this commit
env -u TEST_DATABASE_URL go test ./... -count=1 -v | grep -c -- '--- PASS'  # 689
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'     # 939

# the counts this step moved, each re-derived rather than incremented.
# THE ROUTE GREP IS ANCHORED: the unanchored form R6 used answers 22 here,
# because the sentence documenting it matches.
grep -cE '^\t\t\{http\.Method' internal/httpapi/routes.go          # 21 routes
ls migrations/*.up.sql | wc -l                                    # 4 migrations
grep -cE '^\tCode[A-Za-z]+ +Code = ' internal/httpx/errors.go      # 13 codes
grep -cE '^\s*assert_(eq|contains) ' scripts/slice-arc.sh          # 230, was 168

# the arc, under its own project, so a live volume is untouched
COMPOSE_PROJECT_NAME=travellog-r7arc API_PORT=8099 POSTGRES_PORT=5481 \
  MINIO_PORT=9019 S3_PUBLIC_BASE_URL=http://127.0.0.1:9019 make slice

# the seed, on a SECOND stack, because the arc ends holding a traveller
COMPOSE_PROJECT_NAME=travellog-r7seed API_PORT=8100 POSTGRES_PORT=5482 \
  MINIO_PORT=9020 S3_PUBLIC_BASE_URL=http://127.0.0.1:9020 make seed

# the standing guards and the one they are blind to, in one output
psql "$SEED_DSN" -f scripts/no_dangling_references.sql
#   -> six zeroes, and 95

# the refile leg that can see the mutation, and the packages it is in
go test ./internal/postgres/ ./internal/seed/ -run Refil -count=10

# the fixture shape the whole re-file argument rests on
psql "$SEED_DSN" -c "SELECT count(*), count(DISTINCT at) FROM visits
                     WHERE place_id='nishiki' AND trip_id='japan-2026'"
#   -> 4 | 1     four occasions at ONE instant, so no date can break the tie
```

