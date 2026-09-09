## The stack

Two services, `deploy/docker-compose.yml`:

- **`api`** — built from `deploy/Dockerfile`, published on **127.0.0.1:8080**
  only. Local-only target (DEC-21).
- **`postgres`** — `postgres:17`, named volume **`pgdata`**.

**The named volume is the point, and a line in a YAML file is not evidence it
works.** `pgdata` is the only thing standing between `docker compose down && up`
and an empty log, and its failure stays invisible until a redeploy. VS8's arc
restarts the stack and reads the trip back; that is the proof, and nothing
earlier is.

**The database port IS published, on 127.0.0.1:5434.** Two decisions in that:

- **Published at all**, against the parent plan's S22 stance, because the
  `internal/store` tests run on the **host** and reach the database through
  `TEST_DATABASE_URL`. Loopback-bound; a deploy publishes nothing.
- **5434, after two attempts.** 5432 is a developer Postgres on this machine
  (`lsof -nP -iTCP:5432 -sTCP:LISTEN`). 5433 — the obvious next one, and what
  VS1 wrote first — is held by an **unrelated Docker container**
  (`slab-postgres`), which `lsof` attributes to `com.docker` and which no
  inspection of this repository would have revealed. It failed at
  `docker compose up` with `bind: address already in use`, not at review.
  **The lesson is not "use 5434"** — it is that a published host port is a fact
  about one machine, so `POSTGRES_PORT` in `deploy/.env` is the knob and the
  default is disposable.

**Service naming.** The database service is `postgres`, which is also the host
in `DATABASE_URL`. The parent plan's S22 calls it `db`. The slice plan's own
text says "api and postgres", and the slice governs this step — but the two
artefacts disagree and the re-plan must settle it rather than inherit both.

### `scratch` and its four missing things

Stage 2 is `scratch`, per L31. Everything a normal base image supplies has to
be put there explicitly:

- **CA bundle** — copied from stage 1. No roots, no outbound TLS.
- **Timezone database** — `_ "time/tzdata"` in `cmd/api/main.go`. There is no
  `/usr/share/zoneinfo`, and nothing fails on a developer's machine.
- **A non-root user, NUMERIC**: `USER 65532:65532`. `scratch` has no
  `/etc/passwd`, so `USER nonroot` would fail to resolve at container start.
- **A health probe.** No shell, no curl. `HEALTHCHECK` invokes the binary's own
  `-healthcheck` flag. **That flag exists for this line** — it is not a
  convenience, and removing it leaves the container with no way to report
  health at all.

---

