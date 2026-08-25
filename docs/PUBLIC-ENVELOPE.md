# The public envelope

**What `GET /l/{token}` answers, key for key and row for row.**

This document is a **specification the code is written against**, and it is
written **three steps before that code** on purpose (DEC-24). The value is that
it is drafted by somebody holding the three sharing flags and the schema and
**not** holding R8's handler: an allowlist derived from a handler is a
description of what the handler happens to emit, which is the one thing an
allowlist must not be.

**Written at R5. Implemented at R8. Nothing in R5 answers this route yet.**

Every number below was **run at R5's working tree** against the client's own
`internal/logbook/testdata/client_sample_log.json`, seeded into
postgres:17.11 — commands are beside the numbers.

---

## 1. The rule

**A key reaches the public envelope only if it is named in section 3.** Not
"unless it is sensitive" — a deny-list is a list somebody has to remember to
add to, and every field this project adds after today would be published by
default. The guard R8 writes is **structural**: parse the answer, walk it, and
assert the key set **at every level** *equals* the list in section 3.

That structural form is what lets a city's `centre` stay visible while a
place's `coordinates` do not. With `shareCoordinates` false the assertion
becomes *"the only `lat` in this document is under `cities[].centre`"*, which a
substring search cannot express — a `grep -c lat` would have to be zero, and
zero is wrong. It is a **path** claim, not a **word** claim, and only a walk
can make it.

---

## 2. What the reader is

**Nobody.** `/l/{token}` carries no `Authorization` header and resolves through
a **global** unique index on `share_links.token_hash`, because it arrives with
no traveller in hand. Three consequences shape everything below:

- **The reader cannot mint a media capability.** `POST /v1/media/mint` is
  authenticated. So every image the envelope shows has to be **embedded as a
  signed URL**, minted server-side at `S3_PRESIGN_TTL_PUBLIC` (15 minutes,
  DEC-84).
- **Fifteen minutes is a hard wall, not a rolling window.** It runs from the
  moment the envelope was generated, and there is no refresh path an
  unauthenticated reader can take. Whoever renders a share page must **re-GET
  `/l/{token}`** to refresh them.
- **The response is a capability**, so it wears DEC-51's headers:
  `Cache-Control: no-store, private` and `Referrer-Policy: no-referrer`.
  RFC 9111 §3.5's shared-cache prohibition protects `GET /v1/logbook` because
  that route carries an `Authorization` header; **it does not reach this one**,
  and a 200 with an ETag and no `Cache-Control` is heuristically cacheable by
  any intermediary — which would keep handing out live media capabilities after
  'Stop sharing' and unbound the window DEC-84 fixed at fifteen minutes.

---

## 3. The allowlist

`version` is the same number `GET /v1/logbook` stamps (`logbookFormatVersion`,
currently **2**), for the same reason: a reader refuses a shape it does not
know rather than guessing at it.

### Top level — exactly six keys

```
version   trip   cities   places   photos   walks
```

**`traveller` IS NOT ONE OF THEM.** The owner's name is not part of a trip, and
a share link is a capability over ONE TRIP rather than over a log. This is the
single largest difference from the private document and it is easy to
reintroduce by copying the emitter.

**`logbook` IS NOT ONE OF THEM EITHER.** The private read nests its five lists
under `{"version":…,"logbook":{…}}`. This document is a different shape — one
trip, no traveller, URLs where object ids were — so nesting it under the same
key would invite a reader to decode it with the logbook codec and get a
document whose `trips` list is missing.

**Every list is always present and always a list.** A nil slice marshals to
`null`, and `null` is neither an absent key nor an empty list — it is the one
shape a `List<dynamic>` cast throws on. `sharePhotos: false` makes `photos` an
**empty array**, never a missing key. (See §4 for why the array is empty rather
than the key absent.)

### `trip` — an object, exactly seven keys

```
id   name   cityIds   start   end   summary   coverUrl
```

- `start`, `end` and `summary` are nullable and are always present.
- **`coverUrl` REPLACES `coverAsset`.** The private document carries a 64-hex
  object id the phone mints a URL for; the public reader cannot mint, so the
  URL is embedded. `null` when the trip has no cover, or when `sharePhotos` is
  false (§4).
- **`shareLinkId`, `sharePhotos`, `shareNotes`, `shareCoordinates` and
  `shared` are NOT in the list.** They are the owner's settings and the
  reader's business is what they can see, not how it was decided. `shareLinkId`
  in particular would hand a reader the capability back in the body it is
  already reading, which is not a leak so much as a second copy of one; and
  after DEC-85 the server does not hold the plaintext to emit anyway.

### `cities[]` — exactly four keys

```
id   name   country   centre
```

- `country` is the nested `{code, name}` the private document uses.
- **`centre` STAYS EVEN WHEN `shareCoordinates` IS FALSE**, and this is the one
  place in the document where a coordinate survives that flag. A city centre is
  coarse — it is a city — and it is what a map opens on when there are no pins
  to fit. Removing it would leave a share page that cannot draw a map at all,
  which is a different product rather than a more private one.
- **`coverAsset`/`coverUrl` is NOT in the list.** A city's cover is chrome; the
  page has the trip's. Leaving it out is one fewer minted capability per
  request.

### `places[]` — exactly five keys

```
id   cityId   name   coordinates   days
```

- `coordinates` is `{lat, lng}` and is **removed entirely when
  `shareCoordinates` is false** — the key goes to `null`, not to a zeroed pair.
- **`visits` is NOT in the list; `days` is what replaces it.** A `Visit`
  carries a `tripId` and an `id`, and publishing a place's visits whole is
  precisely how another trip's history leaks (§5). The rename is a decision
  with a cost — a reader written against the private document has to learn one
  more name — and the cost is what it buys: `visits` in the private document
  means *every* visit of that place, and `visits` here would mean *this trip's
  visits only*, which is the same word for two different sets. A distinct name
  is what stops somebody restoring `tripId` and `id` "for symmetry" and
  reopening §5's leak in a change that looks like tidying.
- **`plan` is NOT in the list.** A plan is a note-to-self about a place you
  have not been to yet; it is not part of a trip that happened.
- **`coverAsset` is NOT in the list**, for the reason a city's is not.

### `places[].days[]` — exactly two keys

```
at   note
```

**This is what a `Visit` becomes.** Only the visits **on the shared trip**
appear here (§5), so `tripId` has nothing left to say and `id` names a row no
reader can address. Renaming the list is what stops somebody "restoring" the
two fields for symmetry with the private document.

- `note` is `null` when the visit has none, and is **stripped to `null` when
  `shareNotes` is false**.

### `photos[]` — exactly eight keys

```
id   cityId   placeId   takenAt   url   caption   coordinates   accuracyMetres
```

- **`url` REPLACES `asset`**, minted at the public TTL.
- `placeId` is nullable — a photograph filed to the city and not to a pin.
- `caption` is **stripped to `null` when `shareNotes` is false**.
- **`tripId` is NOT in the list.** Every photograph in this document is on the
  shared trip; a key whose value is the same on every row is a key that only
  tells a reader what they already know.
- **`visitId` is NOT in the list.** It names a row that is not in this document
  under that name (§ `days`).
- `coordinates` and `accuracyMetres` are in the list, nullable, and are
  **removed when `shareCoordinates` is false** — the plan's R8 item 9 names
  both by name, and this document follows it rather than narrowing it.

  **A RESERVATION IS RECORDED HERE RATHER THAN ACTED ON, because narrowing an
  allowlist quietly is the same mistake as widening one quietly.** A
  photograph's coordinate is not a pin: it is *where the person stood*, to
  metres, with a timestamp beside it, and 96 of them across a fortnight is a
  movement trace rather than a map of a trip. Measured on the fixture, 31 of
  autumn-crossing's 96 photographs carry one:

  ```
  SELECT count(*) FROM photos WHERE trip_id='autumn-crossing' AND lat IS NOT NULL;  -- 31
  ```

  H1's own copy for that switch is about *pins*. Whether one switch should
  govern both a place's pin and a photograph's trace is a **question for the
  human**, not a call for whoever writes R8: it is the difference between
  implementing a ruling and re-scoping it. If the answer is that they are two
  things, it is a fourth flag with its own sentence on H1 — which is client
  work — and this bullet is where the reason is already written down.

### `walks[]` — exactly five keys

```
id   cityId   recordedOn   distanceKm   points
```

- `points` is `[{lat, lng}, …]` and is **emptied to `[]` when
  `shareCoordinates` is false** — an empty array and not a missing key, and not
  `null`.
- **`name` is NOT in the list.** A walk's name is written by N1's 'Name it' and
  is a note in everything but the column it lives in.
- **`dismissed` is NOT in the list**, because a dismissed walk is not published
  at all (§5).
- **`tripId` is NOT in the list**, for the reason a photograph's is not.

---

## 4. What each flag removes

The three flags live on `trips` and are written by H1's three switches. They
default to **`true`, `true`, `false`** (migration 0002 — the client's own
defaults), and `stopSharing` writes those three values back explicitly.

| flag | false removes |
|---|---|
| `sharePhotos` | **`photos` becomes `[]`**, and `trip.coverUrl` becomes `null`. |
| `shareNotes` | **`places[].days[].note` → `null` AND `photos[].caption` → `null`.** Both. A note on a visit and a note on a photograph are the same promise to the user. |
| `shareCoordinates` | **`places[].coordinates` → `null`, `photos[].coordinates` → `null`, `photos[].accuracyMetres` → `null` AND `walks[].points` → `[]`.** `cities[].centre` STAYS. |

**`sharePhotos: false` MUST NOT MINT A URL AT ALL** — not mint one and withhold
it. A presigned URL is a live capability from the moment it is signed, whether
or not it reaches a response body; presigning is offline arithmetic, so
"minted and dropped" leaves no trace anywhere and is indistinguishable from
"never minted" in every log. The rule is about the *mint*, not the field.

**An empty `photos` array and an absent `photos` key are different documents,
and the array is the right one.** A reader that branches on presence has to
handle three states; a reader that branches on length has two. It is also
honest: the trip *has* photographs, and the owner has chosen not to publish
them — which is a different statement from "this shape has no photographs
key".

---

## 5. The three row rules

**Filtering by key is not enough, and this is the finding the whole section
exists for (PD-07).** An earlier draft filtered at the `places` level only. A
place accumulates visits across trips **by design** — that is what makes
'Third visit' and P1's year rows possible — so a place correctly published
because it is on the shared trip drags every other trip's history in with it.

**Measured at R5's working tree**, on the client's own fixture, sharing
`autumn-crossing`:

```
SELECT count(*), count(DISTINCT trip_id) FROM visits WHERE place_id='fushimi-inari';
-- 28 visits, across 4 trips
SELECT count(*) FROM visits WHERE place_id='fushimi-inari' AND trip_id='autumn-crossing';
-- 1
SELECT count(*) FROM visits
 WHERE place_id='fushimi-inari' AND trip_id<>'autumn-crossing' AND note IS NOT NULL;
-- 1
```

So `fushimi-inari` is published because of **one** visit and would carry
**twenty-seven** others, one of which has a note on it — and `shareNotes` is
`true` on that trip, so the note filter does not save it. A key-set walk passes.
A "no other trip's id appears" leg passes, once `tripId` is off the allowlist.

### The three rules

1. **`places` = the distinct places having a visit ON THE SHARED TRIP.**
   Not "every place in the trip's cities". Measured on the fixture: 5 places
   have an autumn-crossing visit; **8** places sit in autumn-crossing's five
   cities, so the city-scoped rule publishes **three wishlist pins the trip
   never went to**.

   ```
   SELECT count(DISTINCT place_id) FROM visits WHERE trip_id='autumn-crossing';   -- 5
   SELECT count(*) FROM places
    WHERE city_id IN (SELECT city_id FROM trip_cities WHERE trip_id='autumn-crossing');  -- 8
   ```

2. **`places[].days` = only that place's visits whose `trip_id` is the shared
   trip.** This is the rule the measurement above is about.

3. **`photos` and `walks` = `trip_id = <the shared trip>`.** Straightforward,
   and stated so the set of rules is complete rather than "the interesting one
   plus whatever".

**And `cities` = the trip's own `cityIds`, in `trip_cities.ordinal` order.**
Travel order is load-bearing on the wire and `ordinal` is the only thing that
carries it.

**A dismissed walk is not published.** `Walk.dismissed` is N1's 'Discard', and
publishing a track the owner discarded is publishing something they took an
action to be rid of.

### The leg the key-set walk cannot substitute for

> A wishlist place, and a second trip's place in the same city, appear in
> neither the `places` list nor anywhere else in the document.

Both cases are **rows**, not keys, and both are satisfiable by a document whose
key set is perfect.

---

## 6. Revoked, unknown, and the work between them

**A revoked token and a token nobody ever held answer the same 404 with the
same work done** (DEC-10, PD-12). Byte-identical is necessary and is *not*
sufficient: a handler that returns early on "no row" but, for a revoked row,
resolves the trip, reads three flags and mints a dozen URLs is byte-identical
and is still a clean oracle for *"this token was once real"* — which DEC-67's
revoke-and-keep design makes worth attacking, because every token ever issued
is still a row.

**So the lookup is the only branch**: select the row **regardless of
`revoked_at`** and decide afterwards, before anything else is read. The leg
counts store calls rather than timing them, in the shape
`internal/auth/service_test.go` already uses.

---

## 7. What this document does not decide

- **The rendered page.** There is none. `next_slice` says so, and this
  specifies the JSON only.
- **The limiter's number.** R8's own, and it is a **third** budget: per
  address, generous, for a route with no identity at all. Inheriting
  `AUTH_RATE_LIMIT_PER_MIN` — a credential ceiling of 10/min — from the same
  bucket instance as register and sign-in would mean one person browsing a
  shared trip locks everybody out of signing in. The honest sentence goes in
  the file with it: **this limit does not bind behind a proxy until
  `ClientKey` learns `X-Forwarded-For`**, and Caddy is deferred.
- **The path redaction.** `/l/{token}` puts a live capability in the request
  line. R8's `httpx.LoggedPath` rewrites it at **four** call sites — access
  log, recover, rate limit and `logFailure` — and the rate limiter's is the
  sharp one: it fires precisely when somebody is enumerating tokens, so
  without it the one line written during an attack is the line recording the
  capability being attacked.

---

## 8. Written at

R5, before a line of R8's handler existed. Re-derive the fixture numbers with
the queries above rather than trusting these; four counts in this project's
history have been wrong, and every one of them was carried rather than run.
