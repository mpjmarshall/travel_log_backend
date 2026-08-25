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
