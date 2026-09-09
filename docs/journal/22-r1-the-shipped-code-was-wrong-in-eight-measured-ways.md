## R1 — the shipped code was wrong in eight measured ways

The first step of plan-v7, and the first step in this repository that **fixes
running code rather than adding to it**. Seven of the twenty-one lens blockers
are defects in code that was already deployed; every one was measured against a
real build rather than read.

**It is eight commits and one CLAUDE.md section, and that is a deliberate
reading of DEC-23.** The rule is that the record is written in the same commit
as the code; a step this size written as one commit would be unreviewable, and
a section per commit would be eight sections about one step. What is here was
written as the step ran, not reconstructed at the end.

### The eight, and what each was measured against

| # | Ruling | The defect, as measured |
|---|---|---|
| 1 | DEC-89 | `PUT /v1/trips/{id}` answered **200** to `{id, name}` and left `trip_cities` at zero rows with both dates null |
| 2 | DEC-90 | a zoneless date is refused, and the client can send nothing else — T4's "Add dates" failed on **every** request |
| 3 | DEC-91 | the write response carried `shareLinkId: null`, so an ordinary rename killed a live share link the client held the only copy of |
| 4 | DEC-94 | `GET /v1/logbook` answered **200 with a valid ETag and a body cut mid-token** over a slow link |
| 5 | DEC-96 | a request blocked on a lock returned `curl http=000` — no status, no body, no error — and an outage answered 500 |
| 6 | DEC-101 | 15,151 of 15,960 access lines read `durationMs:0`; no latency question was answerable at all |
| 7 | DEC-102 | three NOT NULL date columns scanned through `sql.NullTime` with `.Valid` never checked; two counts wrong where they were written |
| 8 | DEC-103 | every unbuilt route answered `not_found` — the same word the vocabulary uses for "that trip is not in your log" |

Plus migration 0002 (DEC-82), the `lock_timeout` (third of three), the health
`start_period` (OPS-MAJ-4), the startup ping's elapsed time (OPS-MIN-11), and
`docs/CLIENT-PREREQUISITES.md`.

### THE DEFECT THAT DECIDED THE ORDER, AND WHY THE TYPE WAS THE FIX

`TripWrite.CityIDs` was a bare `[]string`, so **absence and emptiness were the
same value** and `checkCityIDs(nil)` loops zero times. Reproduced from outside
the test binary against a container built at `e4a3b94`, before anything was
changed:

```
create with three cities and both dates -> 200
  SELECT count(*) FROM trip_cities WHERE trip_id='autumn'  ->  3
PUT {"id":"autumn","name":"Autumn crossing, renamed"}      -> 200
  {"cityIds": [], "start": null, "end": null}
  SELECT count(*) FROM trip_cities WHERE trip_id='autumn'  ->  0
```

**The author had already reasoned this out one line above the statement, for
three columns.** `share_photos`, `share_notes` and `share_coordinates` are left
out of the SET clause because *"naming them in EXCLUDED-form would silently
reset a group this route does not own"*. Five columns on the same statement got
the other answer. That is the whole story of this defect, and it is why the fix
is a **type** and not a patch: R6 and R7 write eleven more write routes on the
same convention, over `visits` (whose child is `photos_visit_fk ON DELETE SET
NULL`), over photo filings, and over `walks.points` — which cannot be retyped,
because a GPS track is a recording of a day that has passed.

### The double pointers buy less than they look like, and it is measured

`**T` lets a nullable field carry three states in Go — absent, sent-as-null,
sent-with-a-value. **On the wire it carries two.** `encoding/json`'s `indirect`
breaks at the outermost SETTABLE pointer when the literal is null, so
`{"summary":null}` sets the `**string` field itself to nil. Probed on go1.26:

```
{"name":"n"}                   -> summary=nil
{"name":"n","summary":null}    -> summary=nil
{"name":"n","summary":"s"}     -> summary=value s
```

So a client **cannot clear a summary, a cover or a date over HTTP today**, and
nothing in the client can ask to. The third state is reachable from Go and the
store honours it, with a leg. The day a control needs it, the answer is an
explicit sentinel in the body — recorded in `TripWrite`'s doc rather than left
for whoever writes that control.

### Two refusals became reachable, and one of them is Postgres's decision

- **A create with no name.** Absent means leave alone, and on a create there is
  nothing to leave. Refused under the traveller's advisory lock with a named
  field, because only the store knows whether it is holding a create.
- **A partial date write that would invert the order.** `trips_dates_ordered_ck`
  compares the two columns after the write, so a body carrying only `start`
  against a trip that ends in October is a violation `ValidateTrip` cannot see.
  New under DEC-89: a whole-state upsert always carried both.

**And one thing Postgres decided rather than the design.** The proposed INSERT
row is checked against NOT NULL **before** the conflict is resolved, so
`VALUES (…, NULL, …)` on a name-only PUT answers SQLSTATE 23502 — the CASE
would have discarded that NULL a moment later, but the tuple never gets there.
Found as a red test. An unsent name therefore proposes the name the row already
has, read in the same statement that answers the two questions above.

### DEC-96 needed a shape the ruling's own list does not name

DEC-96 names "pgconn connect errors, `sql.ErrConnDone`, a pool-acquire
deadline", and all three are visible to the standard library — measured against
a real pool pointed at a dead port, `*pgconn.ConnectError` satisfies
`errors.As(err, &net.Error)` because it wraps the dial's `*net.OpError`.

**The ruling's own leg then failed at 500.** With a lock held on `trips`, the
blocked read is cut off by `statement_timeout` and comes back as SQLSTATE
57014, which is neither a net error nor a context deadline. So the classifier
also reads SQLSTATE through a **structural interface**, `interface{ SQLState()
string }`, which matches `*pgconn.PgError` without naming it — the same idiom
DEC-62's `Coder` uses in the other direction, and the only one available under
spec L20. Four classes: `08`, `53`, `57`, `55P03`. **Not `40001` and not
`40P01`**: both are retryable in principle and neither is a dependency being
unavailable — they are this application's own concurrency, and one in a log is
a defect to fix rather than an outage to wait out.

**The lesson is the general one.** A ruling's list of shapes is a list somebody
wrote from the shapes they happened to trigger. Writing the leg is what finds
the fifth.

### The three bounds are three, and the pair-mutation proves it

`lock_timeout` bounds the **migration's wait** and nothing else — OE-19, which
corrects a belief the plan held. Run at this working tree:

- Remove the **migrator's** `lock_timeout` → the migration leg reddens on its
  deadline, and the blocked-request leg stays **green**.
- Remove the **503 branch** → the blocked-request leg reddens on its **status**
  and not on its deadline, and the migration leg stays green.

If either mutation reddened both, the three bounds would be one bound wearing
three names.

### Every mutation run in this step, with the leg that had to stay green

| mutation | reddens | stays green |
|---|---|---|
| `CityIDs` always replaced (the bare `[]string`) | the rename leg, on `cityIds` **and** the row count | the sharing-fields leg, the filed-photograph count |
| accept a zoneless date via a layout with no zone | the zoneless leg **only** — every other package `ok` | everything |
| `shared` as a bare `EXISTS` (no `revoked_at IS NULL`) | the rename leg's **second** half, the revocation | the rename leg's **first** half |
| delete the `UPDATE` from 0002 | the backfill leg | the defaults leg, the catalog leg |
| `share_notes` DEFAULT back to false, alone | the defaults leg naming **shareNotes** | the backfill leg |
| `lock_timeout = 0` | the migration leg, on its deadline | the blocked-request leg |
| drop `Vary` | the Vary legs only | every Content-Encoding assertion |
| remove the 503 branch | the blocked-request leg on its **status**, the handler legs, the classifier legs | the genuine-fault leg |
| `not_found` back in the mux's prebuilt body | four legs | the handler's-own-404 leg |
| `durationUs` holding **milliseconds** | the non-zero assertion | the presence assertion |

### THE MUTATION THAT SURVIVED, AND IT IS THE MOST USEFUL THING IN THIS SECTION

The last row did **not** redden on the first attempt. `durationUs` holding
milliseconds passed, because the leg's handler slept **2ms** — and two
milliseconds is two milliseconds in either unit. The mutation the ruling
explicitly names as "a rename that looks like a fix and is not" was invisible
to the leg written to catch it.

The leg sleeps **200µs** now: under a millisecond, over a microsecond, and
squarely in the range where 95% of this API's requests live — which is the
range the defect was measured in, 15,151 of 15,960 lines. **A leg has to run in
the regime the defect occupies**; one written in a comfortable regime passes
against the very thing it names in its own error message.

### Two numbers were carried and both were wrong

- **One whole-log read is TEN SELECTs, not six.** `logbook_store.go`'s own doc
  comment said six from the day it was written. Six is the count of LISTS in
  the document — which is what `read_tx.go` says, and is fair there. This was a
  COUNT, so this one was wrong. Measured with `pg_stat_statements`, each
  `calls = 1`: photos, visits, trip_cities, places, cities, trips, walk_points,
  walks, logbook_version, traveller name.
- **The size premise was 85,422 bytes in five places, and that is the CLIENT's
  file on disk.** Measured through this build:

  ```
  go test ./internal/logbook/ -run TestTheEmittedSizeIsLarger -v
    the client's file on disk:            85422 bytes
    emitted through THIS build, as-is:    85525 bytes
    emitted with DEC-46 object ids:       95586 bytes
  ```

  **plan-v7 gives this figure as 99,271 and this build measures 95,586.** The
  difference is 3.7%, it changes no argument the number is used in, and the
  number written into this repository is the one that was run here. The leg
  **logs** all three figures and asserts only the relationships, so it does not
  redden on an unrelated change.

### What R1 leaves guarded by nothing

- **The Dockerfile's `start_period`.** `analysis_options`-style exclusions do
  not apply here, but nothing in `go test ./...` reads a HEALTHCHECK directive,
  and `test/image` does not assert it. Setting it back to 3s is a green suite
  and a deploy that reports failure on every migration that has to wait. Same
  tier as the iOS manifest flags in the client: guarded by a human with a
  device.
- **`REQUEST_TIMEOUT`'s DEFAULT value.** The artefact legs assert the variable
  is present in compose and in `.env.example`; nothing pins `15s`. Changing it
  to `1s` is a green suite and an application where every sign-in answers 503 —
  the same hole VS8-SEC recorded for the two rate limits, and it is now three
  variables wide.
- **`migrateLockTimeout` at 3s and `IdleInTransactionTimeout` at 60s.** Both
  are asserted through the constant, so both are self-consistent by
  construction and neither number is defended by anything. The derivations are
  in the comments; nothing measures them against a real deploy.
- **The gzip LEVEL.** `gzip.BestSpeed` is chosen from a measurement recorded in
  the file's comment; a leg asserting level 6 would pass, since every
  assertion is about `Content-Encoding` and round-tripping rather than about
  ratio or time.
- **`docs/CLIENT-PREREQUISITES.md` itself.** Every claim in it about the client
  was verified against `wipe/mock-data` with a path and a line, and **nothing
  re-checks them**. Line numbers move. The only check on that document that
  means anything is somebody who has not read this repository reading it and
  building against it.
- **The Vary header's effect on a real cache.** The leg asserts the header is
  set; nothing puts a cache in front of this server and watches it do the right
  thing. Caddy is still deferred.

### AND ONE THING R1 CANNOT DO WITHOUT BREAKING A GATE, RECORDED RATHER THAN WORKED AROUND

`scripts/check-plan.py` recomputes the sha256 and byte size of **this file** and
compares them against `plan.base.inputs`. R1's own file list says
`CLAUDE.md (EDIT, same commit — DEC-23)`. **Those two instructions cannot both
be satisfied**: the moment R1 writes its record, the stamp goes stale and the
plan gate exits 1.

```
$ python3 scripts/check-plan.py docs/plan-v7.json
FAIL: the CLAUDE.md stamp says 207684 bytes; the file is <this file>
FAIL: the CLAUDE.md stamp says 335a39cae66a…; the file is <this file>
2 failure(s); 66 ids; 23 routes; 8 steps; 14 deletions
exit=1
```

**THE ACTUAL FIGURES ARE DELIBERATELY NOT IN THAT BLOCK, and finding out why
took three attempts.** They are a length and a hash OF THE FILE THEY WOULD BE
WRITTEN IN, so quoting them changes them: each of the first two drafts of this
paragraph was stale the instant it was saved. A measurement of a document
cannot live inside that document — the one shape this project's "put the number
in the comment" rule cannot take. Run the two commands below to get them.

Green at `e4a3b94` (`0 failure(s); 66 ids; 23 routes; 8 steps; 14 deletions`,
exit 0), red from R1's documentation commit, and **those two lines are the only
failures** — nothing else in the plan check moved.

**The stamp was NOT updated, and that is the decision.** Its `status` field
reads *"RE-READ AND RE-STAMPED AT v7.2 … a plan that certifies a hash it did not
run is making a claim rather than a measurement"*. The stamp is a record that
the PLANNER read this file at that hash — so re-pointing it at a section the
planner has never seen would make the plan assert something false, which is
precisely the failure the mechanism exists to catch, and which the same status
field records this namespace committing three times already.

**What the next planner does with it:** re-read this section, then re-stamp with

```
wc -c < CLAUDE.md
shasum -a 256 CLAUDE.md
```

**And the structural fix, if this is going to happen at every step:** the stamp
belongs to the plan's INPUTS, and `CLAUDE.md` stopped being only an input the
moment a step was told to write to it. Either the stamp records the value at
plan time and the check tolerates a later hash on this one path, or CLAUDE.md
comes out of `base.inputs` and is stamped somewhere that expects it to move.
R2 through R8 each edit this file too, so it recurs seven more times.

