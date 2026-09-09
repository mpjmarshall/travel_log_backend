## Layout

```
cmd/api/main.go        the one binary
internal/              everything else (config, logging, httpx, auth, postgres,
                       logbook, rest, media)
migrations/            .up.sql / .down.sql, a PACKAGE (//go:embed cannot reach
                       outside its own directory), applied by internal/postgres
deploy/                Dockerfile, docker-compose.yml, .env.example
.dockerignore          AT THE ROOT, because the build context is the root
scripts/               slice-arc.sh — `make slice`, five phases, needs Docker
docs/                  EVIDENCE.md, the plan and the review records
```

Standard Go project layout, as go_backend.md L17 asks.

**`.dockerignore` is at the repository root and cannot move into `deploy/`.**
Compose builds the api with `context: ..`, so the context root is the
repository root, and Docker reads `.dockerignore` from the context root and
nowhere else — a copy beside the Dockerfile would be read by nothing, silently.
It was absent until VS1-FIXES; see that section for what `COPY . .` was picking
up.

**`docs/DIVERGENCES.md` has never existed, and that is the only half of this
paragraph still standing.** It used to say `docs/` and `README.md` did not
exist either; `README.md` landed at `de94ca1` and `docs/` now holds the plan,
the three review records, the handoff and — from VS8 — `EVIDENCE.md`. The
finding it was written for survives all of that: `deploy/Dockerfile` claimed a
divergence was "recorded in three places" and named two files that had never
been created, which is what the arc's `record` phase now checks on every run.

---

