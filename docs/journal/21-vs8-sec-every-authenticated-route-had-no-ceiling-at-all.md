## VS8-SEC — every authenticated route had no ceiling at all

A security review found it, it is **built code rather than a plan defect**, and it
is one line of `Mount`:

```go
if route.Auth { handler = authed(handler) } else { handler = limited(handler) }
```

Rate limiting and authentication were applied as **either/or**. So the two
credential routes were limited and **the two authenticated routes were limited
by nothing whatever** — and the eighteen unbuilt routes of the parent plan would
each have arrived with the same hole, because the derivation in `routes.go` is
what decides the middleware.

### Measured against the running stack before the fix, at `7b47bee`

```
POST /v1/auth/session x15, one address    400 400 400 400 400 400 400 400 400 429 429 429 429 429 429
GET  /v1/logbook      x60, one token, -P 20        60 200
```

The credential ceiling bites after its burst; sixty concurrent authenticated
whole-log reads drew **no limiter at all**. Against DEC's thirty-day untuned
session TTL with **no revocation surface until R8**, that is unlimited whole-log
reads for a stolen token, and unlimited cascading deletes the day the write
routes land.

### The fix is composition, and the second budget is a second decision

`handler = authed(perTraveller(handler))`. Three calls in it, each against a
real alternative:

- **Two limiters, not one.** The credential limiter exists to bound an
  unauthenticated 64 MiB-per-attempt Argon2 surface, so `AUTH_RATE_LIMIT_PER_MIN`
  is deliberately 10. Wrapping the authenticated routes in *that* would give
  every route a ceiling — and `TestEveryRouteInTheTableIsRateLimited` would pass —
  while a phone syncing a log met a limit built for a password guesser.
  `TRAVELLER_RATE_LIMIT_PER_MIN` is the eighth configuration variable and defaults
  to 600.
- **Keyed on the traveller, not the address.** A stolen token used from a thousand
  addresses is one traveller and would otherwise be a thousand buckets; every
  traveller behind one NAT would otherwise be one bucket. Both directions are
  wrong and the second is the one users feel.
- **The limiter sits INSIDE the authentication, which is the opposite order from
  the one the fix was first written as.** The traveller id is on the context only
  after `RequireTraveller` has resolved the credential, so `limited(authed(h))` —
  the obvious spelling, and the one the review proposed — has nothing to key on.
  Two consequences follow and both are wanted: a flood from somebody holding no
  credential cannot spend a traveller's allowance (there is a leg), and the
  refusal is per-identity rather than per-socket. One consequence is **not**
  wanted and is stated rather than hidden: the session lookup happens *before* the
  ceiling, so what this bounds is the log read and the cascade, not the token
  lookup itself. Bounding that needs a general per-address ceiling on the whole
  API, which is a third budget and does not exist.

`httpx.RateLimitBy(l, log, keyName, key)` is the seam: `RateLimit` is now that
function with `ClientKey` and the name `client`. The key function belongs to the
caller because **`internal/httpx` imports no domain** and has never heard of a
traveller — the same line DEC-74 draws for the error vocabulary. A request the
key function cannot key is a **500, logged at ERROR**, not a shared bucket and
not a wave-through: the only way it fails is a middleware mounted outside the one
that supplies the fact, which is a wiring defect, and failing open there removes
the ceiling in exactly the case nobody notices. Same ruling DEC-48 already made
for a nil limiter, applied one layer down.

### THE SIXTH FIELD WAS PUT AND DECLINED, and the reason is not scope

`routes.go` said, in the source: *"The day an unauthenticated route arrives that
is not a credential attempt, this becomes a sixth field rather than a
derivation."* That day is the **public share read**, which is two steps away and
is not this step. A per-route ceiling added now is a field whose value is a pure
function of `Auth` on **every row of the table** — the exact shape `Mutating` is
in, which needs `TestMutatingAgreesWithTheMethod` to stop it becoming decoration.
And it would be the wrong field: what the share read needs is a **third** budget
— per address, generous, for a route with no identity at all — and a field that
can only say *credential* or *traveller* could not carry it.

The derivation is also **forced** today rather than merely convenient, which is
the half that was not written down before: a route with no traveller has no id to
count against, so `Auth` does not choose the key so much as exhaust it. The field
arrives with the row that cannot be derived, and it arrives holding three values.

### The red, before the fix existed

`internal/httpapi`, eight legs written first, six red on behaviour:

```
--- FAIL: TestEveryRouteInTheTableIsRateLimited
    GET /v1/logbook answered 6 requests in a minute at a ceiling of 3 without one 429.
    PUT /v1/trips/{id} answered 6 requests in a minute at a ceiling of 3 without one 429.
--- FAIL: TestAnAuthenticatedRouteRunsOutOfItsOwnAllowance
    the 4th authenticated request at a limit of 3 a minute = 200 {"version":2,…}, want 429.
--- FAIL: TestOneTravellerRunningOutDoesNotRefuseAnotherAtTheSameAddress
    the first traveller was served 4 of an allowance of 3
--- FAIL: TestOneTravellersTwoSessionsShareOneAllowance
    a second session for the SAME traveller = 200 {"version":2,…}, want 429.
--- FAIL: TestARefusedAuthenticatedRequestNeverReachesTheHandler
    a request past the allowance = 200 {"version":2,…}, want 429
--- FAIL: TestTheTravellerLimitLogsTheTravellerAndNeverTheToken
    the 2nd request at a limit of 1 = 200 {"version":2,…}, want 429
```

`internal/config`:

```
--- FAIL: TestLoadNamesEveryVariableWhenTheEnvironmentIsEmpty
    error does not name TRAVELLER_RATE_LIMIT_PER_MIN:
    config: 7 problems with the environment: …
--- FAIL: …/traveller_rate_limit_is_not_a_number
--- FAIL: …/traveller_rate_limit_is_zero
--- FAIL: TestComposeSetsEveryVariableTheConfigPackageReads
    deploy/docker-compose.yml does not set TRAVELLER_RATE_LIMIT_PER_MIN on the api service.
```

`internal/httpx`, the honest first red for a function that does not exist yet:

```
internal/httpx/ratelimit_test.go:246:20: undefined: httpx.RateLimitBy
FAIL	travellog/internal/httpx [build failed]
```

**Two of the sixteen new legs could not be red first and are labelled here rather
than left to look like the rest.** `TestTheTwoBudgetsAreNotOneBudget` and
`TestAnUnauthenticatedFloodDoesNotSpendTheTravellersAllowance` both **passed
vacuously against the defect** — with no limiter at all, no authenticated route is
refused and no allowance is spent. They are guards against the two wrong fixes
rather than against the defect, so their whole evidence is M4 and M5 below.

### THE LEG THAT DISTINGUISHES PER-TRAVELLER KEYING FROM PER-ANYTHING KEYING

This is the class the existing limiter legs are structurally blind to: **every
leg in this suite arrives from loopback**, so a limiter keyed on the address, on
a constant, or on the path passes the whole suite. It takes a pair, and neither
half is sufficient:

- `TestOneTravellerRunningOutDoesNotRefuseAnotherAtTheSameAddress` — two
  travellers, one address. Kills *keyed on the address* and *keyed on a constant*.
- `TestOneTravellersTwoSessionsShareOneAllowance` — one traveller, two live
  tokens, one budget. Kills *keyed on the bearer token*, which passes the first
  leg and is bought off with a second sign-in by anybody holding the passphrase.

**And the premise of the first is asserted rather than assumed.** The harness
records `httpx.ClientKey` for every request that reaches the chain and the leg
fails if the two travellers arrived from more than one address — because a leg
whose premise is an expectation about `httptest` stops meaning what it says the
day the harness changes. M10 is that assertion's own proof.

### The thirteen mutations, run at `7b47bee` + this working tree

Snapshotted and restored **by file copy**, with a sha256 before and after each
edit; the two that did not compile were rewritten until they did, because a
mutation that does not build proves nothing (M1 and M4 first went red as
`declared and not used`).

| | mutation | what reddened |
|---|---|---|
| M1 | the shipped defect restored: `authed(handler)` | 6 legs, the same six as the red above |
| M2 | key the authenticated budget on the **client address** | two-travellers-one-address; the log leg |
| M3 | key it on the **bearer token** | one-traveller-two-sessions; the log leg |
| M4 | authenticated routes wear the **credential** limiter | `TestTheTwoBudgetsAreNotOneBudget` + 4 |
| M5 | the limiter **outside** the authentication | 33 legs — every authenticated route 500s |
| M6 | `RateLimitBy` fails **open** on a request it cannot key | `TestRateLimitByRefusesARequestItCannotKey` |
| M7 | a refused request reaches the handler anyway | 4, including VS6's own DEC-48 leg |
| M8 | config stops reading `TRAVELLER_RATE_LIMIT_PER_MIN` | 3 config legs |
| M9 | the two ceilings wired from each other's variable | `TestTheTwoCeilingsComeFromTheirOwnVariables` |
| M10 | the harness stops recording addresses | two-travellers-one-address |
| M11 | `RateLimitBy` ignores the key function | 3 httpx legs |
| M12 | the refusal logged under a hardcoded name | `TestTheKeyNameIsWhatTheRefusalIsLoggedUnder` |
| M13 | `Mount` stops refusing a nil traveller limiter | `TestMountRefusesToWireAHalfBuiltAPI/no_traveller_rate_limiter` |

**M5 is the interesting one and its blast radius is the finding.** Putting the
limiter outside the authentication is not a subtle regression — it is 33 red legs,
because every authenticated request then arrives with no traveller on the context
and `RateLimitBy` answers 500. That is the fail-closed choice paying for itself:
the wrong order is impossible to ship quietly.

**M9 is the one nothing else could have caught.** Which variable feeds which
limiter is invisible from outside the process — a swapped pair gives the
credential routes a ceiling of 600 against a 64 MiB-per-attempt surface and gives
a phone a ceiling of 10, and every leg about status codes passes. `limiters()` in
`cmd/api/main.go` exists as its own function so that a test can spend from both.
`Mount`'s nil panic guards the *wiring*; this guards the *arithmetic*.

### The acceptance check, against the rebuilt stack

```
$ docker compose -f deploy/docker-compose.yml up -d --build
$ seq 700 | xargs -P 20 -I{} curl … /v1/logbook -H "Authorization: Bearer $TOK"
    619 200
     81 429
$ # a SECOND traveller, same address, while the first is out of allowance
    200
$ docker compose logs api | grep 'rate limited' | tail -1
{"level":"WARN","msg":"rate limited","traveller":"62726077-…","path":"/v1/logbook",…}
```

619 rather than 600 because the bucket refills while the run is in flight, which
is the token bucket working. The log line names the **traveller** and not the
address, which is what tells an operator which of the two ceilings fired.

### A claim in README.md that was false, and how it was measured

README said *"deploy/.env.example is the template, and a test asserts it lists
everything the config package reads."* **There was no such test** —
`grep -rn 'env.example' --include='*_test.go' .` matched three comments and
nothing executable. The claim was also untrue as written: `DATABASE_URL` and
`PORT` are read by `internal/config` and are deliberately **not** in the template,
because compose composes the first from the `POSTGRES_*` variables and pins the
second to the port the container publishes.

`internal/config/deploy_files_test.go` now asserts the two halves that are true —
compose's api service sets every variable the config package reads, and
`.env.example` documents every variable compose interpolates from the environment.
**Both are artefact tier and both are labelled as such**: they cannot fail because
the code is wrong. They were nonetheless each seen red for a real reason during
this step rather than by mutation — the first when the variable reached
`internal/config` and not compose, the second when it reached compose and not the
template.

### What VS8-SEC leaves guarded by nothing

- **The credential routes' own key is still `RemoteAddr`.** Unchanged, correct for
  a direct connection, wrong the moment Caddy appears — see "Inherited
  unfinished", which this step does not close and does not widen.
- **There is no ceiling on the token lookup itself.** The traveller limiter is
  inside the authentication by necessity, so an attacker with no credential still
  buys one session-table read per request on an authenticated path. What bounds
  that is a general per-address ceiling over the whole API — the third budget, and
  the one that also serves the public share read. It is the same step as the sixth
  field.
- **600 is untuned**, exactly as the Argon2 parameters and the pool sizes are. It
  was chosen against no client traffic: a phone syncing a whole log is one
  conditional read and a handful of writes, so the number is two orders of
  magnitude above the honest case by intent rather than by measurement. The first
  real client is what re-derives it.
- **Nothing tests the log line's *level*.** `TestTheTravellerLimitLogsTheTraveller…`
  asserts the traveller is named and the token is not; a refusal demoted to DEBUG
  would keep the suite green and lose the only signal an operator has that
  somebody's token is being used at machine speed.
- **No leg pins the DEFAULT ceilings** — 10 and 600 live in
  `deploy/docker-compose.yml` and `deploy/.env.example`, and the artefact legs
  assert the variables are *present*, never their values. Changing 600 to 6 is a
  green suite and an application that does not work.

---

