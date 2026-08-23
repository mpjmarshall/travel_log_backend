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
# Recorded in three places by DEC-09: here, docs/DIVERGENCES.md, and README.md.
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

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=3s --retries=12 \
    CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
