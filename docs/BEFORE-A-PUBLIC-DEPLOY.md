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

The database backup is DEC-92's, and it is **not built yet** — there is no
`make backup` in the Makefile at R2. When it lands it is a custom-format
`pg_dump`, and the thing to know before it does: **`media_objects` rows travel
with a dump and the objects do not.** Restore the database alone and all 308
media references in the fixture resolve perfectly — `cities_cover_fk` and its
siblings are satisfied by rows that came back — pointing at bytes that are
gone. There is no error anywhere: it is 308 broken images and a foreign-key
graph that looks healthy.

**The bucket needs its own backup**, and it does not have one. It is named here
with the other three absences (creation — which R2 closed — lifecycle,
encryption at rest) because only the first was needed now.

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
