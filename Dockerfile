# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Stage 1 — build
#
# NAMED DIVERGENCE 1 (DEC-09), from go_backend.md L31:
#   "Stage 1 must use `golang:1.22` for the build, and Stage 2 must copy the
#    statically linked binary into a minimal `scratch` image."
#
# Stage 2 is `scratch`, exactly as mandated. Stage 1 is golang:1.26 and not
# golang:1.22, for two reasons and the second one is decisive:
#   1. a 1.22 build image and a 1.26 development toolchain produce binaries
#      from different compilers, runtimes and std sources;
#   2. go.mod's directive is `go 1.25.0` (DEC-27), so a golang:1.22 stage
#      could not compile this module at all — it is not a preference.
# Recorded in TWO places: here, and CLAUDE.md's divergence table ("The spec,
# and the divergences from it", divergence 1).
#
# CORRECTION (VS1-FIXES). This line used to say "three places by DEC-09: here,
# docs/DIVERGENCES.md, and README.md". NEITHER OF THOSE FILES HAS EVER
# EXISTED — checked three ways rather than assumed: `ls docs` is absent;
# `git show f6705e6 --name-only` lists only CLAUDE.md, cmd/api/main.go and
# cmd/api/main_test.go; and `git log --all --diff-filter=A --name-only |
# grep -iE 'divergence|readme'` matches nothing in the whole history. They are
# not deferred either — CLAUDE.md's Layout block plans `docs/EVIDENCE.md`
# (VS8) and no other docs file. A reader chasing a divergence to a file that
# never existed cannot tell whether it was lost or never written, which is
# worse than a smaller claim honestly made.
# ---------------------------------------------------------------------------
FROM golang:1.26 AS build

WORKDIR /src

# go.sum does not exist until the first dependency lands (VS2). The glob
# tolerates its absence; go.mod always matches, so COPY still has a source.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 is what "statically linked" means here — scratch has no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---------------------------------------------------------------------------
# Stage 2 — runtime
#
# scratch lacks four things this binary needs, and each is supplied explicitly
# rather than inherited from a base image:
#   - the CA bundle, copied from stage 1 (outbound TLS has no roots without it);
#   - the timezone database, embedded in the binary by the blank import of
#     `time/tzdata` in cmd/api/main.go — there is no /usr/share/zoneinfo here;
#   - a non-root user. scratch has no /etc/passwd, so USER must be NUMERIC:
#     `USER nonroot` would fail to resolve at container start;
#   - a health probe. There is no shell and no curl, so HEALTHCHECK invokes the
#     binary's own -healthcheck flag. That flag exists for this line.
# ---------------------------------------------------------------------------
FROM scratch AS runtime

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/api /api

USER 65532:65532

# DOCUMENTATION ONLY, and it is not the port the server listens on. That is
# PORT, read by internal/config (VS2) and defaulted in the compose file; this
# line publishes nothing and configures nothing. If PORT moves, this and
# compose's container-side port move with it — but neither of them makes it
# happen. Kept because `docker inspect` and `docker run -P` read it.
EXPOSE 8080

# --timeout MUST STAY STRICTLY ABOVE THE PROBE'S OWN DEADLINE, and it did not.
# probe() gives itself context.WithTimeout(3s) (cmd/api/main.go) and this was
# --timeout=3s, so a probe that actually reached its own deadline raced
# Docker's kill: the health log recorded a killed check with empty output
# instead of the `healthcheck: Get ...: context deadline exceeded` line the
# code exists to print. The diagnostic could never win. 4s gives it 1s to land.
#
# The three budgets now nest, innermost first, and each must stay below the
# next: /healthz's own database ping 2s < probe's request 3s < this timeout 4s
# < the 5s interval.
#
# AND THERE IS A FOURTH THAT DOES NOT NEST WITH THEM (DEC-96, OPS-MAJ-4). The
# server does not listen until migrations finish, `migrateTimeout` allows 120s,
# and the health budget was 3s + 12x5s = 63s. MEASURED: `health=unhealthy` at
# t=61s while a CORRECT migration was still waiting for its lock and went on to
# apply at t=98s. So `make up --wait` fails a deploy that is working, and the
# natural operator response — Ctrl-C, `docker compose down` — interrupts a
# migration mid-flight, which is the one thing this project's forward-only
# runner cannot recover from by itself.
#
# `--start-period=150s` costs NOTHING, and that is the whole argument: a check
# that PASSES during the start period marks the container healthy at once, so
# the number is a ceiling on how long a slow boot is tolerated and never a
# delay. deploy/docker-compose.yml already says exactly that about Postgres.
# 150s is migrateTimeout (120s) plus the ping budget plus room, so the health
# check gives up strictly AFTER the thing it is waiting for has given up —
# which is the property the other three budgets have with each other.
#
# THE ALTERNATIVE IS NAMED AND NOT TAKEN: split the migration out of boot. It
# costs nothing to build — `make migrate` already exists and `cmd/api
# -migrate-only` already works — and a deploy would run migrations as a
# separate gated step and boot a server that only ever listens. It is declined
# because it changes the DEPLOY PROCEDURE rather than one line, and because
# `up --wait` reporting a healthy stack after one command is the property this
# project has been protecting since the arc was written. If that stops being
# true, this comment is where the reversal goes.
#
# THE PROBE TAKES NO PORT ARGUMENT, AND THAT IS DELIBERATE (VS2). It used to
# rely on a `-addr` flag defaulting to :8080 while the server's port was a
# separate knob — run the image with any other port and the container served
# correctly and was reported unhealthy forever, which `make up --wait` turns
# into a failed deploy. VS2 deleted the flag: the probe loads the same config
# the server does, so there is one source for the port and nothing left to
# pass. Do not reintroduce an address argument here.
HEALTHCHECK --interval=5s --timeout=4s --start-period=150s --retries=12 \
    CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
