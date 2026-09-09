## VS1 — what was decided, measured and declined

**Decided**

- Module path is bare `travellog` (DEC-20), matching sibling GOMX's bare
  `gomx`. One binary, not a library anyone imports.
- `git init` inside `/Users/mattmarshall/Documents/Go/travellog`.
  `/Users/mattmarshall/Documents/Go/` is not a repository and GOMX is not one
  either, so there was nothing to nest inside.
- The database port is published on loopback (see above).
- `make migrate` and `make slice` fail loudly rather than exiting 0.

**Measured**

- Go **1.26.5** darwin/arm64, Docker **27.4.0**, Compose **2.31.0-desktop.2**.
- `go 1.25` vs `go 1.25.0`: the full result is in the section above. The
  headline is that VS1 **cannot** reproduce the failure, because it has no
  dependencies.
- Ports 5432 **and** 5433 are both occupied on this machine — 5432 by a
  developer Postgres, 5433 by an unrelated Docker container. The stack is
  published on 5434.
- `sha256 /Users/mattmarshall/Desktop/go_backend.md` matches
  `e546f63d…eebceb`, the value the spec constraints were verified against.

**Declined**

- **`git status --porcelain` on GOMX**, which the parent plan's S01 names as an
  acceptance check. **GOMX is not a git repository** — `git status` there
  answers `fatal: not a git repository`. The check is unimplementable as
  written. What VS1 did instead: nothing under
  `/Users/mattmarshall/Documents/Go/GOMX` was opened for writing, and the new
  repository is a sibling directory, not a parent.
- **A `.keep` file**, and **`os.Getenv` in the placeholder** — both above.
- **Publishing the database on 5432 or 5433** — both occupied.

**Honest statement about VS1's checks.** Two of the three are **artefact
checks** and cannot fail for a reason about code: `grep '^go ' go.mod`, and
`docker compose config` showing two services and a named volume. Only the third
— the stack coming up and `/healthz` answering — exercises anything. The step's
own text says as much, and it is repeated here so a later reader does not count
three checks where there is one.

---

