## R8 — the public read, and the reckoning the last step owes

The eighth and last step of plan-v7. **One route**, and it is the only route in
this API that no bearer token stands in front of — so everything about it is a
decision about what a stranger holding a URL can see, and a defect in it is a
privacy leak rather than a bug.

**Seven commits and one section**, which is R1's reading of DEC-23 and for its
reason. What is here was written as the step ran.

### The route, and its three fields

```
GET /l/{token}   200 + the public envelope, or 404
                 no Authorization header, no ETag,
                 Cache-Control: no-store, private + Referrer-Policy: no-referrer
```

**Twenty-two rows in the table now, and twenty-three routes ship.** The two
numbers are not a disagreement and the difference is `GET /healthz`, which is
cmd/api's and deliberately not in `httpapi.Routes()` — a liveness probe is not
part of the API. Both are asserted, in the two places each is true:
`TestEveryRouteInTheTableReachesTheMux` over the slice, and cmd/api's
`TestTheShippedSurfaceIsTwentyThreeRoutesIncludingHealthz` over the mux the
server actually serves. Re-derive rather than remember, and **the grep is
anchored** because R6's unanchored one answered 22 against 21 rows by matching
the sentence that documented it:

```bash
grep -cE '^\t\t\{http\.Method' internal/httpapi/routes.go    # 22, + /healthz = 23
```

**All three of the row's fields differ from every other row, and not one of
them is derivable.**

| field | value | why nothing else could decide it |
|---|---|---|
| `Auth` | `false` | the traveller comes OUT of the token lookup, through a GLOBAL unique index on the digest (DEC-85). Every other route arrives with one already resolved. |
| `Limit` | `LimitPublic` | a THIRD budget and a third BUCKET (PD-09). |
| `NoStore` | `true` | the row the flag was added for. |

**`routes.go` predicted the limiter row in its own words** — *"the day an
unauthenticated route arrives that is not a credential attempt, this becomes a
sixth field rather than a derivation"* — and this is that day. Under the old
derivation from `Auth`, the public read would have inherited
`AUTH_RATE_LIMIT_PER_MIN` — a CREDENTIAL budget of 10/min — **from the same
bucket instance** as register and sign-in, so twelve reads of a shared trip
would lock everybody out of signing in. It is a third *instance* and not only a
third number, and the leg that holds it exhausts the public bucket and then
signs in.

**`NoStore` matters more here than anywhere.** Every other capability response
in this table carries an `Authorization` header, so RFC 9111 §3.5 forbids a
SHARED cache from storing it — which is what has been silently protecting
`GET /v1/logbook`. **Nothing reaches this one.** A 200 with an ETag and no
`Cache-Control` is heuristically cacheable by any intermediary, and a cached
envelope keeps handing out live media capabilities after 'Stop sharing' for as
long as it survives — which unbounds the very window DEC-84 fixed at fifteen
minutes.

**AND THERE IS NO ETag, WHICH IS A DECISION AND NOT AN OVERSIGHT.**
`GET /v1/logbook` carries one because the phone re-reads the whole log
constantly. Here the body embeds capabilities that expire in fifteen minutes,
so a validator inviting an intermediary to ask *"is this still fresh?"* is
inviting it to serve a cached envelope full of URLs — the exact thing
`no-store` on this row exists to prevent. A tag and a no-store are not a
contradiction to a correct cache; they are an invitation to a careless one.

### Three layers, three questions, and none of them answered twice

```
internal/postgres/share_read.go   WHICH ROWS   the three row rules, in SQL
internal/logbook/public.go        WHICH KEYS   the allowlist and the flags
internal/httpapi/public_handlers  WHAT HTTP    the status, the headers, the
                                               mint, and the two answers that
                                               have to be one answer
```

**The row rules are SQL and that is the finding rather than a layering
preference.** PD-07's leak is not a key that should have been dropped: **every
key in the leaking document is on the allowlist.** A place accumulates visits
across trips BY DESIGN — it is what makes 'Third visit' and P1's year rows
possible — so a place correctly published because it is on the shared trip
drags every other trip's history in with it. Measured on the client's own log:

| the rule | the measurement on `autumn-crossing` |
|---|---|
| `places` = the distinct places having a visit ON THIS TRIP | **5**, against **8** in its five cities — the city-scoped rule publishes three pins the trip never went to, one a wishlist with zero visits |
| `days` = only that place's visits whose `trip_id` is this trip | `fushimi-inari` holds **28** visits across **4** trips, exactly **1** on this one — and **1** of the other 27 carries a NOTE, on a trip whose `shareNotes` is `true`, so the note filter does not save it |
| `photos` and `walks` = `trip_id`, and `NOT dismissed` | **96** photographs; the only walk is published because nobody discarded it |

**The allowlist is separate TYPES and not the document's own with tags added.**
`omitempty`, a second tag set or a marshaller that dropped fields would all make
the public shape a FUNCTION of the private one: a field added to `Photo` next
month would be published by default, and the only thing between a stranger and
it would be whoever remembered. A field added to `Photo` cannot reach a
`PublicPhoto` at all — it has to be typed out in a file whose whole subject is
what a stranger may see.

**The allowlist is asserted THREE WAYS AGAINST EACH OTHER**: the response, the
golden file, and a map typed out of `docs/PUBLIC-ENVELOPE.md` §3. The third is
what makes a golden regenerated to match a mistake redden — **run**: add a
`plan` key to `PublicPlace`, regenerate both goldens with `UPDATE_GOLDEN=1`, and
`TestThePublicEnvelopeCarriesExactlyTheAllowlistAtEveryLevel` is still red.

**And the walk is STRUCTURAL rather than a substring search**, which is the
claim §1 of that document says a grep cannot make. With `shareCoordinates`
false the assertion is *"the only `lat` in this document is under
`cities[].centre`"* — a `grep -c lat` would have to be **zero**, and zero is
wrong, because the centre stays. It is a PATH claim and not a WORD claim.

### THE TOKEN IS NOT VALIDATED, AND THAT IS A MEASUREMENT

`logbook.ValidateShareMint` refuses anything but `^[a-z0-9]{12,64}$` — an
entropy floor on a capability the server is about to CREATE. Applying it to the
READ would refuse tokens this server has already issued: **the client's own
captured log carries `kyoto-9f2a`**, ten characters with a hyphen, which is what
`make seed` hashes into `share_links` and what the seeded fixture's only live
link IS. A validating handler answers 404 for a live capability, and a link
somebody has already sent to a friend stops working for a reason nothing
reports. A token that could not have been minted simply fails the digest
lookup, which is the same answer with no second rule to keep in step.

### REVOKED AND UNKNOWN: THE SAME BYTES **AND** THE SAME WORK

DEC-10 says *"the SAME 404 WITH THE SAME WORK DONE"* and v7.0 carried only
byte-identical. **Byte-identical is necessary and is not sufficient.** A
handler that returned early on "no row" but, for a revoked row, resolved the
trip, read three flags and minted a dozen URLs would answer identical bytes and
still be a clean oracle for *"this token was once real"* — through timing,
through database load, through the bucket's own signing. DEC-67's
revoke-and-keep design makes that worth attacking, because **every token ever
issued is still a row**.

So the lookup is the only branch: `ShareLink` selects **regardless of
`revoked_at`**, and the two failures are ONE condition and ONE `WriteError`
call. The leg **counts** store calls and mints rather than timing them, in the
shape `internal/auth/service_test.go` already uses — a timing assertion on a
shared runner is a flake and a call count is a fact — and it carries a positive
control, because two zeroes are equal too.

```
revoked  ->  1 lookup, 0 log reads, 0 mints
unknown  ->  1 lookup, 0 log reads, 0 mints
```

The arc asserts the other half in the container: the same body **and the same
header set**, compared minus `X-Request-Id` and `Date`, with the revoked ROW
still in the table.

### DEC-84's DELETED LEG IS RESTORED, AND IT ASSERTS THE CALL SITE

R2's TTL leg was deleted at v7.1 as OE-15 for being unfalsifiable — it compared
the two configured values **to each other**, so a handler reaching for the
private lifetime would have reddened nothing. **The ruling ordered a
replacement and the plan deleted the leg before it had read the ruling.** What
ships reads `X-Amz-Expires` back off the URL the signer produced:

```
every URL in the public envelope   X-Amz-Expires=900     (DEC-84, fifteen minutes)
the phone's own mint               X-Amz-Expires=120     (DEC-44, the revocation knob)
```

Mutation, run: `media.Public` → `media.Private` in `public_handlers.go` turns
900 into 120 and the leg reddens on the private number **by name**.

**FIFTEEN MINUTES IS A HARD WALL AND NOT A ROLLING WINDOW**, and this sentence
is the step's own: it runs from the moment the envelope was generated, the
reader holds no credential, and `POST /v1/media/mint` is authenticated — so
there is no refresh path an unauthenticated reader can take. **Whoever renders
a share page must re-GET `/l/{token}` to refresh them.**

### DEC-108, BUILT AS RULED

**One switch governs a place's pin and a photograph's coordinate both.** The
reasoning is the sheet rather than the schema: H1 says *'share coordinates'* and
the user reading it means all of them, and a control that silently governs less
than its label says is the same defect as D2's subtitle promising more than the
model could keep. The reservation stays a RECORD in `docs/PUBLIC-ENVELOPE.md`
rather than becoming a task, which is what the ruling asked for — and the
measurement it inherits is **31 of `autumn-crossing`'s 96 photographs carry a
coordinate**, which is a movement trace rather than a map of a trip.

The leg is at fixture scale, where that measurement was taken. With the switch
off the seeded document carries **5** coordinates — the five city centres — and
still carries all **96** photographs and all **5** places, because a filter that
empties the document satisfies every absence assertion ever written.

### THE CAPABILITY OUT OF THE LOG: FOUR SITES, AND IT IS FIVE

Measured on the running container at R8 entry, `curl /l/CAPABILITY7XY` wrote
`"path":"/l/CAPABILITY7XY"` to the api's stdout. `internal/logging`'s redactor
decides on the attribute KEY — `token`, `passphrase`, `authorization` — and the
key here is `path`, so it could never fire; adding `path` to that list would
blank every path in the file.

**The plan called it four sites and it is five.** `RateLimitBy` writes TWO log
lines: the WARN when a bucket is empty and an ERROR when a request cannot be
keyed at all. They are separate calls to the logger and a fix applied to one
leaves the other. **The rate limiter's line is the sharp one** — it fires
precisely when somebody is enumerating tokens, so without the redaction the one
line written during an attack is the line recording the capability being
attacked.

`httpx.LoggedPath` is a FUNCTION and not a middleware, and the reason is worth
carrying: rewriting `r.URL.Path` above the mux routes every share read to
nothing, and rewriting it below leaves the access log — which sits above —
printing the raw one.

**Every leg carries a positive control and both directions were run.** With the
provocation deleted and the control present the leg FAILS; with the control
removed as well it PASSES green against an empty buffer, which is exactly the
shape this repository has already caught twice. And a sixth site is guarded by
an AST walk rather than by a list: the second argument of
`slog.String("path", …)` in lib code is a call to `LoggedPath` and nothing else.

---

