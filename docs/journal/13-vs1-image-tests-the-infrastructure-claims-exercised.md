## VS1-IMAGE-TESTS — the infrastructure claims, exercised

**Written 23 August 2026**, against the tree at `ee543b9`. Docker 27.4.0,
Compose 2.31.0-desktop.2, Go 1.26.5 darwin/arm64, daemon linux/arm64.
Eighteen legs in `test/image/`, **every one of them observed failing** before
it was recorded as guarding anything. Standard library only — `testing`,
`os/exec`, `net/http`, `archive/tar`, `encoding/json`. No dependency added.

This closes the list VS1-BACKFILL left open: the CA bundle, the embedded
timezone database, the numeric `USER` (and whether that user can read and
execute the binary), the HEALTHCHECK **wiring**, the named volume, and the
stack coming up at all.

### How to run it, and why `make check` did not change

```bash
make test-image        # 45s green, on a warm image cache
```

`make check` is still `go build ./...` → `go vet ./...` → `gofmt` → `go test
./...`, and still **2.8s**. Nothing here runs inside it: the tier is gated on
`TRAVELLOG_IMAGE_TESTS=1` **and** on a Docker daemon answering, in the shape
`internal/store` will use for `TEST_DATABASE_URL`. `go test ./...` on a machine
with no Docker runs the package and skips every Docker leg — **0.5s**, green.

**A silent skip is a pass that lies, and the mechanism for not lying was
measured rather than assumed.** Measured: under a plain `go test ./...` a
package whose tests all pass or skip prints exactly one line, `ok <pkg> 0.5s`.
`t.Skip`'s message, `t.Log`, and anything `TestMain` writes to stdout **or**
stderr are all suppressed — they surface only under `-v` or when the package
fails. So the reason is written twice: through `t.Skip` (what `-v` and
test2json see) and to **`/dev/tty`**, which `go test` does not own and cannot
capture. With no controlling terminal the second write fails and is dropped,
which is why it is not the only one.

Two legs guard that, and they are the only two in the file that need no
Docker — deliberately, because "the machine with no Docker is told" is the one
claim a developer with no Docker can still break.

### Two compose projects, on their own ports, by decision

The tier runs under `-p travellog-imagetest` and `-p travellog-imagetest-vol`,
on host ports 18080/15434/15435. `-p` beats the `name: travellog` in the
compose file, so the volumes it creates and destroys are
`travellog-imagetest*_pgdata`. **A test that cannot run beside `make up` is a
test nobody runs**, and one that could `down -v` a developer's own `pgdata` is
worse than no test at all. The volume leg gets its own project because it must
call `down`, and a shared stack cannot be pulled out from under the other legs.

### The probe, and why the image is layered rather than exec'd into

`scratch` cannot be inspected from the inside: no shell, no `ls`, nothing to
exec but `/api`, which takes no argument that would report any of this. So a
~60-line Go program is cross-compiled for the daemon's platform (`GOOS=linux`,
`GOARCH` read from `docker version --format {{.Server.Arch}}`, `CGO_ENABLED=0`
— the first run produced `exec /probe: exec format error` from getting that
wrong), layered onto **the image under test** with a two-line Dockerfile, and
run. It inherits that image's filesystem and its `USER`, so what it reports is
a fact about the real image.

**It is a string constant in the test, not a package.** A second `main` package
in this module would make `go build ./...` — the literal first command of the
gate — drop a second binary in the working directory every run, which is the
thing `/api` already has a paragraph in `.gitignore` about.

### The tzdata measurement VS1-BACKFILL could not make

The backfill declined a tzdata leg **on a measurement**, and that was the right
call: on macOS, a program with and without `_ "time/tzdata"` gave four
identical answers, because the embedded database is consulted only after every
filesystem source fails and macOS has two. It wrote the measurement down and
recorded `test_strategy: "none"`.

Inside `scratch` the filesystem sources are gone, and the same experiment
separates. Same program, same image, one import different:

```
--- probe WITHOUT _ "time/tzdata", inside the runtime image ---
tokyo=err:unknown time zone Asia/Tokyo
zoneinfodir=missing
--- probe WITH _ "time/tzdata", inside the runtime image ---
tokyo=ok
zoneinfodir=missing
```

Both halves are legs. The **negative** one is not decoration: if a base image
ever supplied `/usr/share/zoneinfo`, the import in `cmd/api/main.go` would stop
being load-bearing and the positive leg would stop proving anything, and that
leg is what says so. The **shipped** binary is checked separately, by its
bytes: an embedded `zoneinfo.zip` keeps its entry names uncompressed, so
`Asia/Tokyo`, `America/New_York` and `Europe/London` are all in `/api`, along
with **598** `TZif` headers. Without the import: **zero**.

### TWO FINDINGS, and the first one contradicts something this file implied

**1. The stack coming up does NOT prove the numeric user can execute the
binary. A capability hides it.**

`COPY --chmod=700 /out/api /api` — root-owned, no permissions for anyone else —
against `USER 65532:65532`, and **the container starts and answers `/healthz`
normally**. The first draft of `stack_test.go` claimed that leg as the proof of
executability; the mutation left it green.

The reason: runc still holds the default capability set, `CAP_DAC_OVERRIDE`
included, at the moment it `execve`s the entrypoint, so the exec of a 0700 file
succeeds. The capabilities are then dropped **by that same `execve`**, because
a non-root euid inherits none without file capabilities. Confirmed from the
other side, which is what turns the explanation into a measurement:

```
$ docker run --rm --cap-drop=ALL <image-with-0700-binary>
docker: Error response from daemon: ... unable to start container process:
exec: "/api": permission denied: unknown
```

So a wrong-permission binary is a **latent** defect: invisible until somebody
hardens the deploy with `cap_drop`, at which point the container will not
start. The only leg in this tier that catches it is the probe's `open("/api")`,
which runs *after* the capabilities are gone and reports `permission denied` on
an image that boots fine. Both comments have been corrected in place.

**2. My own absence assertions were blind, and a mutation is what found them.**

`docker export` writes directory entries with a **trailing slash** and file
entries without. The four "this must not exist in the image" checks looked up
`usr/share/zoneinfo`, and the export held `usr/share/zoneinfo/`. A mutation
that copied the entire zone database into the runtime image left that
assertion **green**; only the layer count (2 → 3) went red, and it is the
reason the miss was noticed at all. Keys are normalised now. Two lessons, both
already in the client project's list: *an absence assertion is the easiest kind
to write so that it cannot fail*, and *a leg that reddens for a neighbouring
reason is what saves you when the aimed-at leg does not*.

### The twelve mutations, and the eighteen legs they reddened

**Ten of the twelve were applied to a COPY of the repository** under `/tmp`,
reached by `TRAVELLOG_REPO`, and not to the working tree. That is not
squeamishness about `git diff`: two other agents were writing in this
repository at the time, and `cmd/api/**` and `internal/**` were another
worker's. The two that had to be in-tree are the two whose subject is this
package's own source; both were reverted and `git diff` was checked clean.

| # | mutation | legs reddened | actual output |
|---|---|---|---|
| M1 | `USER 65532:65532` → `USER nonroot` | `TestRuntimeImageRunsAsANumericNonRootUser`, `TestTheStackComesUpAndAnswersHealthz` | `Config.User = "nonroot", want uid:gid …` and, from the daemon, `unable to find user nonroot: no matching entries in passwd file` |
| M2 | `USER 65532:65532` → `USER 0:0` | `TestRuntimeImageRunsAsANumericNonRootUser`, `TestTheContainerProcessRunsAsTheNumericUser` | `Config.User = "0:0", which is root`; `uid = 0: the container is running as root` |
| M3 | delete the CA bundle `COPY` | `TestRuntimeImageCarriesTheCABundle`, `TestTheCABundleGivesTheContainerARealRootStore`, `TestOutboundTLSVerifiesAgainstTheCopiedBundle`, `TestRuntimeImageIsScratchAndHasNothingToFallBackOn` | `x509.SystemCertPool() holds 0 roots`; `tls: failed to verify certificate: x509: certificate signed by unknown authority`; `1 layers, want 2` |
| M4 | `HEALTHCHECK … CMD ["/api", "-healthcheck"]` → shell form | `TestRuntimeImageHealthcheckInvokesTheBinarysOwnFlag`, `TestDockerReportsTheContainerHealthy` | `HEALTHCHECK is "CMD-SHELL" form, want CMD (exec)`; and the health log, which is the whole argument: `exec: "/bin/sh": stat /bin/sh: no such file or directory` × 5, `health status = "unhealthy" after 120s` |
| M5 | delete `HEALTHCHECK` entirely | same two | `the image declares no HEALTHCHECK …`; `the running container has no health state at all` |
| M6 | `COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo` | `TestScratchWithoutEmbeddedTzdataCannotResolveAZone`, `TestRuntimeImageIsScratchAndHasNothingToFallBackOn` | `a binary WITHOUT _ "time/tzdata" loaded Asia/Tokyo inside this image`; `/usr/share/zoneinfo is present`; `3 layers, want 2` |
| M7 | delete `_ "time/tzdata"` from the copy's `cmd/api/main.go` | `TestTheShippedBinaryEmbedsTheTimezoneDatabase` | `the shipped binary contains no "Asia/Tokyo"` … `holds 0 TZif headers, want the whole database (measured: 598)` |
| M8 | remove `pgdata:/var/lib/postgresql/data` from compose | `TestTheNamedVolumeSurvivesDownAndUp` | `no volume named "travellog-imagetest-vol_pgdata"`; and after `down` + `up`, `ERROR: relation "volume_probe" does not exist` — *the database directory did not survive the restart, and a redeploy would destroy every trip in the log* |
| M9 | compose `DATABASE_URL` host → `nosuchhost` | `TestTheStackComesUpAndAnswersHealthz` | `connect: connection refused`, over a restart loop of `the database did not answer within 10s: … lookup nosuchhost` |
| M10 | `ENTRYPOINT ["/api"]` → `CMD ["/api"]`, delete `EXPOSE` | `TestRuntimeImageEntrypointIsTheBinary` | `Entrypoint = [], want [/api]`; `ExposedPorts = map[], want 8080/tcp` |
| M11 | `COPY --chmod=700 /out/api /api` | `TestTheShippedBinaryIsReadableAndExecutableByAnyUser`, `TestTheShippedBinaryIsReadableByTheUserItRunsAs` | `/api mode 0700: the uid in USER is not the owner, so it needs o+rx`; `opening /api as uid 65532: err:open /api: permission denied`. **And see finding 1: the stack leg stayed green** |
| M12 | in-tree, reverted: `writeNotice` writes nothing; the skip reason drops the target name | `TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt`, `TestTheSkipReasonNamesTheMakeTarget` | `notice = "", want "hello from the skip\n"`; `skip reason … does not name "make test-image"` |

One leg is reddened by a **test-side** mutation rather than a subject-side one,
and it is labelled that way rather than counted with the rest:
`TestEmbeddedTzdataResolvesInsideScratch` asserts that a binary **with** the
import resolves `Asia/Tokyo` in this image, and nothing in the Dockerfile can
falsify that — the probe carries its own import. Pointing it at the no-tzdata
probe reddens it (`LoadLocation("Asia/Tokyo") inside the runtime image:
err:unknown time zone Asia/Tokyo`), which proves the assertion is wired to the
subject it names. Reverted immediately.

**Count the legs against the mutations rather than assuming a suite that went
red somewhere went red everywhere** — the backfill's own M9/M10 lesson. Twelve
mutations, eighteen legs, and four of the legs needed a mutation aimed at
nothing else: M10, M11 and both halves of M12 exist only because their targets
had never failed.

### What the attempt revealed about VS1's infrastructure

**No defect was found in it.** Every claim VS1's Dockerfile comment makes about
`scratch` is true of the built image: the bundle is there and gives the process
150 real roots, the binary carries the whole IANA database, the user is 65532
at runtime and not just in the Dockerfile, Docker's own probe goes `healthy`
inside a container with no shell, the stack comes up, and the volume survives.
What was missing was the guard, not the correctness — the same sentence
VS1-BACKFILL wrote about `/healthz`, now true of the image.

Two things it recorded that are not defects and are worth carrying:

1. **`x509.SystemCertPool()` reports 150 roots inside the image**, and an
   outbound TLS handshake to `proxy.golang.org:443` verifies. That leg is the
   one place in this tier that needs the internet, so its failure is
   **classified**: a dial that cannot resolve or connect **skips**, because
   that is a fact about the network; a dial that connects and fails to
   **verify** is a hard failure, because that is the defect it exists for.
   Under M3 it took the second branch, which is how the classification was
   itself proven.
2. **The `USER`/capability interaction in finding 1** — recorded because it
   changes what a reviewer may conclude from "the stack came up", and because
   the natural hardening step (`cap_drop: ALL` on the api service) would turn a
   silent condition into a container that never starts. Not proposed here:
   `deploy/docker-compose.yml` is not this step's file to change.

### What is STILL guarded by nothing

- **The image's behaviour on `linux/amd64`.** Everything above ran on
  `arm64` — the probe is cross-compiled for the daemon's architecture, so the
  tier follows the machine rather than pinning one, and no leg has ever run on
  amd64.
- **The `/dev/tty` half of the skip notice, against a real terminal.** It is
  guarded through a temporary file, because this environment has no pty to
  allocate. What is proven is that the helper writes what it is given and
  swallows a path it cannot open — not that a developer at a terminal sees the
  line. That last step is a human with a shell, and it is unticked.
- **`postgres:17` → `18`**, which the VS1 review flagged as a latent trap
  (`PGDATA` moved). `TestTheNamedVolumeSurvivesDownAndUp` would catch the data
  loss on a bump, but nothing asserts the tag, and this tier does not run in
  the gate — so the trap is caught only by somebody running `make test-image`.

---

