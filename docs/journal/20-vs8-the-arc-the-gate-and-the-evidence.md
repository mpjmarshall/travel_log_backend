## VS8 — the arc, the gate, and the evidence

**The slice is closed.** `make slice` runs the whole arc against the live stack
from a cold `docker compose down -v`, plus the four standing legs earlier steps
left explicitly for this one. `make check` is unchanged and still **4.4s**,
**462 legs and 16 skips** — VS8 added no Go legs, which is the right answer for
a step whose whole subject is the tiers `go test` cannot reach.

`make slice`: **exit 0, 76 assertions, 1m26s** on a warm image cache. The five
phases run cheapest-first, so a stale record fails in a second rather than
after a two-minute build: `record`, `gate`, `arc`, `testdb`, `healthcheck`.
Each is also a subcommand — `scripts/slice-arc.sh gate` — because a phase you
cannot run alone is a phase nobody debugs.

**`docs/EVIDENCE.md` exists**, which the plan has asked for since v1. It is the
mutation proofs, each run, at a stated commit, with the diff checked. Do not
restate its tables here: it is the file, and the reason this one has carried
four wrong counts is that a number lived in two places.

### The two `down`s are different commands and the script says so

`down -v` runs **once**, at the top: the volume goes, so the 201s that follow
are real creations rather than rows that were already there. `down` — no `-v` —
runs at the restart leg, and reading the trip back after it is the **only**
proof `pgdata` works. Swap the two and the restart leg passes while proving the
opposite.

**That is not hypothetical, and MU-A1 is the measurement.** Changing the mount
from `/var/lib/postgresql/data` to `/var/lib/postgresql/dataX` — which is
exactly what bumping `postgres:17` to `18` does, since 18 moved the image's
default `PGDATA` — left **every leg from A0 to A13 green**: register, sign in,
the write, the read, the 304, the 405, all of it. A15 answered **401**, because
the sessions table went with the trips. The latent trap the VS1 review flagged
and could not test is now caught, by the one leg that restarts the stack.

### The uppercased sign-in is the only thing in the arc that proves DEC-65

Two steps, and they prove different halves. **A5** registers the same address
UPPERCASED and expects **409** — that is the unique index on `lower(email)`
refusing it, not any Go code. **A6** signs in UPPERCASED and expects **201** —
that is `WHERE lower(email) = lower($1)` finding it. Lowercase either request
and both steps pass against a plain b-tree on `email`, so **the case is the
assertion**.

And the two mutations that look like one are not. `ON CONFLICT (lower(email))`
→ `ON CONFLICT (email)` reddens **A4**, not A5 — naming an index that does not
exist breaks the *first* register too, so it says nothing about the uppercase
one. Isolating A5 needs the schema and the statement moved **together**: a
plain unique index on `(email)` with `ON CONFLICT (email)`, under which both
registrations answer 201 and A5 is the only thing that notices.

### THE DEFECT RUNNING IT FOUND, AND IT WAS IN THE SCRIPT

VS7's lesson was that a green suite cannot tell a guard from a decoration. VS8's
is the same sentence one level up: **a green arc cannot either, and two of its
legs were decoration until they were broken on purpose.**

- **`curl -o` does not create or truncate its output file when the response has
  no body.** So `wc -c < body` after the 304 read the **previous** request's
  document — **333 bytes** — and the leg *passed*, because the number it wanted
  was 0 and it never got one. Had the handler started writing a body on a 304,
  this leg would have gone on passing. `req` removes both files before every
  request now, and `body_bytes` counts a file curl never created as zero. *An
  absence assertion is the easiest kind to write so that it cannot fail* — this
  project's own list, item 2 of the two smaller ones, arriving in a shell.
- **A JSON body written inline inside a quoted command substitution is not
  quoted.** `assert_eq 201 "$(req … -d "{\"email\":\"$X\",…}")" "…"` reaches the
  shell with the braces bare and **brace-expands**: curl ran twice with half an
  object each, the server answered `400 invalid_body` to both, and the failure
  was reported with the label `400` rather than the step's name. **The identical
  text as the right-hand side of an assignment parses correctly**, which is what
  made it look like a server defect for twenty minutes. `jq` builds every body
  now and every status is assigned before it is asserted on.

Neither is a defect in `cmd/` or `internal/`. Both would have made a leg pass
while proving the opposite, which is the only kind of test defect that matters.

### VS6's diagnosis of the build hang is WRONG, and here is the measurement

VS6 recorded `docker compose build api` stalling for ~15 minutes at
`resolve image config for docker-image://docker.io/docker/dockerfile:1` and
concluded that "egress to `registry-1.docker.io` did not answer here", leaving
"`docker compose build api` once, on a machine with Hub reachable" as the whole
of the fix. **Hub was reachable the whole time.** Measured at VS8 on the same
machine, while a `docker compose build` had been hung for ten minutes:

```
$ curl -o /dev/null -w '%{http_code} in %{time_total}s\n' https://registry-1.docker.io/v2/
401 in 0.168s                     # correct for an unauthenticated request

$ ps -ax | grep docker-credential
54821  10:23  docker-credential-desktop get      # hung for the whole build

$ echo 'https://index.docker.io/v1/' | docker-credential-desktop get
(no output)                       killed at 15s, exit 143

$ DOCKER_CONFIG=<copy of ~/.docker with credsStore deleted> docker compose build api
 Service api  Built                8.6s
```

The BuildKit frontend fetch **is** where it stalls, and the reason is the
credential helper Docker Desktop installs, not the network. `docker_preflight`
in `scripts/slice-arc.sh` probes the helper with a ten-second deadline before
the build, changes nothing when it answers, and prints all of the above plus a
live Hub timing when it does not — then continues against a `credsStore`-free
copy, because every image this project pulls is public. The helper answered
again half an hour later, so the condition is intermittent, which is the worst
kind to inherit a wrong explanation for.

### The four standing legs, and what each one is

All four were named in "What VS1-FIXES leaves for VS8" and all four are now
written. Two of them needed a shape nobody had specified.

- **G2, the gate's parse-error leg.** `make check` against `.tools/broken.go` in
  a **copy** of the repository: exit 2, "cannot PARSE". It is here rather than
  in a Makefile target because a test invoking the gate from inside the gate is
  circular, and that is why `Makefile` still reads `test_strategy: none`. G3
  keeps VS1's own misformatted-file mutation as the control, and under MU-G1 —
  the `ee543b9` recipe restored — the two branches separate exactly as
  VS1-FIXES said: **unparseable alone gives `make exit=0`, misformatted gives
  `make exit=2`.**
- **H1/H2, the healthcheck/TCP agreement leg, and it is DIFFERENTIAL by
  necessity.** The window on a normal cold start was measured at 0.33s against a
  3s interval, so a poll that simply never disagrees proves the poll is too slow
  just as well as it proves the fix. H1 therefore runs the **defect** — socket-only
  `pg_isready`, through a compose override, against a 15-second init script —
  and **fails if it does not disagree**: 16 samples, **10** with
  `docker=healthy` while TCP refused. H2 runs the shipped `-h 127.0.0.1` recipe
  under the same init: 17 samples, **0**. H3 is the budget leg VS4 asked for —
  every compose healthcheck's timeout below its interval, which is the rule the
  api image's own test enforces and the compose file shipped violating.
- **T1–T3, `make test-db`.** Under `POSTGRES_PORT=5999 POSTGRES_USER=alice
  POSTGRES_DB=otherdb` and its own project name, so it can never disturb
  `make up`. T1 and T2 are string comparison against `compose port postgres
  5432`; **T3 is what makes it evidence** — the URL the target prints opens a
  session and answers `alice@otherdb`.
- **R1–R3, the record checks, and they are labelled artefact tier.** R1 asserts
  every repo-relative path named in a **comment** exists, with an exemption list
  where the reason is written down — "the comment names it to say it never
  existed" is a reason, and `internal/rest` (DEC-74 renamed away), `internal/store`
  (VS5's predicted name), `deploy/.env` (a file that must NOT be in the tree)
  and `docs/DIVERGENCES.md` are the four. R2 asserts the `##` headings and
  `.PHONY` are the same set. R3 proves `make slice` propagates a non-zero exit
  **without recursing**, by running the target against two stub scripts through
  a `SLICE` variable.

**R1 found two stale citations on its first run and both were fixed rather than
exempted.** `internal/auth/bearer.go` called `internal/httpapi/middleware.go`
"the middleware" — a file that has never existed; `RequireTraveller` is in
`auth_handlers.go`. `internal/seed/seed.go` named `cmd/seed` as "the only entry
point" when DEC-75's command does not exist at all. And three places —
`deploy/docker-compose.yml`, `deploy/.env.example` and the `Makefile` — told a
developer to run `go test ./internal/store/...`, which matches no package in
this repository. That is VS1-FIXES finding 6 recurring three steps later, which
is the argument for the check rather than against it.

### And the seventh artefact check to go red against correct code

R2's first draft read the Makefile with `grep -oE '^## [a-z-]+'`, which matches
the **continuation** lines under a heading as well as the heading — it compared
`normally`, `without` and `three` against the target list and failed against a
perfectly good Makefile. The em dash is what separates a heading from its prose.
Phase 2 of the parent plan ran six of these; this is the seventh, and it landed
in the very step that exists to write them. **Write the artefact checks — they
catch a stale record cheaply — and expect the first draft of each one to be
wrong about the artefact rather than about the code.**

### What the inherited evidence turned out to be

Nine sections of this file carry mutation proofs. **Every one quotes real
output. Three state the commit they were run at; six do not.** That is recorded
as a finding rather than smoothed over — it is exactly what `docs/EVIDENCE.md`
exists to stop, and the inference "it was the step's own commit" is an
inference, not a statement.

Two hundred and forty-three mutations across five packages is not a step's work
to re-run, and re-running them without cause would be theatre. VS8 took a
**three-mutation sample** instead, one per tier that could regress silently, and
re-ran it at `cbb467a`. All three reddened with the text the record quotes: VS2's
`M-C` (the environment AST sweep), VS7's `L2` (the `.UTC()` survivor), and VS4's
`Q1` (the composite `ON DELETE SET NULL` blocker three review passes walked
past). The output is in `docs/EVIDENCE.md`.

### What VS8 leaves guarded by nothing

`docs/EVIDENCE.md` carries the whole list, in one place, for the first time —
including what VS8 moved **out** of it. The two entries that moved and are worth
naming here because they had stood since VS1: the named volume is now proved
**through the API** as well as by `test/image`, and `postgres:17` → `18` is not
asserted but **is caught**, because MU-A1 is that failure exactly.

New at VS8, and both are about this script rather than about the server:

- **The arc writes one trip and nothing else**, because that is all `PutTrip`
  can make. Every claim it proves about cities, places, photographs and walks is
  a claim about an empty list. `make seed` (DEC-75) is what changes that.
- **Nothing runs `make slice` but a human.** There is no CI, `make check` is
  deliberately fast and Docker-free, and the phases that need a daemon are
  opt-in. So the arc's evidence is only as fresh as the last time somebody ran
  it — which is the same tier `make test-image` has always been in, and it is
  worth saying out loud rather than letting a green file imply otherwise.

---

