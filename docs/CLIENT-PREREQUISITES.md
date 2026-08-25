# Client prerequisites

**What the Flutter app has to change before it can talk to this server, and why
each one is not optional.**

This document is written for somebody who has **not** read this repository. You
do not need to know what DEC-89 is; every item states the change, the reason,
and what breaks if it is skipped. The `DEC-` and `PD-` tags are here so a
reader who *does* want the full argument can find it in
`docs/rulings-v3-pending.json` and `docs/plan-v7.json`.

**Every claim about the client below was checked against
`/Users/mattmarshall/Documents/Flutter/Travel Log` on branch `wipe/mock-data`,
with the file and line quoted.** Where the plan said one thing and the code
said another, the code won and the divergence is written down. Line numbers
are what they were when this file was written; they move, and the surrounding
quote is what identifies the site.

**The one-line summary.** The phone stops being the record and becomes a cache.
PostgreSQL is the record. The phone reads the whole log in one conditional GET
before its first frame — exactly as it reads its file today, so
`logbookProvider` stays synchronous and no screen grows a loading state — and
every write goes to the server and waits for it. There are no offline writes
and no reconciliation.

Each step below is numbered. Steps 1 to 4 must land **before the first real
request**; the rest can land with the feature they belong to.

---

## 0. Already done — verify, do not redo

The fixture removal and the pinned clock are **already on `wipe/mock-data`**.
Listing them as work sends somebody to undo nothing.

- `lib/src/logbook/sample_log.dart` is deleted, and so is `assets/imagery/`.
  Verified: `git ls-tree -r HEAD --name-only | grep -i 'sample_log\|imagery'`
  matches nothing; commit `903de99` shows `sample_log.dart | 941 ---------` and
  four `assets/imagery/*.png` removed.
- The `sampleClock` override went with them. `lib/main.dart:52-59` records it:
  *"That override was the sample data's and never the app's … The fixture is
  gone, so it has gone: the app runs on `DateTime.now().toUtc()`."*

**One correction to carry.** The client's own `CLAUDE.md` says
`startupOverrides()` puts **four** overrides on the scope. It now installs
**two unconditionally and one conditionally** — `logbookSeedProvider`,
`logbookSourceProvider`, and `logbookStoreProvider` only when a documents
directory exists (`lib/main.dart:74-81`). Anything written against "four" is
stale.

**And one stale doc comment to leave alone or fix deliberately, not
accidentally.** `logbook_store.dart:45` still says `fresh` means *"A first run,
seeded with the fixture."* There is no fixture; `load()` returns `emptyLog` on
that path (`logbook_store.dart:133`). Do not transcribe that comment anywhere.

---

## 1. `logbookFormatVersion` becomes 2, before the first real request

**Today:** `lib/src/logbook/logbook_format.dart:19` reads
`const logbookFormatVersion = 1;`, and `decodeLogbook` compares with **exact
equality** at lines 44-49:

```dart
final version = decoded['version'];
if (version != logbookFormatVersion) {
  throw FormatException(
    'Log format $version, and this build reads $logbookFormatVersion.',
  );
}
```

**The server stamps `"version": 2` and can emit nothing else.** So as things
stand there is **no version at which the two can exchange a document**: the
client throws on 2, and asking for 1 through `X-Logbook-Format` answers `406`.

**Justified on the client's own terms, not to match the server.** `Photo.asset`
and the three `coverAsset` fields change MEANING — a bundled path before, an
opaque object id now — and the client's own rule, written at
`logbook_format.dart:14-18`, is that a *removed or retyped* key moves the
version while an added-and-defaulted one does not.

**The bump is cheap because the file stops being the record.** Under the cache
model (step 13) a cached document the client cannot read is DELETED and
RE-FETCHED, not quarantined. A version bump costs one refetch. Against a
file-as-record it would have cost the log.

**Also send `X-Logbook-Format: 2` on every request.** The server treats a
missing header as "the current version", so this is additive — but sending it
turns a future disagreement into a clean `406` naming what the server can
write, instead of a decode failure the client has to guess at.

## 2. Register does not sign you in

Two calls, not one.

```
POST /v1/auth/register   {email, passphrase}  -> 201, the traveller, NO token
POST /v1/auth/session    {email, passphrase}  -> 201, the opaque token
```

**The first screen after registering must render a traveller with NO NAME.**
`traveller.name` is null until `PATCH /v1/me` sets it. This is a state U1 can
already draw — the client's own comment says a missing `traveller` key "reads
as a log nobody has named yet".

**And the register/session responses must NOT be decoded with
`Traveller.fromJson`.** That generated codec is
`Traveller(name: json['name'] as String)` (`traveller.g.dart:10`) — a hard cast
that throws on null, against a field the server rules IS null until `PATCH
/v1/me`. The two responses are their own types. (Note also `traveller.dart:12`:
the client's `Traveller` has **no id field at all**, which matters if you want
to key anything on it.)

## 3. `.toUtc()` on every `DateTime` before encoding — and it changes the local file too

**This is a live bug today, not a precaution.** Measured against a running
server: `PUT /v1/trips/{id}` with `"start":"2027-10-12T00:00:00.000"` answers
`400 invalid_body`; the identical body with `...000Z` answers `200`. **T4's
"Add dates" therefore fails on every request.**

The path, verified end to end:

| where | what it does |
|---|---|
| `lib/src/ui/app_fields.dart:23` | `pickLogDate` is `showDatePicker`, which returns `DateTime(y,m,d)` in the **device's local zone** |
| `lib/src/screens/trip_screen.dart:89` | `_addDates` hands it to `setTripDates` |
| `lib/src/logbook/logbook.dart:490` | `setTripDates` is a pure pass-through |
| `lib/src/logbook/trip.g.dart:27` | encodes with `toIso8601String()`, which emits **no zone suffix** for a non-UTC value |

`grep -rn 'toUtc' lib/src/` answers **two lines**, both
`DateTime.now().toUtc()` in the clock, and **neither on a user-picked date**.

**The server refuses and will keep refusing.** Guessing at a zoneless string's
meaning is exactly the class of thing `logbookFormatVersion` exists to refuse:
a date that means one thing on the phone and another in the log is worse than a
write that did not happen.

**Say the local consequence out loud:** `.toUtc()` changes what `logbook.json`
holds, so this is a **local-format change as well as a wire one**.

**Two precision facts, so nobody "fixes" the wrong thing.** The wire contract
is **millisecond** precision (`2027-10-12T00:00:00.000Z`). Dart writes **six**
fractional digits whenever `microsecond != 0`, which `logbookNow()` always has
on the VM — so `Photo.filedLater` and a minted `Visit.at` can both carry six.
The server accepts six and **truncates** to three (it does not round: `.123999`
becomes `.123`). Truncate on the client before sending and the round trip is
byte-identical.

## 4. The status-branching rule — the half the server cannot do for the client

**The API layer branches on the HTTP STATUS as well as on the code.** A `404`
or `405` on a route the client's **own route table believes in** is a
DEPLOYMENT MISMATCH and must **never** map to the delete-succeeded branch. It
goes to the failure line.

**Why this is not made redundant by the new error code.** The server now
answers `unsupported_route` for a route it does not have (step 5's table), but
that does not help a client talking to a build that PREDATES it: **every server
older than R1 still answers `not_found`.** Ship the status rule first and read
the code as confirmation.

**Why it matters more than it sounds.** All three client deletes treat an
unknown id as SUCCESS, by decision:

```dart
if (log.photo(id) == null) return Future<bool>.value(true);   // logbook.dart:119
if (log.place(placeId) == null) return Future<bool>.value(true); // :156
if (log.trip(tripId) == null) return Future<bool>.value(true);   // :201
```

The obvious network mapping of that rule makes a delete against an undeployed
route **report success, delete nothing, and advance the cache**. And the copy
the user sees is `not_found_screen.dart:84`, `'That $what is not in your log'`
— the app telling somebody their trip is gone because the server is a version
behind.

**Corollary: a control must not ship ahead of its route.** `X-Logbook-Format`
is the cheapest way to tell.

**Note the asymmetry in the client's own contract, because it is real API
surface.** Deletes are idempotent-success; SETTERS are the other way —
`setWalkName` (`logbook.dart:660`): *"An id the log does not hold is a
failure … a set asks for a value the log then has to hold."*

---

## 5. The thirteen error codes, in four lanes

The body of every failure is `{"code":"…"}` and nothing else, except that a
`422` may add one key, `field`.

**Thirteen code rows and one row that is not a code — fourteen in all.** The
two halves of that sentence go together: pairing a *code* count with a *row*
count is how a number ends up wrong.

| code | status | lane | what the client does |
|---|---|---|---|
| `rate_limited` | 429 | **RETRY** | back off and retry; the user is told nothing |
| `timeout` | 503 | **RETRY** | honour `Retry-After` (the server sends `5`), then retry |
| *(transport failure — not a code)* | — | **RETRY** | no response at all: retry, then the failure line |
| `invalid_body` | 400 | REPORT | a client bug; the failure line, and log it |
| `invalid_field` | 422 | REPORT | a client bug; `field` is the only thing that says which |
| `conflict` | 409 | REPORT | the failure line |
| `payload_too_large` | 413 | REPORT | the failure line |
| `not_found` | 404 | REPORT | **only** when the route exists — see step 4 |
| `forbidden` | 403 | REPORT | the failure line |
| `internal` | 500 | REPORT | the failure line; **do not retry** |
| `upload_incomplete` | 409 | REPORT | the failure line |
| `unauthenticated` | 401 | **RE-AUTHENTICATE** | the credential is dead: the sign-in screen, *not* "your log could not be saved" |
| `unsupported_format` | 406 | **NEVER RETRY** | permanent against this server: "this needs a newer app" |
| `unsupported_route` | 404 | **NEVER RETRY** | permanent against this server: "this needs a newer server" |

**The fourth lane is the one a client gets wrong by retrying for ever.** The
plan's own text says "three lanes" and then names four; four is what the table
holds, and the two `unsupported_*` words share the fourth.

**`_commit` returning `bool` cannot carry any of this.** That type change is
client work and is named here rather than left to be discovered: four of the
thirteen need behaviour a boolean cannot express.

**There is no `deleted` code.** Anything that mentions one is stale —
`grep -rn '"deleted"' internal/` in this repository returns nothing.

## 6. The eighteen failure sentences, in three classes

**Eighteen sentences across ten screens.** Re-derived on `wipe/mock-data`
rather than transcribed — `grep -rn 'could not be saved' lib/src/screens/ |
grep -v '///' | wc -l` → 16, `'your log is unchanged'` → 2, `'stops working'`
→ 1: **nineteen sites across eleven files**, of which one is excluded, so the
target is eighteen across ten. Quote both halves of that sentence together.

**(i) Seventeen assert an OUTCOME a network write cannot guarantee.** They say
"nothing was saved" or "your log is unchanged", and over a network the honest
answer is that the outcome is unknown for an interval. The replacement is
retry-then-refresh, which is **tractable precisely because every write verb is
idempotent** — the server upserts on a client-minted key, so retrying is safe.

> `trip_screen.dart:263`; `edit_sheets.dart:383, 453, 514, 603`;
> `to_file_screen.dart:101, 102`; `add_city_screen.dart:136, 164`;
> `share_sheet_screen.dart:240`; `refile_photo_sheet.dart:197`;
> `edit_traveller_sheet.dart:190`; `profile_screen.dart:241`;
> `delete_sheets.dart:165, 366, 608`; `new_trip_screen.dart:164`

**(ii) One promises revocation that is not immediate.** `delete_sheets.dart:649`
says a stopped share stops working. Photographs are served through presigned
URLs with a public lifetime of fifteen minutes, so the honest sentence is:
stopping a share stops **new** links at once, and a photograph already loaded
may keep working for up to fifteen minutes.

**(iii) One needs no change, and is named as excluded so the count stays
checkable.** `settings_screen.dart:423` is about `preferences.json`, which
genuinely stays local.

## 7. `LogbookSource` maps onto four HTTP outcomes

The client distinguishes `fresh`, `restored`, `unreadable` and `unavailable`
(`logbook_store.dart:44-60`), and **five call sites read
`logbookSourceProvider`** — `trips_screen.dart:176`, `settings_screen.dart:317`,
`profile_screen.dart:206`, and `splash_screen.dart` at `:64`, `:100` and `:170`.
(The client's own record says "four screens"; splash reads it three times, so
count screens or count reads, but say which.)

The enum must survive the port. Once `logbookSeedProvider` is fed by an HTTP
read, **a failed response and an empty log produce the same five empty lists**,
and the natural implementation drops the enum and tells a user whose server is
unreachable to *begin* — about a record that exists and that they cannot see.
That is the exact failure the enum was invented to prevent, arriving from the
other side.

| HTTP outcome | `LogbookSource` |
|---|---|
| 200 with content | `restored` |
| 200 with an empty log and version 0 | `fresh` |
| a decode or format failure, including the 406 on `X-Logbook-Format` | `unreadable` |
| a transport failure — no response at all | `unavailable` |

**What this makes AVAILABLE, not required.** With the file as a cache rather
than a record, the quarantine, the sidecar, S1's notice and U2's sidecar export
line all become unnecessary. That is a simplification the client may take; it
is not a task.

## 8. `Trip.shared`, and H1's third state

**`shareLinkId` will come back `null` from the server, always.** Share tokens
are hashed at rest, so the server cannot emit the plaintext. **The client
minted the token and holds the only copy.**

**The consequence is bigger than "a restored device sees no link".** The write
response is a whole `Trip` the phone splices into its cached log, so an
ordinary **rename** — or "Add dates", or a T5 itinerary change — overwrites the
local token with null. Verified in the client:

- `Trip.isShared => shareLinkId != null` (`trip.dart:102`)
- Copy link: `onPressed: url == null || _busy ? null : () => _copy(url)` (`share_sheet_screen.dart:224`)
- Stop sharing: `onPressed: !trip.isShared || _busy ? null : _stop` (`share_sheet_screen.dart:228`)

So both controls go inert while the row is un-revoked and `GET /l/{token}`
still serves it: **the user loses the capability AND the only control that
revokes it, from an action that has nothing to do with sharing.** `Trip.withName`'s
own doc (`trip.dart:185-187`) states the invariant this breaks verbatim:
*"renaming a trip you have shared must not quietly kill the URL somebody is
holding."*

**Three changes:**

1. The emitted `Trip` now carries **`"shared": bool`**, derived from the
   server's `share_links` table. `Trip.isShared` becomes `shared` rather than
   `shareLinkId != null`.
2. **The splice must be a MERGE that preserves `shareLinkId`**, not a
   replacement. This is the one that is easy to miss and impossible to notice.
3. **H1 gains a third state it does not have today:** *shared, but this device
   does not hold the link.* "New link" is the way back, and H1 already offers
   it.

`shared` is additive with a default, so by the client's own rule it does **not**
move the format version. (It does move the server's ETag, which is a different
number and is handled for you.)

## 9. A 412 on the upload PUT means "already there", not "failed"

Photograph uploads go straight to object storage through a presigned URL, and
the URL's signature covers `If-None-Match: *`, which makes the key **write-once**.

**So a client re-uploading after an unacknowledged success gets `412
PreconditionFailed` — and that is SUCCESS.** Measured on MinIO: the first PUT
answers 200 and any second answers 412 with the original bytes intact. The
commit path treats 412 as success. Without this rule the retry story is a
regression: every recovered upload reports failure.

**Replay `uploadHeaders` verbatim.** The begin response carries a flat map of
exactly the headers the signature covers, already encoded. A presigned URL
whose signature covers extra headers is **unusable** unless the caller replays
each one exactly — a client handed only the URL gets 400 on every upload,
forever, with no way to derive the encoding itself.

## 10. Decimate the track before sending — and what happens if you do not

**The cap is 500 points.** The server refuses a longer `Walk.points` with a
`422` naming the field.

**The asymmetry has a cost the phone pays, and it is the reason this is a
prerequisite rather than a nicety: a user whose walk is refused has lost a
recording of a day.** A `List<LatLng>` is recorded once, on a day that has
passed, and cannot be re-recorded. The server refusing an over-long track is
not the same as the phone knowing to shorten one.

**There is no decimation in the client today.** Verified: nothing shortens
`Walk.points` anywhere; both copy constructors carry the full list
(`photo.dart:199`, `photo.dart:213`), and the one consumer draws all of it
(`city_map_screen.dart:234`). The field's own comment concedes the risk —
`logbook.dart:1130` calls it "the largest list in the log".

**C2 draws a polyline and does not need every fix**, so decimation costs the
picture nothing. The 422 is the backstop, not the mechanism.

For scale: 100 points is 5,226 bytes on the wire, 1,000 is 51,004, and 21,600 —
a six-hour walk at 1 Hz — is 1,099,390, which is **over** the 1 MiB body
ceiling. Uncapped, the user's longest walk is the one write that silently
cannot be saved.

## 11. `refilePhoto` gains two arguments

**Today** (`logbook.dart:425`):

```dart
Future<bool> refilePhoto(String photoId, String placeId) {
```

It mints the visit itself, via `_freshId` at `logbook.dart:444`. Against a
server the client still mints both — ids are the client's — but they have to be
**sent**, so the signature grows `visitId` and `visitAt`. One call site: M2.2,
`refile_photo_sheet.dart:89-91`.

This is a real change to one of the eighteen named methods on `LogbookNotifier`
and to `_freshId`'s role.

## 12. Platform secure storage is the seventh dependency

The session token is a **credential**, and the app's documents directory is
deliberately exposed to Files.app — so anything kept there is readable by
anyone holding the phone. Keychain on iOS, Keystore on Android, through one
package (`flutter_secure_storage`). `preferences.json` holds settings and never
the token.

**This gets the same conversation the first six dependencies had.** The
client's `pubspec.yaml:70-75` states that rule in the file itself.

**`dio` is NOT a separate conversation — it is already there.** Verified:
`dio: ^5.11.0` is a direct dependency, and `lib/src/api/dio_client.dart:33-47`
already builds a `keepAlive` client with 10-second connect/receive/send
timeouts and an `onDispose`. **So the API layer is not a blank sheet.** What is
missing is endpoints, auth, serialization and interceptors — not the client.

**One thing to know before the first request:**
`lib/src/api/dio_client.dart:23-27` throws on purpose:

```dart
String apiBaseUrl(Ref ref) => throw UnimplementedError(
  'apiBaseUrlProvider has no value: this project has no API yet. Override it '
  'in the root ProviderScope before anything reads dioProvider.',
);
```

`main.dart` does not override it today, so `dioProvider` throws on first read.
That override is step zero of the port.

*(`flutter_secure_storage` is absent: `grep -rn 'secure_storage' pubspec.yaml
pubspec.lock` returns nothing.)*

## 13. An `ImageProvider` keyed by OBJECT ID — required, and the decision is forced

Photographs are read through **short-lived signed URLs**. `NetworkImage`'s
equality is `(url, scale)`, so **a re-mint is a different cache key and
Flutter's `ImageCache` misses every single time**. The provider must be keyed
by the object id, with the URL as a detail it fetches.

**This has stopped being a cache-key optimisation and become the sentence that
is actually true.** On `wipe/mock-data` there are **five `AssetImage`
constructions and ZERO declared assets**:

```
lib/src/logbook/photo.dart:71     ImageProvider get image => AssetImage(asset);
lib/src/logbook/logbook.dart:1547 coverAsset == null ? null : AssetImage(coverAsset!);
lib/src/logbook/place.dart:83     …
lib/src/logbook/trip.dart:79      …
lib/src/logbook/city.dart:97      …
```

`pubspec.yaml`'s `flutter:` section declares fonts and **no `assets:` entry at
all** (the only `# assets:` line is the commented-out scaffold template). So
**every image in the app is the failure plate today**, and without this change
it stays that way. The five sites above are where the change lands.

The client's own record lists three answers to "where does the base URL come
from" — a parameter on the accessor, a screen-level helper, or a provider
mapping a locator to an `ImageProvider` — and calls the question open. **It is
now forced rather than deferred:** something has to choose, before any
photograph renders.

Batch the mints: `POST /v1/media/mint` takes a **list** of ids, so a
twelve-photo grid is one round trip rather than twelve.

## 14. The cache, and the three things logout clears

The file becomes a **cache**, not a record. Three consequences:

- A cached document the client cannot read is **deleted and re-fetched**, never
  quarantined. (This is what makes step 1's version bump cheap.)
- Store the ETag **inside** the cached document, not beside it. Discard the
  document and keep the tag and the server answers `304` with an empty body,
  and the phone has no log and no way to get one — permanently and silently.
- **Logout clears three things:** the cached log, the ETag, and the token in
  secure storage. Leaving any one behind hands the next person on the device
  either somebody's log or a live credential.

## 15. Your collision guard no longer means what its comment says

**Named, not a task.** `LogbookNotifier._freshId` (`logbook.dart:678-698`)
refuses an id the log already holds, and its doc is emphatic:

> *"A duplicate id is not a cosmetic failure: `Logbook.trip(id)` is a
> `firstWhere`, so the second entity is unreachable, photographs filed to it
> land on the first, and deleting either takes the other's content."*

Against a server that is the authority and a cache that can be stale, that
guard is **belt-and-braces rather than the guarantee it claims**.
`PUT /v1/trips/{id}` is an unconditional upsert with no exists-branch, so a
colliding id silently **overwrites** an existing trip rather than being
refused. At 31^12 a random collision is not the risk; a **stale cache** is.

**The upsert is right and is not changing.** Idempotency is what makes retrying
safe, and retrying safely is what makes the RETRY lane in step 5 tractable at
all. If this ever matters, the server-side answer is a `WHERE NOT EXISTS`
create/update split — and that is a conversation, not a patch.

---

## What the server does that you do not have to do anything about

Listed so nobody re-implements them.

- **`gzip`.** Send `Accept-Encoding: gzip` and the whole-log read compresses
  roughly 15x. `Vary: Accept-Encoding` is set on every answer. The ETag is
  **unchanged** by the encoding, so switching encodings does not re-download an
  unchanged log. Dart's `HttpClient` and `dio` both ask for gzip by default.
- **A 503 always carries `Retry-After: 5`,** whether it came from a request
  timeout or from the database being unreachable.
- **`{id, name}` is a safe body.** Every field on a write is optional and an
  absent field is **left alone** — so `renameTrip` can send exactly the two
  keys it owns. (This was not true before R1: a two-key rename used to empty
  the trip's whole itinerary and both its dates, answering 200.) An **empty
  list** is still heard as "empty": `"cityIds": []` clears the itinerary,
  omitting the key does not.
- **A field cannot currently be CLEARED over the wire.** `{"summary": null}`
  and omitting `summary` are the same request — this was measured, not assumed:
  Go's JSON decoder collapses an explicit `null` onto absence for the type the
  server uses. Nothing in the client can ask to clear one today. If a control
  ever needs to, say so and the server adds an explicit sentinel; do not
  discover it in production.

---

## Later steps append to this file

R2 through R8 each add to this document rather than starting another one. If
you are reading this after those steps, the sections above are still the
prerequisites; the additions are below them.

---

# Added at R3 — the three media routes, as they actually answer

Section 9 above described the upload flow before it was built. It is built now,
and everything in it still holds. What follows is the wire contract, measured
against the running container rather than described.

## R3.1 The three routes, and what each answers

```
POST /v1/media                  -> 201  {id, alreadyExists, uploadUrl?, expiresAt?, uploadHeaders?}
POST /v1/media/{id}/commit      -> 200  {id, byteSize, contentType, alreadyExists, uploadedAt}
POST /v1/media/mint             -> 200  {urls: [...]}
```

All three are authenticated and all three count against
`TRAVELLER_RATE_LIMIT_PER_MIN`, not the credential ceiling.

**`POST /v1/media` answers 201 on BOTH paths**, whether or not the object was
already there. It is an upsert on an id **you** minted, so it is idempotent by
construction — the same shape `PUT /v1/trips/{id}` has, which answers 200 for a
create and an update alike. `alreadyExists` is the one place that fact is
reported; do not branch on the status.

**The id is the sha256 of the bytes, in lowercase hex, and you compute it.**
The server never invents one. That is what makes a retry free and what makes
two photographs of identical bytes one object.

## R3.2 The three fields that are ABSENT when `alreadyExists` is true

`uploadUrl`, `expiresAt` and `uploadHeaders` are **omitted entirely** — not
null, not empty — from a begin response for an object whose bytes have already
landed. There is nothing to upload, and handing out a live write capability
against a committed address would be worse than a redundant one.

**So the branch is `if (body['uploadUrl'] == null) { skip to commit }`**, and
`alreadyExists` tells you the same thing. A client that reads `uploadUrl` as a
non-nullable String will throw on the second upload of the same photograph,
which is the ordinary case for a re-install.

**A begin that was never uploaded still answers `alreadyExists: false` and
still mints a URL.** The field is derived from whether the BYTES landed, never
from whether a row exists — so re-beginning a half-finished upload is a
retry, and it works.

## R3.3 `expiresAt` is a real deadline and it is short

It is the private lifetime, `S3_PRESIGN_TTL_PRIVATE`, which the deployment sets
to **two minutes**. It bounds when the PUT must **arrive**, not when it must
finish — so it is a window to start a 25 MiB upload in and not a budget to
complete one. Past it, begin again: a second begin for an uncommitted digest
mints a fresh URL and costs nothing.

## R3.4 Commit twice if you are not sure

**A second commit of an already-uploaded object is `200`, not `409`.** This is
the retry contract and it is the only thing standing between a lost response
and bytes you have uploaded and cannot attach. The bucket-and-database seam is
the one place in this API that can half-apply, and asking again is how you get
out of it.

`409 upload_incomplete` means the object is **not in the bucket** — you began
it and the PUT did not land. Upload and ask again. It does not mean the
request was wrong.

## R3.5 A reference waits for the BYTES, not for the row

`coverAsset` on `PUT /v1/trips/{id}` — and every asset reference in R6 and R7 —
is refused with `422 invalid_field` naming the field until the object has been
**committed**. Beginning it is not enough.

```
begin -> PUT the bytes -> commit -> reference
```

Out of order, the reference is a 422 and not a 500, and the `field` key tells
you which one.

## R3.6 Two media types, and `heic` is not one of them

The allowlist is **`image/jpeg` and `image/png`**, enforced in Go for the 422
and in the database schema as the guarantee. Anything else is
`422 invalid_field` on `contentType`.

**`image/heic` was in an earlier draft and is out.** Nothing in this system can
produce one — the shutter is inert by decision — and an allowlist entry nothing
can produce is a claim no test can check. It comes back with the real capture,
which is the same dependency conversation the shutter already needs. **If you
wire a real camera on iOS, this is the line to raise before you ship**: HEIC is
the platform default, and the server will refuse it.

**And the content type is SIGNED into the PUT.** So getting it wrong is not a
422 naming the field — it is `403 SignatureDoesNotMatch` from the bucket, which
looks identical to a tampered URL. That is the trade DEC-87 took, and
`uploadHeaders` is the mitigation: replay what you were given and never derive
it.

## R3.7 `byteSize` has a ceiling and the signature has an EXACT length

`MEDIA_MAX_BYTES` (the deployment sets 26,214,400 — 25 MiB) is refused with
`422 invalid_field` on `byteSize`, **before anything is signed**.

**The signature then pins the exact declared size, and it can never express a
range.** SigV4 signs a header VALUE. So a body one byte off what you declared
is `403 SignatureDoesNotMatch` from the bucket — the same answer as a wrong
content type, a tampered URL or a broken signer. Declare the length you are
actually going to send, and send exactly it. A chunked PUT with no
`Content-Length` at all answers `411 MissingContentLength`.

## R3.8 Minting is a batch, and there is no singular GET

`POST /v1/media/mint` takes `{"ids": [...]}` and answers `{"urls": [...]}`
**in the order you asked**, one per id. Pair by index. There is no
`GET /v1/media/{id}`: it would be this route with a one-element list.

- **At most 100 ids in one request**, else `422 invalid_field` on `ids`.
- An id nothing holds -> **`404 not_found`**, for the whole request.
- An id begun and not committed -> **`409 upload_incomplete`**, for the whole
  request.

Those two are deliberately different, and the difference is what you act on: a
404 is a reference that will never resolve and a 409 is one that will once the
upload finishes.

**The URLs are bearer capabilities.** Anyone holding one can fetch the bytes,
replay is unlimited, and they cannot be revoked before they expire. The
response carries `Cache-Control: no-store, private` and
`Referrer-Policy: no-referrer` for exactly that reason — do not put one in a
log, a crash report, or anywhere a `Referer` header could carry it.

**Every minted URL forces a download.** `response-content-disposition:
attachment` is inside the signature, so it cannot be stripped. That is
deliberate: it means a mislabelled object is downloaded rather than rendered on
the storage origin. It does not affect an `Image.network`-style fetch, which
reads the bytes rather than navigating to them.

---

# Added at R5 — the trip delete, the three share writes, the name, and the two things that can now sign you out

Six routes are live that were not. Everything above still holds; sections 8
(`Trip.shared`) and 4 (the status-branching rule) are the two you should re-read
before wiring any of this, because both of them are now load-bearing rather
than forward-looking.

## R5.1 The six routes, and what each answers

```
DELETE /v1/trips/{id}          -> 200 + THE WHOLE ENVELOPE + ETag
PUT    /v1/trips/{id}/share    -> 200 + a whole Trip + ETag
POST   /v1/trips/{id}/share    -> 201 + a whole Trip + ETag
DELETE /v1/trips/{id}/share    -> 200 + a whole Trip + ETag
PATCH  /v1/me                  -> 200 + {"name": "…"} + ETag
DELETE /v1/auth/session        -> 204, no body, no ETag
```

## R5.2 `DELETE /v1/trips/{id}` answers the WHOLE LOG, not a 204

**Do not splice this one.** Every other write answers a bare entity you patch
into your cached document; this one answers
`{"version": 2, "logbook": {…}}` — the same envelope `GET /v1/logbook`
gives — and **you replace your cache with it**.

The reason is that a cascade cannot be spliced. Deleting a trip removes rows
from five tables and clears a column on rows in a sixth:

| D3's own line | what the server does |
|---|---|
| "N photos and their notes" | every `Photo` with that `tripId` goes |
| "N recorded walks" | every `Walk` with that `tripId` goes |
| "N pins in …" — **kept** | `places` keeps every entry, minus that trip's visits |
| "The shared link stops working" | the link is on the trip, and the trip goes |
| (not itemised) | the trip's `cityIds` go with the trip; the cities do not |
| (not itemised) | **another trip's photograph that named a visit on this trip comes back with `visitId: null` and everything else unchanged** |

**A place left with no visits at all survives**, and that is deliberate: it is a
wishlist place, and "kept" is what the sheet promised. Measured against your own
`client_sample_log.json` seeded into PostgreSQL, deleting `autumn-crossing`:
photos 284 → 188, walks 2 → 1, visits 49 → 44, trip_cities 18 → 13,
share_links 1 → 0, **places 17 → 17**, **cities 12 → 12**, and `gamcheon` comes
back in the answer as `{"id":"gamcheon","cityId":"busan","name":"Gamcheon","visits":[]}`.

**Send it again and it answers 200 again, with the SAME ETag.** A delete of a
trip the server does not hold is a success — your `deleteTrip` already treats an
unknown id that way — and it moves no version, so a retry does not throw your
whole cache away. **Read section 4 again before you rely on that**: a `404` on
this route means the SERVER DOES NOT HAVE IT, not that the trip is gone.

**There is no confirmation field.** D3 makes the user type the trip's name; the
API asks for a bearer token and nothing else. That is decided, not overlooked —
the gate the sheet has is a gate on the human, and a body field would be a gate
on the client, i.e. on the software that already drew the sheet. **Keep the
sheet's gate.** It is the only one there is.

## R5.3 The three share writes, and the one that carries a token

**`PUT /v1/trips/{id}/share` writes only the flags you send.** Send one:

```json
{"shareCoordinates": true}
```

and `sharePhotos` and `shareNotes` are left exactly as they were. This matches
`setShareOptions`, which takes three `bool?` and is called with one set. An
empty body `{}` is legal and writes nothing.

**`POST /v1/trips/{id}/share` takes the token YOU mint.**

```json
{"token": "mnpqrstuvwxy"}
```

- **The server never mints it.** Tokens are hashed at rest (section 8), so the
  server can never hand you a plaintext on any later read. `newShareLinkId()`
  is the only thing in the system that produces one, and **you hold the only
  copy**.
- **The answer is the ONE response in the whole API that carries
  `shareLinkId`.** It is echoed, not recovered: it is the token you just sent.
  **Store it before you do anything else with the response.**
- **Twelve characters minimum**, `[a-z0-9]`, at most 64. Your generator makes
  twelve of a 31-character alphabet, which is 59.5 bits. A shorter one is
  `422 invalid_field` on `token` — a share token is a bearer capability and a
  short one is guessable.
- It **revokes whatever link was live** in the same transaction, which is what
  `newShareLink`'s own doc says it does. There is never more than one live link
  per trip; the database enforces that, not the handler.

**`DELETE /v1/trips/{id}/share` stops sharing AND resets the three switches** to
`true / true / false` — your own `Trip.defaultSharePhotos`,
`defaultShareNotes`, `defaultShareCoordinates`. This is not tidiness: leaving
`shareCoordinates` on after a link is killed means the **next** link hands out
exact pins without anybody having turned that on. `stopSharing` already does
this locally; the server does the same thing, so the two agree.

**All three are SETTERS: an unknown trip is `404 not_found`,** including the
one spelled `DELETE`. That is your own asymmetry — *"An unknown id is a failure
here, where it is a success for a delete. A delete asks for something to be
absent and an absent thing satisfies it; a set asks for a value the log then
has to hold."* Stopping sharing on a trip that is not there cannot answer a
Trip.

## R5.4 `PATCH /v1/me` is `setTravellerName`, and an empty name is refused

```json
{"name": "Matt"}   ->  200 {"name": "Matt"}
{"name": "   "}    ->  422 {"code":"invalid_field","field":"name"}
{}                 ->  422 {"code":"invalid_field","field":"name"}
```

The name is **trimmed server-side** as well as by your sheet, and **an empty
name is not a way to clear it** — exactly as `setTravellerName` decided: *"a log
with an owner keeps one, and 'no traveller' is a state a log arrives in and
never returns to"*. There is no way to clear a traveller's name over this API.

**It moves the ETag**, because the name is the sixth key of the emitted
document. Splice `{"name": …}` into your cached `traveller` slot.

**`GET /v1/me` does not exist and is not coming.** The name arrives inside
`GET /v1/logbook`. Asking for it twice is what section 4 of the private read
exists to refuse.

## R5.5 There is now a way to sign out — and a way to sign out everywhere

```
DELETE /v1/auth/session              -> 204   this token stops working
DELETE /v1/auth/session?scope=all    -> 204   every token this traveller holds
```

**You have no control that calls either.** That is recorded here rather than
being a reason not to build them: the route is what makes the control possible,
and a recovery that waits for a screen is a recovery nobody has during the week
they need it.

**Why `scope=all` matters more than it sounds.** A session lasts thirty days
and there is no refresh flow. If a token leaves the device, *"this token"* is
precisely the one the thief will not use — so the only recovery is revoking
them all. `?scope=all` **includes the token you called it with**: "sign out
everywhere" that leaves this device signed in has not done what it says. Expect
the very next request to be a 401 and go to the sign-in screen.

**A scope you have not spelled exactly is `422 invalid_field` on `scope`, not a
fallback.** `?scope=al` signing one device out while the user believes every
device is out is the one failure this parameter can have.

**Revoking does not move the ETag.** A session is not in the log, so nothing you
have cached is stale afterwards.

## R5.6 Registration is CLOSED after the first traveller

`POST /v1/auth/register` answers **409 `conflict`** once any traveller exists —
whatever address is sent. Two things follow for a sign-up screen:

- **A 409 no longer means "that address is taken".** It means the instance is
  in use. The two answers are byte-identical on purpose, so there is nothing to
  branch on: the honest copy is *"this log already has an owner — sign in"*.
- **A malformed address is still 422 naming the field**, on a closed instance
  as on an open one. 409 says stop trying; 422 says fix the body.

If the server was seeded with `make seed`, the account already exists and its
credentials were printed by that command. **Registering is not the way in; it is
the way in exactly once.**

## R5.7 Nothing in this step changed the log's shape

`logbookFormatVersion` is still 2, the emitted document still has the same six
keys, and the ETag's emitter half is still 2. What moved is the SURFACE, not the
format.

---

# Added at R6 — T5's city, C1's pin, and the ONE field where `[]` and absent are different requests

Three routes are live that were not. **Read R6.3 before you write any of it**:
it is the one place in this API where sending a key you did not mean to change
destroys somebody's record, and your generated `toJson()` sends it by default.

## R6.1 The three routes, and what each answers

```
PUT    /v1/cities/{id}                     -> 200 + a City       + ETag
PUT    /v1/cities/{id}   (with attachTo)   -> 200 + THE ENVELOPE + ETag
PUT    /v1/places/{id}                     -> 200 + a Place      + ETag
DELETE /v1/places/{id}?photos=keep|delete  -> 200 + THE ENVELOPE + ETag
```

## R6.2 `PUT /v1/cities/{id}` is `createCity`, and `attachTo` folds `setTripCities` into it

T5 drives two of your methods — `createCity` and `setTripCities` — and
`createCity` already takes `attachTo` and does both under one `_commit`. Send
that argument as a body field and the server does both in one transaction:

```json
{"name":"Kyoto","country":{"code":"JP","name":"Japan"},
 "centre":{"lat":35.0116,"lng":135.7681},"attachTo":"autumn-crossing"}
```

**WITH `attachTo` THE ANSWER IS THE WHOLE ENVELOPE — replace your cache with
it, do not splice.** Two entities moved: the city was created AND the trip's
`cityIds` grew, so a bare City would leave you re-deriving the itinerary from
your own copy of the rule. **Without it the answer is a bare City** and you
splice it as usual.

Branch on the shape you got, not on what you sent — `logbook` present means
envelope. The new id lands at the **END** of `cityIds`, exactly as
`t.withCities([...t.cityIds, id])` does.

**`country` comes from you and the server never derives it** (DEC-59). There is
no countries table and T5 has no country input, so this is the geocoder's
answer travelling through. `code` must be ISO-3166-1 alpha-2 — two capitals —
or it is a 422 naming `country`.

**On a CREATE, `name`, `country` and `centre` are all required**, and each is
refused by name. On an UPDATE every field is optional and an unsent one is left
exactly as it was: `{"name":"Kyōto"}` renames and touches nothing else.

**`attachTo` naming a trip you do not have is a 422 on `attachTo`, not a 404**,
and nothing is written — not even the city. Your own `createCity` treats it the
same way: it answers null without writing when `log.trip(attachTo) == null`.

**Sending it twice is safe.** A city already on the itinerary stays where it
is; it is not appended a second time and it is not moved to the end.

## R6.3 `visits` — ABSENT and `[]` are different requests, and one of them is refused

**This is the most important paragraph in this file.**

`PUT /v1/places/{id}` carries the WHOLE ORDERED visits array, newest first, and
the position in the array IS the order the server stores. Three bodies, three
different meanings:

| you send | the server does |
|---|---|
| no `visits` key at all | **leaves every occasion exactly where it is** |
| `"visits": [ … ]` | replaces the list with that one, in that order |
| `"visits": []` | **422 `invalid_field` on `visits`. Nothing is touched.** |

**SO OMIT THE KEY UNLESS YOU MEAN TO WRITE THE ARRAY**, and that is a change to
how you serialise a place. `Place.toJson()` writes
`'visits': instance.visits.map((e) => e.toJson()).toList()`, which is `[]` for a
wishlist place — so **C1's pin, serialised as a whole `Place`, is refused**, and
so is re-sending any of the nine wishlist places in your own sample log. Build
the body from the fields you are changing, the way `renameTrip` already sends
only `{id, name}`.

**Why `[]` is refused rather than obeyed.** An empty array is a request to clear
every occasion at that place, and clearing them unfiles every photograph filed
there: measured against your own log at `fushimi-inari`, that is **30
photographs across 3 trips**, and whole-log **95 photographs and 5 visit notes**.
No control in the app asks for it — there is no "forget every visit here"
sheet — so the route refuses it until one exists. A route offering a destruction
no sheet authorises is the same error as a sheet offering a choice the model
cannot make.

**A visits array that DROPS an occasion still holding photographs is also
refused**, on `visits`, and named. Dropping it would clear those photographs'
`visitId` while leaving their `placeId` standing — a place with no occasion,
which is a state your model has never held (across all 284 sample photographs:
95 carry both, 189 carry neither, place-only 0, visit-only 0). Dropping an
occasion nothing is filed to is allowed.

**Re-sending an unchanged array is a true no-op**, filings included. It is worth
knowing that this cost a blocker to get right: the obvious implementation
deletes and re-inserts, and re-inserting the same visit id does NOT restore
`photos.visitId`.

Each visit carries `{id, placeId, tripId, at, note}`. `placeId` may be omitted
or empty — the path carries it — but if present it must match, and `tripId` must
name a trip you have.

### NARROWED at DEC-109 — `[]` is refused only where it would destroy something

`visits: []` used to be a 422 always. It is now a 422 **only when the place
holds occasions**, and a no-op when it holds none.

This matters to you in one specific place: **nine of the seventeen places in
the sample log are wishlist places**, and the server emits `"visits": []` for
every one of them. Under the old rule the document the server gave you was not
one it would take back, and C1's pin — serialised by your generated
`toJson()` — was refused. Both now work.

**The advice below has not changed.** Omit the key when you mean leave-alone.
It is the clearer request, it is one character different from the destructive
one on the wire, and it never depends on what the server happens to hold. The
narrowing means a whole-entity PUT no longer fails on a pin; it does not mean
`[]` is a good way to say "don't touch this".


## R6.4 `PUT /v1/places/{id}` — C1's pin, and what a create needs

```json
{"cityId":"kyoto","name":"Tofuku-ji","coordinates":{"lat":34.976,"lng":135.774}}
```

**`coordinates` is REQUIRED on a create** and is not defaulted server-side.
`places.lat` and `places.lng` are NOT NULL, and you already resolve
`coordinates ?? city.centre` before you build the pin — so the server computing
it too would be a second implementation of one rule. On an UPDATE it is
optional like everything else.

**The answer always carries `"visits": []` and never `null`.** That is worth one
sentence because it is the shape you throw on: `place.g.dart` reads
`(json['visits'] as List<dynamic>)` with no null branch, and a wishlist pin has
no visits at all, so this is the ordinary create rather than an edge case.

`plan` and each visit's `note` are capped at **4,096 bytes** — this build's
policy, like a trip's summary. `coverAsset` must name a media object you have
already COMMITTED (R3.5), or it is a 422 on `coverAsset`.

## R6.5 `DELETE /v1/places/{id}` — `?photos` is REQUIRED and there is no default

D2 makes the user answer this on screen, so the API makes you answer it too:

```
DELETE /v1/places/tofuku-ji?photos=keep      the photographs stay
DELETE /v1/places/tofuku-ji?photos=delete    the photographs go
```

**Anything else — the parameter absent, misspelt, or in the wrong case — is a
422 `invalid_field` on `photos`, and nothing is removed.** Both refusals give
the same sentence deliberately: to you they are one condition, and two different
answers would suggest one of them has a safe fallback.

**It answers THE WHOLE ENVELOPE, not a 204.** Replace your cache with it. What
each branch does, measured against your own log at `fushimi-inari`:

| | `?photos=keep` | `?photos=delete` |
|---|---|---|
| the pin | gone | gone |
| its occasions | gone — "the visits go with the pin" | gone |
| the 30 photographs filed there | **kept**, `placeId` AND `visitId` both null | **deleted** |
| their date, city and **caption** | **untouched** | gone with them |
| the walks | **untouched** | **untouched** |

That is exactly `Photo.copyWith(clearPlace: true)` on the keep branch — both
columns, and nothing else. The captions are the row worth checking: your log
holds 2, and after a `?photos=delete` at `fushimi-inari` it holds 1, because the
sheet names "the notes you wrote on them" on the destructive branch only.

**Removing a place you do not have is a 200 and moves no ETag**, exactly as the
trip delete is — and on the destructive branch it takes no photograph with it,
because the whole thing is one transaction. **Read section 4 again**: a `404`
here means the SERVER DOES NOT HAVE THE ROUTE.

**There is no `DELETE /v1/cities/{id}`, and there will not be one until a sheet
says what it does.** The database refuses to delete a city that any place,
photograph, walk or itinerary points at. If you ever need one, the sheet copy is
written first.

## R6.6 Nothing in this step changed the log's shape

`logbookFormatVersion` is still 2, the emitted document still has the same six
keys, and the ETag's emitter half is still 2. What moved is the SURFACE.
