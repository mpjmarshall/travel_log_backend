## VS7 — one conditional read, one trip write, and the shape the client already decodes

**TEST-FIRST** (agent-graph-spec-V4 §6.7). Every leg below was written and
watched to fail before the code it names existed, and every one was reddened
again by its own mutation afterwards. Nothing here is reported as proven that
was not observed failing.

`make check` **exit 0**, **462 passing legs and 16 skips** — re-derived, not
remembered: `go test ./... -count=1 -v | grep -c -- '--- PASS'`, with
`TEST_DATABASE_URL` exported. VS6 left 363. The 16 skips are `test/image`,
unchanged.

**73 new top-level legs and 71 mutations.** Counts re-derived rather than
carried: `grep -c '^func Test' <file>` over `internal/httpx/mux_test.go` (5),
`internal/logbook/emit_test.go` (15), `internal/logbook/validate_test.go` (6),
`internal/postgres/logbook_store_test.go` (15),
`internal/httpapi/logbook_handlers_test.go` (25),
`internal/httpapi/routes_test.go` (5), plus two added to files that already
existed (`cmd/api/routes_test.go`).

### THE DEFECT RUNNING IT FOUND THAT NO LEG DID

The gate was green, 71 mutations had been run, and then the binary was pointed
at the real database on 127.0.0.1:5434. `PUT /v1/trips/kyoto` answered:

```
{"id":"kyoto","name":"Kyoto in May","cityIds":null,"start":"2027-05-12T00:00:00.000Z",…}
```

`null`, and `trip.g.dart` reads `(json['cityIds'] as List<dynamic>)` with no
null branch — so the client throws on the answer to its own write. **The GET
was correct the whole time**: `Emit` normalises nil slices, and the write path
answers a bare entity that never goes through it. One rule, two paths, and only
one implementation of it. `logbook.EmitTrip` is the rule as a named thing both
paths call.

**Two legs could not see it, and the second is the sharper lesson.** The splice
leg appends the returned trip to a document and re-emits, so `Emit` repaired it
on the way. And the *first draft of the new leg* sent `"cityIds":[]` in the
body — which decodes to an empty NON-NIL slice and marshals as `[]` with or
without the fix. Measured: it survived its own mutation. **A leg about a nil
slice has to OMIT the key**, because an empty JSON array and an absent one are
two different values on the Go side and only one of them is the bug.

The general form is the one this project keeps re-learning: a green suite
cannot tell a guard from a decoration, and neither can a suite that has never
been pointed at the real thing.

### What is in it

- **`internal/logbook`** — the domain. `types.go` (the wire shape and
  `Instant`), `emit.go` (the one emitter and the two version constants),
  `validate.go` (the compiled regexps and `TripWrite`), `store.go` (the
  contract `internal/postgres` satisfies).
- **`internal/postgres/logbook_store.go`** — the six queries one read is made
  of, and the whole-state upsert.
- **`internal/httpapi`** — `routes.go` (DEC-28's declared table) and
  `logbook_handlers.go` (the tag, the condition, the status).
- **`internal/httpx/mux.go`** — the two responses `http.ServeMux` writes for
  itself, brought inside the envelope.

### THE ROUND TRIP IS THE STRONGEST LEG, AND WHY

`TestTheClientsOwnLogRoundTripsThroughTheseTypes` decodes
`internal/logbook/testdata/client_sample_log.json` — the 85,422-byte document
the Flutter app's own encoder produced before its fixture was deleted (DEC-75)
— into these Go types, emits it back, and asserts the two are equal value for
value. Seven trips, twelve cities, seventeen places, 49 visits, 284
photographs, two walks. **The reference was not written beside the code that
has to satisfy it**, which is the whole of its strength: it proves every key,
every date string and every number at once, against bytes neither this package
nor its tests authored.

Everything below it — the golden key file, the per-field date legs — exists
because the round trip cannot say *which* thing broke. The golden is checked in
and a second leg asserts **the golden IS the client's key set**, so a golden
regenerated to match a mistake reddens too (mutation L11).

### The dates, and the six fields DEC-68 asks about

`Instant` renders `2027-12-06T07:05:00.000Z`. Dart's `toIso8601String()` writes
milliseconds unconditionally; Go's `time.Time` marshals RFC 3339 with trailing
zeroes REMOVED. Both parse, only one is byte-identical to what the client sent,
and the three `date` columns — `trips.started_on`, `trips.ended_on`,
`walks.recorded_on` — must come back as `T00:00:00.000Z` or `DateTime.parse`
hands the client a local time for those three and a UTC one for every other
date in the log.

**Two measurements came out of writing those legs.**

- **All 284 photographs in the client's log carry `"filedLater": null`.** So
  the sixth date-bearing field has no fixture, the leg that would have compared
  it says so with `t.Fatalf` rather than passing vacuously, and a synthesised
  leg covers it — written against the string a Dart encoder produces, not
  against what Go happens to do.
- **Mutation L2 — deleting `.UTC()` from `MarshalJSON` — left the whole package
  green.** Every leg built its `Instant` through `At`, which converts, so the
  legs proved `At`'s conversion and never the marshaller's. A store scanning a
  `timestamptz` may reach for the bare conversion, which `At` does not protect.
  `TestAnInstantBuiltByConversionIsStillRenderedInUTC` is the one leg that
  closes it, and L2 reddens it now.

**And one thing measured before relying on it:** `city.g.dart:18` reads a
coordinate as `(json['lat'] as num).toDouble()`, not `as double`. So a
whole-numbered latitude emitted by Go as `35` rather than `35.0` decodes
correctly, and `encoding/json`'s shortest round-tripping form is safe. A bare
`as double` would have made that a defect.

### The 304, and the shape of the interface it forced

`logbook.Store.Read` takes a **callback** rather than answering a document:

```go
Read(ctx, travellerID string, assemble func(version int64) bool) (Snapshot, error)
```

Two facts make that the only honest shape. The version and the document must
come out of ONE repeatable-read snapshot, or the phone stores a torn body under
a number describing a different moment; and the decision to assemble belongs to
the HANDLER, which is what holds `If-None-Match` and DEC-49's emitter version.
A two-call interface cannot keep both — the second call is a second snapshot —
and returning `(version, document)` always assembles. `Snapshot.Document` is
nil on the 304 path, so "the 304 does not assemble the document" is a fact
about the type rather than a claim.

**It is proven twice, at two tiers, and the store's proof needed its instrument
changed.** At the handler, an instrumented fake counts assemblies: three
revalidations after one 200 leave the count at one. At the store, the five
entity tables are **DROPPED** and the refused read still succeeds, with an
assembling read as the control. `pg_stat_all_tables` was the first attempt and
is the wrong instrument — its counters are collected asynchronously and cached
per transaction, so the leg would have been a flake pretending to be a
measurement. A table that is not there cannot be read from by accident.

### Decisions taken, each against a real alternative

- **A traveller at `logbook_version` 0 is served 200 with NO ETag.**
  `FormatETag` panics on a zero half deliberately, and `W/"1-0"` is precisely
  the one-half tag DEC-49's first half exists to prevent. `ETagMatches` then
  refuses every `If-None-Match` including `*`, which is the right answer: a 304
  against a log the client has never held hands it an empty body it reads as
  unchanged — DEC-49(b)'s permanently empty app, arriving by a third route. The
  alternative was a migration moving the column's DEFAULT to 1, which is one
  line and changes what the counter MEANS, and would redden VS6's own
  bump/no-bump legs. Declined.
- **A tag from another emitter does not revalidate**, and it has its own leg
  (`If-None-Match: W/"99-7"` against data version 7 answers 200). That is the
  slice's "bumping the emitter constant alone invalidates a cached client",
  expressed as a request rather than as arithmetic about a constant.
- **The 405 keeps its status and its `Allow` header, and its body says
  `not_found`.** Rewriting it to a 404 would make the status and the
  vocabulary agree at the cost of telling a client the path does not exist when
  the mux has just listed the methods it takes. A THIRTEENTH word
  (`method_not_allowed`) is refused by DEC-12: the block is closed, and a 405
  is a client that disagrees with the API rather than a condition a user can be
  told about.
- **The mux wrapper decides on the Content-Type, not on the status alone.**
  `http.Error` sets `text/plain; charset=utf-8` before `WriteHeader` and
  `WriteJSON` sets `application/json` before it, so by the time the wrapper is
  asked the two are distinguishable — and a handler's own 404 keeps DEC-12's
  `field`. Measured on Go 1.26.5: `GET /nope` → `404 text/plain "404 page not
  found\n"`; `POST /v1/logbook` → `405 text/plain "Method Not Allowed\n"` with
  `Allow: GET, HEAD`.
- **The path id wins and a disagreeing body id is 422 on `id`.** A body with no
  id is the ordinary case — the path already carries it, and that is what makes
  the route an upsert on a client-minted key (DEC-33). A body naming a
  DIFFERENT trip is a client that believes it is writing somewhere else, and
  honouring either half of that puts the write where nobody asked for it.
- **The write's existence checks are in the store, not in `ValidateTrip` and
  not left to the foreign keys.** Under the traveller's advisory lock the check
  is race-free, which is exactly what DEC-02 says the lock buys. Reading the
  violation back off the driver would mean importing `pgconn` for SQLSTATE
  23503, which `cmd/api`'s import sweep forbids (spec L20). Without them an
  unknown city is a 500 with nothing the client can show. **DEC-64 deleted the
  Go check that SUBSTITUTED for referential integrity; this is the one that
  names the field before the constraint fires, and the constraint is still what
  enforces it.**
- **`walks.points` is unnested in SQL, not decoded in Go.** The obvious answer
  — `json.Unmarshal` into `[]LatLng` — would make the store the SECOND non-test
  file importing `encoding/json`, which `internal/httpx`'s AST sweep asserts
  against (spec L19). `jsonb_array_elements … WITH ORDINALITY` answers the same
  question in SQL, and Postgres decoding its own jsonb is not payload encoding.
  It also keeps the ORDER explicit, where a Go decode would have inherited it
  silently.
- **Every list is `ORDER BY id`, and that is about determinism rather than
  display.** Two reads with no write between them must be byte-identical or the
  ETag is a claim the server cannot keep; the client sorts for display itself
  and always has. The two exceptions are the ordered lists the schema mandates:
  `trip_cities` by `ordinal` (DEC-64), and visits by `ordinal, id` — the second
  key so a pre-existing duplicate degrades to stable rather than random, because
  emitting visits in a different order silently rebinds a photograph to a
  different occasion (DEC-26).
- **The upsert does not NAME the three sharing columns**, in either the column
  list or the `SET` clause, so a create leaves them at their schema defaults and
  a rename leaves them exactly as they were (SF6). The type helps: `TripWrite`
  has no slot for them at all, and DEC-13 keeps unknown fields tolerated, so a
  client sending `shareCoordinates: true` is not refused — it is simply not
  heard.
- **The write's answer is RE-READ from the row.** The three flags are not in
  the body, so a response assembled from the input could only guess at them —
  and a response assembled from the input is a response that agrees with the
  client about a write the database may have shaped differently. Mutation H7 is
  the slice's own named proof, and it reddens.
- **`!Auth` is exactly DEC-48's rate-limited set, derived rather than declared
  as a sixth field.** Every route in this table that does not require a
  traveller is a credential attempt; `/healthz` is unauthenticated, unlimited,
  and deliberately not in the table, because a liveness probe is not part of the
  API. `TestOnlyTheUnauthenticatedRoutesAreRateLimited` makes the derivation a
  fact rather than a comment. The day an unauthenticated route arrives that is
  not a credential attempt, this becomes a field.
- **`Mount` panics on a nil store**, for the reason it already panicked on a
  nil limiter: nil does not read as "no logbook", it reads as working software
  until somebody asks for their log and gets a 500 out of the recover
  middleware.
- **A traveller who has gone is 401, not 500.** The row can be deleted between
  the credential being accepted and the query running; the honest report is
  that the credential is not live and the answer is to sign in again. A 500
  would have the client wait for a server that is perfectly well. Same argument
  VS6 made for `ErrNoSession`.
- **Two ceilings are this build's own policy and are marked as such.**
  `MaxNameBytes = 200` and `MaxSummaryBytes = 4096`. Nothing in the schema
  bounds either — both are `text` — so without them a one-megabyte trip name is
  storable and then re-emitted on every read of the whole log, for ever. They
  are constants so raising one is one constant and one leg, exactly as
  `auth.MinPassphraseBytes` is.

### THE TWO VERSION NUMBERS ARE NOT ONE NUMBER, and the step text reads as if they might be

`emitterVersion` is **1** and the document's `version` is **2**, and both are
right:

| | what it names | where it goes | when it moves |
|---|---|---|---|
| `logbook.EmitterVersion` | the CODE that rendered the bytes | the first half of the ETag, never the body | every deploy that changes what this package emits |
| `logbook.FormatVersion` | the WIRE's shape | the body's `version` key (DEC-40) | a coordinated release, negotiated by DEC-53 |

VS7's step text says `"version": 2` in one sentence and `emitterVersion starts
at 1` in the next. They are not in tension and a reader in a hurry will think
one of them is a typo. Written down here so nobody "corrects" either.

**`Emit` takes the format version as a PARAMETER** and refuses one it cannot
write rather than falling back to the one it can — a fallback is DEC-40's
refetch loop wearing a 200. `Formats()` is what the 406 names.

### Three mutations that survived, and what each one bought

- **H10 — deleting the handler's early format gate — SURVIVED, and the gate
  stays.** `Emit` refuses the version on its own and `writeLogbookFailure` maps
  that to the same 406 with the same header, so the two paths agree on every
  byte the client sees. What they do not agree on is the WORK: without the gate
  the read opens a snapshot and builds the whole document before refusing it.
  `TestA406NeverAssemblesTheDocument` is the leg that says so, and H10 reddens
  it now.
- **L2 and H19/L12** are above: both were survivors that named a real gap and
  both are closed by a leg written for them.
- **H2 — writing a JSON body on the 304 — SURVIVED, and it stays a survivor
  with a measurement.** `net/http` refuses to write a body for a 304
  (`bodyAllowedForStatus`), so the bytes never leave the process. Measured
  through `httptest.NewServer` and a real `http.Client`: the body is empty with
  the mutation applied. **So the empty-body half of
  `TestAMatchingIfNoneMatchAnswers304WithAnEmptyBody` is the stdlib's guarantee
  rather than this handler's**, and the leg is reddened by H1 and H4 instead,
  which are about the status and the tag. Say which half of an assertion is
  yours.

**One leg no mutation reddened, argued rather than left unmentioned:**
`TestLogbookStoreIsTheStoreTheDomainDeclared`, a compile-time interface
assertion whose failure mode is `LogbookStore does not implement
logbook.Store` — exit 1 out of the build. VS6 settled that this is the right
red for a claim about types. `set(all_legs) − set(reddened)` is that one name.

### The arc, run against the real database without rebuilding the image

`docker compose build` was out of scope for this step and the running `api`
container still predates VS6. So the binary was built and pointed at the real
PostgreSQL on 127.0.0.1:5434 directly. Everything below is output, not
description:

```
GET  /v1/nope                    404 application/json  {"code":"not_found"}
POST /v1/logbook                 405 application/json  {"code":"not_found"}
POST /v1/auth/register           201
POST /v1/auth/session            201  token I11QgC_6…
GET  /v1/logbook  (version 0)    200, NO ETag
   {"version":2,"logbook":{"trips":[],"cities":[],"places":[],"photos":[],"walks":[],"traveller":null}}
PUT  /v1/trips/kyoto             200  ETag: W/"1-1"    (body carried shareCoordinates:true)
   psql: share_photos|share_notes|share_coordinates -> f|f|f
GET  /v1/logbook                 200  ETag: W/"1-1"    cityIds:[] , dates as T00:00:00.000Z
GET  + If-None-Match: W/"1-1"    304, ETag: W/"1-1", NO BODY (curl created no output file at all)
GET  + If-None-Match: W/"99-7"   200
GET  + X-Logbook-Format: 3       406  X-Logbook-Format: 2  {"code":"unsupported_format"}
GET  with no credential          401  {"code":"unauthenticated"}
```

The `f|f|f` line is the acceptance check: **a PUT body carrying
`shareCoordinates: true` left the stored flag unchanged.**

### Divergences from VS7's file list, each deliberate

| the step said | what was built | why |
|---|---|---|
| `internal/api/routes.go`, `internal/api/*_handlers.go` | `internal/httpapi/…` | DEC-74 supersedes the `internal/rest` half of DEC-71, and `internal/api` is the name DEC-71 renamed away from |
| `internal/api/testdata/logbook_keys.golden` | `internal/logbook/testdata/logbook_keys.golden` | it is the emitter's shape and it is asserted against `client_sample_log.json`, which is already there |
| (nothing) | `internal/logbook/store.go` | DEC-62's contract has to live with the domain, and the callback shape is a decision the read forced |
| (nothing) | `internal/httpx/mux.go` | the unknown-route gap, ruled to this step |

### What VS7 leaves guarded by nothing

Unchanged from VS6 and repeated so the list does not shorten by silence: the
Dockerfile's CA bundle, `time/tzdata`, the numeric `USER`, the named volume,
and the fact that `deploy/.env.example` and `deploy/docker-compose.yml` are not
checked against `config.Load`'s variable list. New at VS7:

- **The four unimplemented lists have no round trip through storage.** The
  emitter is proven against the client's own document, and `LogbookStore.Read`
  is proven to bring back what `PutTrip` wrote — but PutTrip only writes TRIPS.
  Nothing has ever put a city, a place, a visit, a photograph or a walk into
  PostgreSQL and read it back through the emitter, so the six read queries'
  column ordering, the visits nesting and the `jsonb_array_elements` unnest are
  each guarded by their scan compiling and nothing else. `make seed` (DEC-75) is
  what closes this, and it is the leg to write first when it lands.
- **`ORDER BY id` is asserted for `trip_cities` and for nothing else.** Two
  reads with no write between them are proven byte-identical only for a log
  holding one trip, because that is all a trip write can make. A city list that
  came back in a different order twice would pass every leg here.
- **Nothing measures the 1 MiB body ceiling against a real payload.** VS3 left
  that open for "VS7's real payloads", and the largest body VS7 takes is still
  one trip — far under it. The whole-log READ is the big one and it has no
  ceiling at all.
- **The emitter version is a constant a human must remember to bump.** There is
  a leg asserting it appears in the tag and a leg asserting it is 1; there is
  nothing that notices when this package changes shape and the constant does
  not. That is DEC-49's stated design and it is worth naming as the risk it is.
- **The route table's `Mutating` flag is read by no lib code.** A leg keeps it
  agreeing with its verb, which is the most a declared-and-unused field can be
  guarded by.

### The routes are STILL not in the running container

*(Closed at VS8. Left standing because the note is what made the gap findable,
and because the reason VS6 gave for the build failing turned out to be wrong —
see VS8 below.)*

VS6's note stands and now covers four routes rather than two. `make check` is
green, the arc above ran against the real database through the real binary, but
**the image was not rebuilt in this session** — `docker compose build` was
explicitly out of scope — so `docker compose ps` shows an `api` container from
before VS6 and `GET /v1/logbook` against `127.0.0.1:8080` answers **404**, in
plain text, from a build that predates `httpx.MuxErrors`. VS8's arc needs that
build to succeed; VS6 recorded the BuildKit frontend fetch as the thing that
stops it.

> **CLOSED, VS8.** `scripts/slice-arc.sh arc` builds the image as step A1 and
> then asserts, at A13, the four answers only the running container can give —
> the mux's own 404 inside the JSON envelope, the 405 with its `Allow` header,
> the 401 with no credential, and the 406 naming the formats. A 404 in plain
> text at any of them means an image from before VS6, which is what that
> paragraph was warning about and is now a red rather than a note.

---

