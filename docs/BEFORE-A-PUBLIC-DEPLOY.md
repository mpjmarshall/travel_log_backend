# Before a public deploy

This repository's target is **local only** (DEC-21). Everything in it is
loopback-bound, every password is guessable on purpose, and the compose stack
is three services on one machine.

This file is what changes when that stops being true. It is not a checklist of
good practice — it is the list of things that are **deliberately wrong for a
public deploy and correct for this one**, each with the measurement or the
decision behind it, so that whoever deploys is choosing rather than
discovering.

**Nothing here is guarded by a test.** `analysis_options`-style exclusions do
not apply to Go, but `flutter test`'s equivalent does: `go test ./...` does not
read a compose file's `mem_limit`, `docker compose config` is not run by the
gate, and no leg in this repository asserts a byte of this document. It is the
third evidence tier — *guarded by nothing but a human with a device* — and it
must stay honest about that.

---

> **This file is the operational half of a public deploy, and it assumes one
> traveller.** The product decision to become a hosted multi-tenant service is
> in `HOSTED-MULTI-TENANT.md`, settled 29 August 2026. Everything below still
> applies — sizing, the bucket's absent lifecycle rules, restore ordering, the
> S3 secret, TLS — and three sections are named by that document as unchanged
> by it: §3 the tile source, §4 and §5 the bucket, §8 the proxy and the rate
> limiter's `RemoteAddr` key.

---


## 1. Sizing — the three numbers, and where they came from

Measured (DEC-95), on the running stack rather than estimated:

| service | steady working set | how it was reached |
|---|---|---|
| api | **140.5 MiB** | after ONE Argon2id hash. Idle is **12.42 MiB**, and it does **not** come back down — Go's scavenger does not return it promptly |
| minio | **214.7 MiB** | idle, doing nothing at all |
| postgres | **128 MB** | its own default `shared_buffers`, plus `work_mem` per sort per connection |

**A 1 GiB VPS is roughly half-spent before any data.** 140.5 + 214.7 + 128 is
483 MiB of steady state with nothing happening.

**Each +1 on `ARGON2_MAX_CONCURRENT` is +64 MiB of steady working set**, not of
peak. DEC-08's parameters are 64 MiB per hash and the memory does not come
back, so the api's ceiling is a function of that variable and nothing else.

**MinIO is the largest of the three**, which is the fact that makes `mem_limit`
worth setting at all: after R2 the OOM-killer's "whichever has the largest RSS"
picks MinIO, and unless somebody has said otherwise, that is the object store
holding the photographs.

The three ceilings in `deploy/docker-compose.yml` are **ceilings, not
reservations**, and they deliberately sum to more than a 1 GiB box has:

```
API_MEM_LIMIT=256m       # 140.5 measured + one more concurrent hash
POSTGRES_MEM_LIMIT=384m  # 128 MB shared_buffers + eight connections' work_mem
MINIO_MEM_LIMIT=512m     # 214.7 idle, and it grows with concurrent object I/O
```

Re-derive them rather than carrying them:

```bash
docker stats --no-stream --format '{{.Name}}\t{{.MemUsage}}'
```

---

## 2. The container nobody restarts, and the probe nobody reads

**`restart: always`, not `unless-stopped`** (DEC-95), and the difference is
invisible by inspection. Measured as a controlled pair: a container that dies
on its own reaches `restarting restarts=6` in 20s under both; one stopped from
**outside** sits at `exited restarts=0` indefinitely under `unless-stopped`,
and that decision persists across a daemon restart and a host reboot. So
`docker stop` to take a dump, then a reboot, leaves an unattended box
permanently down.

**Nothing consumes `unhealthy`** (OPS-MIN-13). Compose does not restart on it,
there is no watchdog, and no orchestrator is planned — so every healthcheck in
this project has exactly one real consumer, `--wait` at deploy time, and an
unhealthy container keeps taking traffic for ever. Restart-on-unhealthy is an
autoheal sidecar or a systemd timer. It is a deliberate seventh-service
conversation and **the first unattended box is its trigger**.

**Log rotation is set and was not** (DEC-95). `LogConfig.Config` was empty on
both containers with no daemon-level log-opts, so json-file ran uncapped:
**3,066,153 bytes over 21.35 idle hours = 3.4 MB/day = 1.26 GB/year**, of which
**15,151 of 15,960 lines (94.9%)** were the healthcheck talking to itself.
`max-size: 10m, max-file: 3` bounds each service at 30 MB.

---

## 3. The tile source, and OpenStreetMap's usage policy

Not this repository's — the **client's** — but it deploys with it.
`CLAUDE.md` in the client project records that tiles come from OSM's public
server with an identifying `User-Agent`, that their usage policy rules out
heavy use, and that this is **right for a personal build and not right for a
store release with real users**. It is one line to change, behind one provider.

---

## 4. What is deliberately absent from the bucket, and what that costs

Measured against the running MinIO at R2: versioning `""` (disabled),
`GetBucketLifecycle` → "The lifecycle configuration does not exist",
`GetBucketEncryption` → "The server side encryption configuration was not
found". All three are defaults and none is set by this project.

**Nothing reclaims an object** (OE-12). `media.Store` has no method that
deletes one, by decision: the sweep that would call it needs a liveness query
— an object is live iff some photograph's `asset` equals its id — and no step
before R7 can ask that. Two classes of orphan follow, both silent:

1. **A successful upload that is never committed.** `uploaded_at IS NULL` plus
   a real object, for ever. Every *dishonest* or partial upload leaves nothing
   — measured: a partial body over a raw socket, a zero-length body, a checksum
   mismatch and an oversized body all leave the object ABSENT — so this is the
   only upload-shaped orphan there is.
2. **A traveller who is deleted.** `media_objects_traveller_fk ON DELETE
   CASCADE` drops every row and leaves every object.

**The arithmetic is why it is deferred rather than fixed.** The whole fixture
is 1,084,768 bytes in **two** objects — all 284 photographs cycle two PNGs. At
that scale the orphan set is nothing. At ten thousand photographs it is not.

**The deferred answer, with its trigger:** a MinIO (or S3) lifecycle rule
expiring objects under a prefix after N days is the zero-code option. Its
trigger is the first deployment with more than one traveller, or the first time
the bucket's size is a number anybody looks at.

---

## 5. A database restore without a bucket restore is a silently broken log

**UPDATED AT R4: the database half now exists and has been restored once.**
`make backup` is a custom-format `pg_dump -Fc` taken inside the container, keep
7, and one restore rehearsal is recorded in `docs/EVIDENCE.md` with its output —
twelve content digests matching and a negative control that moved them. **The
bucket half is still DEF-07 and is still not built.**

**`media_objects` ROWS TRAVEL WITH A DUMP AND THE OBJECTS DO NOT.** Restore the
database alone and every media reference in the seeded log resolves perfectly —
`cities_cover_fk` and its siblings are satisfied by rows that came back —
pointing at bytes that are gone. There is no error anywhere: it is a foreign-key
graph that looks healthy and every photograph broken. Counted from the captured
fixture: **284 photographs, 6 trip covers, 9 city covers and 9 place covers —
308 references**, all addressing **two** objects.

```bash
python3 - <<'EOF'
import json
L = json.load(open('internal/logbook/testdata/client_sample_log.json'))['logbook']
print(len(L['photos']),
      sum(1 for t in L['trips'] if t.get('coverAsset')),
      sum(1 for c in L['cities'] if c.get('coverAsset')),
      sum(1 for p in L['places'] if p.get('coverAsset')))
EOF
# 284 6 9 9   -> 308 references, two objects
```

**THE ORDER IS FIXED, AND IT IS FIXED THE OTHER WAY ROUND FROM WHAT IS OBVIOUS
(DEC-92): BACK UP THE BUCKET FIRST AND THE DATABASE SECOND.** A dump newer than
the bucket copy references objects that were never copied — a silently broken
log. A bucket copy newer than the dump leaves objects nothing references, which
is unreferenced garbage and costs disk. The sentence is in the `make backup`
recipe as well as here, because that is where whoever adds the second half will
be reading.

**Three things `make backup` is NOT**, and each is a real gap rather than a
hedge: it is **not off-box** — it writes into `backups/` on the same machine as
the volume it protects; it is **not scheduled** — nothing runs it; and it does
**not include the bucket**. `docs/EVIDENCE.md` carries all three under "what is
still guarded by nothing".

**The bucket's other three absences** (creation — which R2 closed — lifecycle,
encryption at rest) are unchanged.

---

## 6. The S3 credentials are the first real secret in this project

Until R2 nothing here was worth stealing: the Postgres password is `travellog`,
guessable on purpose (DEC-21). `S3_ACCESS_KEY` and `S3_SECRET_KEY` on a deploy
are a key against a bucket holding the only copy of somebody's photographs.

- **`.dockerignore` now excludes patterns rather than two exact paths**, and it
  was verified rather than reasoned — six canaries (`.env.local`, `*.pem`,
  `*.key`, `secrets/`, `id_rsa`, `deploy/.env`) built into the build stage and
  every one absent, with `deploy/.env.example` still present.
- **A deploy uses a scoped key, not the root one.** compose feeds
  `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` into both MinIO and the API, which is
  right for one machine and wrong for anything else.
- **Rotating the secret invalidates every outstanding presigned URL.** That
  follows from SigV4 deriving the signing key from the secret; it is reasoned
  and has not been executed here. It is also the only lever besides deleting
  the object that revokes an already-minted URL.

---

## 7. `S3_PRESIGN_TTL_PUBLIC` is copy, not configuration

Fifteen minutes (DEC-84). `internal/config` refuses anything outside
**1s..168h**, which are minio-go's own limits and not a policy — outside them
every media route 500s at its first request.

**Raising it is a copy change.** Four client sentences are written against the
number, and the honest one is: *stopping a share stops new links at once, and a
photograph already loaded may keep working for up to fifteen minutes.* Set it
to a week and that sentence becomes false on a screen somebody is reading.

---

## 8. Caddy is still not in the stack

Deferred, not declined, and it has been deferred once before. It brings TLS,
the proxy hop, and `X-Forwarded-For` client resolution. Until it lands the rate
limiter keys on `RemoteAddr`, which is **correct for a direct connection and
wrong the moment a proxy appears** — so the public read's limiter does not bind
behind one. That is now a weakened limit rather than merely postponed TLS.

---

## 9. `GET /l/{token}` is the only unauthenticated route, and it ships at R8

Until now every sentence in this file was about a stack nobody outside the
machine could reach. That stops being true the first time somebody sends a
share link, because **this is the only route in the API that answers without a
bearer token**. Nine things follow, and each is a check a human with a device
performs — nothing below is guarded by a leg.

**UNTICKED, AND IT MUST STAY THAT WAY UNTIL SOMEBODY RUNS IT.**

- [ ] **The limiter binds.** `PUBLIC_RATE_LIMIT_PER_MIN` is 120 by default,
      per address, on its own bucket — and `httpx.ClientKey` keys on
      `RemoteAddr`, so **behind a proxy it is one bucket for the whole
      internet**. Twelve characters of `[a-z0-9]` is 59.5 bits, so enumeration
      is not the threat; an unmetered public route on a 1 GiB box is. Do §8
      first, or do not expose this route.
- [ ] **The number is not 120 because anybody measured it.** It is untuned,
      like the two ceilings beside it. Watch a real share page's request
      pattern before trusting it — a page that fetches an envelope and then 96
      images makes ONE request here, not 97, because the images go straight to
      the bucket.
- [ ] **Fifteen minutes is what the copy says.** §7 already carries it; what
      R8 adds is that there is **no refresh path** — the reader cannot mint,
      so the page must re-GET the envelope. If a share page is built and it
      does not re-GET, the number is a promise the product cannot keep.
- [ ] **The envelope is 61,503 bytes on the seeded log**, of which **68% is
      presigned URLs** (41,664 characters for 96 photographs, plus 434 for the
      cover). It is gzipped by the same middleware as every other route and no
      leg asserts that for this one. Check the wire.
- [ ] **`Cache-Control: no-store, private` and `Referrer-Policy: no-referrer`
      must survive whatever sits in front.** They are set from the route table,
      above the handler, so they are on the 404 too — but a proxy that strips
      or rewrites response headers reopens exactly what they close: a cached
      envelope keeps serving live media capabilities after 'Stop sharing'.
      Verify with `curl -sSi` through the proxy, not against the container.
- [ ] **The path must not reach any other log.** `httpx.LoggedPath` rewrites
      `/l/{anything}` to `/l/[redacted]` at five sites inside this process.
      **A reverse proxy's access log is not one of them.** Caddy logs the
      request URI by default; a share token in `access.log` is the same leak
      this repository spent a step closing. Configure that before the first
      link goes out.
- [ ] **And neither must a referrer.** `no-referrer` is what keeps the
      presigned URL out of the NEXT origin's logs when a share page links
      somewhere. It is inside the response this server writes; nothing checks
      that a browser honoured it.
- [ ] **Revoked and unknown must stay indistinguishable end to end.** They are
      the same body and the same header set from this server, and equal work
      inside it. A proxy that adds a timing difference, a WAF that answers
      differently for a "known bad" token, or a CDN that caches one and not the
      other reopens the oracle.
- [ ] **There is still no way to revoke an already-minted photograph URL.**
      Out of scope by decision and bounded by the presign lifetime. 'Stop
      sharing' stops NEW ones at once; §7's sentence is the honest one.

**And one thing this route does NOT change**: nothing here reclaims a media
object, and the bucket is still not backed up. §4 and §5 are unchanged and both
still apply.

---

## 10. The admin panel, and the four ways it contradicts this document

*Planned 30 August 2026. `ADMIN-PANEL.md` is the design; this section is what
it costs a public deploy.* **Nothing here is built yet** — this section exists
so that whoever deploys it is choosing rather than discovering, which is what
this whole file is for.

This document opens by saying the target is local only, everything is
loopback-bound, and every password is guessable on purpose. The panel is the
first thing in this repository that contradicts all three, and it does so at
`/admin` on **the same port as the API**. There is no separate listener to
firewall: wherever the API is reachable, the panel is reachable, and the only
thing between the internet and every traveller's account is one password.

**Four firsts, each of which this file has no other entry for.**

1. **The first credential that is not a traveller's.** `ADMIN_PASSWORD`, twelve
   characters minimum, refused at boot if shorter. It administers *every*
   account, so it is a different class of secret from §6's S3 keys: those let
   you read the bucket, this one lets you delete a person.

2. **The first cookie, and the first browser surface.** Nothing in this
   repository has ever sent `Set-Cookie`, and the API is JSON only. So the
   panel is also the first place a `Content-Security-Policy`, an
   `X-Frame-Options` and a `SameSite` attribute matter at all — and **`make
   check` cannot see any of them.** A CSP that is present and wrong passes every
   test in the plan. This is the same third evidence tier as §1's sizing and
   the iOS manifest keys: guarded by nothing but a human with a browser.

3. **The first thing that deletes from the bucket** — and it is worth saying
   why that is new. **Nothing in this codebase has ever deleted a media
   object.** `media.Store` has four methods and none of them removes anything,
   so a traveller deleted today by hand in `psql` leaves their bytes in storage
   permanently: unreferenced, unreachable, and still present after the account
   is gone. The panel's delete adds a fifth method and cleans up after itself,
   rows first so a storage failure leaves a recoverable orphan rather than a
   live photograph pointing at nothing.

   **It does not sweep the orphans that already exist.** Measured on the live
   stack on 30 August 2026: 4 objects, 5,175,532 bytes, and no code path that
   could ever have removed one. §4 and §5 are unchanged and both still apply.

4. **`ADMIN_PASSWORD` is the first variable in this stack with no safe
   default**, deliberately. Every other password here is guessable on purpose
   because the target is local; this one has no value in `.env.example` and
   none in compose, so a stack that has not been told a password **mounts no
   panel at all** and boots exactly as it does today. That is the failure mode
   chosen: absent rather than open.

**Before exposing it, three things this stack does not do for you:**

- [ ] **TLS.** The session cookie is `Secure`, which means the panel is unusable
      over plain http — by design. §8's proxy is not optional here the way it is
      for the API.
- [ ] **`RemoteAddr` behind that proxy.** The lockout counts failures per client
      key, and §8 already records that the rate limiter keys on `RemoteAddr`. A
      proxy that does not set the forwarded header collapses **every** admin
      login attempt onto one key, so one attacker locks the real operator out.
- [ ] **Recovery from that lockout.** The lockout and the sessions are both in
      memory, so the only way out is a restart of the API container — which
      also signs out every logged-in operator. Acceptable for one person, and
      worth knowing before it happens at an inconvenient moment.
