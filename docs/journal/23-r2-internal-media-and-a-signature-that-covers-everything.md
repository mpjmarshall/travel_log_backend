## R2 — internal/media, and a signature that covers everything the URL can do

The second step of plan-v7, and the first that adds a **service**, an
**external dependency** and a **capability the server hands to somebody else**.
A presigned PUT is a bearer capability: whatever it is allowed to do has to be
inside the signature, or it is not bounded at all.

**Five commits.** The same reading of DEC-23 R1 took: the record is written
with the code, a step this size as one commit is unreviewable, and a section
per commit would be five sections about one step.

### THE BAN IS EARNED, AND HERE IS THE RED THAT EARNS IT

minio-go offers three ways to presign a PUT and **two of them sign `host` and
nothing else** — confirmed in `api-presigned.go`, where both call `presignURL`
with a nil extra-header set, rather than assumed. `PresignHeader` is the only
one that signs extra headers.

Implemented naively first, and run against real MinIO before the real signer
existed:

```
$ TEST_S3_ENDPOINT=http://127.0.0.1:9412 go test -tags integration \
    ./internal/media/ -count=1 -run TestABodyThatDoesNotMatch -v
    minio_integration_test.go:230: the bucket answered 200  for bytes that are
    not the digest the URL was signed for; want XAmzContentChecksumMismatch
--- FAIL  (0.01s)
exit=1
```

**200, and the object stored at an address claiming to be its hash.** Every
later reader trusts that address. A leg written after the correct
implementation cannot produce that line, which is the whole reason the naive
implementation was written on purpose.

### Four headers, four rulings, and each is holding something up

| header | ruling | what it stops |
|---|---|---|
| `x-amz-checksum-sha256` | DEC-38 | bytes that are not the address |
| `content-length` | DEC-51 | a minted URL being an **unbounded** write |
| `content-type` | DEC-87 | a row reading `image/png` addressing an object the bucket serves as `text/html` |
| `if-none-match: *` | DEC-88 | a second PUT silently replacing a committed object |

Plus `host`, which SigV4 always covers, and which is why DEC-42's two addresses
are two variables.

**THE LENGTH IS EXACT AND CAN NEVER BE A CEILING, and both sentences are
needed.** SigV4 signs a header VALUE, so the bucket enforces `== byteSize`;
`MEDIA_MAX_BYTES` is an API-side refusal to **mint**, taken before the
capability exists. A worker who reads "sign `MEDIA_MAX_BYTES`" literally
produces a URL that accepts only a file of exactly the maximum size. A real
range needs a presigned POST policy, which is a different client contract.

### THE PLAN'S CHECKSUM LEG COULD NOT SEE THE LENGTH CONTROL, AND VICE VERSA

The leg this was written from used a **45-byte lie against a 29-byte
signature**. With `content-length` signed, that request is refused by the
LENGTH control with `SignatureDoesNotMatch` and the digest is never looked at.
**Two controls means each leg varies exactly one thing**: the checksum leg now
lies with bytes of the *same length*, and the length leg holds every signed
header constant and varies only the body. The pair is what M1 proves — drop the
length from the signature and the length leg reddens while the checksum leg
stays green.

### THE MUTATION THE PLAN PREDICTED, WHICH DID NOT HAPPEN

The plan says: swap the signer for the banned call and *"the checksum leg
reddens against real MinIO"*. **It does not.** Measured: with `host` signed and
nothing else, and the four headers still **sent**, MinIO validates the digest
anyway and answers `XAmzContentChecksumMismatch`. The leg passes.

The reason is that it exercises an **honest** client. An attacker holding the
same URL simply omits the digest header, and with only `host` in the signature
there is nothing left to refuse: **200**, and arbitrary bytes at the address.

So a leg was added that replays everything **except** the digest, and three
things now cover the ban, none of them replacing another:

- `ban_test.go` — an AST walk saying the code does not call it, **inside
  `make check`**, because there is no CI and a grep in a plan document guards
  nothing.
- the header-map leg — saying the map and the signature agree.
- `TestAnUploadThatOmitsTheDigestIsRefused` — saying what the bucket does if
  either is wrong.

**`ban_test.go` spells the two banned names in halves**, and that is a decision
rather than coyness: the step's acceptance check greps this directory for them
and must return nothing, so a guard that wrote its subject out in full would
make the grep match its own explanation. That is the phase-2 defect class
exactly — an artefact check that matches its own replacement can only fail
against correct work.

### THE TWO LENSES MEASURED OPPOSITE THINGS AND BOTH WERE RIGHT

One got *"The specified bucket does not exist"* **at presign time**; the other
got a perfectly-formed URL that failed on the phone. The branch is one line in
minio-go's `bucket-cache.go`: *"Region set then no need to fetch bucket
location"*. With `Region` empty, the first presign per bucket does a real
`?location=` round trip; with it set, presigning never touches the network.

**Observed here: the succeeding branch, from cold.** `media.New` pins the
region on both clients, so `TestPresigningAgainstAMissingBucketSucceedsAndThe
UploadIsWhatFails` gets a well-formed URL against a bucket that does not exist
and the PUT is what answers `NoSuchBucket`. That is a structural proof that
presigning is offline — stronger than a timing — and it is what makes R3's
"one blocking call plus twelve local HMACs" false and "twelve local HMACs"
true, from the first request.

**And it is why DEC-98's fix is at boot rather than in a healthcheck.** Nothing
creates the bucket: re-measured here, the pinned image with a fresh volume is
healthy with `ls /data` = `.minio.sys` only, **zero buckets**, and
`MINIO_DEFAULT_BUCKETS` is a Bitnami variable. Deleting the call from `run()`:

```
make check                     exit 0     (green)
docker compose up -d --wait    3 healthy
scripts/slice-arc.sh arc       FAIL  buckets named travellog-media
                                     in `mc ls local/` = 0, want 1
```

**`make check` stays green and that is stated rather than glossed.**
`cmd/api/media_test.go` guards what `mediaStore` *does* — against a closed port
it must refuse to come up — and cannot see whether `run()` calls it. The arc is
the only guard on that call site.

### Two variables holding one fact, kept honest by refusing them

`Key.Object` and `Upload.SHA256` are the same digest. `Address` is the one
function that turns **one** hex digest into the object's path **and** the
base64 checksum header, and `PresignPut` **refuses** a disagreement rather than
picking one. Both fields exist on purpose: DEC-88 asks for the two-variable
mistake to be a state a leg can redden, which means it has to be expressible.

The two encodings are not a style choice. `x-amz-checksum-sha256` is base64 by
the S3 protocol and the id is hex everywhere else, including migration 0001's
`media_objects_id_sha256_ck`. Sign the hex and **every** upload is refused with
`400 InvalidArgument` — a different sentence from a genuine mismatch, which is
why every leg asserts the CODE and never a status class. Four failure modes
land in 4xx and only one is any given leg's control.

### `Audience`, and why it is an enum rather than a duration

DEC-84's leg has to assert **which** lifetime each call site uses, and v7.1's
compared the two configured values to each other — which cannot fail in the way
that matters. A `time.Duration` parameter makes "the handler reached for the
private lifetime" a plausible-looking expression no leg can see. `Private` and
`Public` make it one wrong word, greppable, and assertable by reading
`X-Amz-Expires` back off the URL the signer produced: **120** and **900**.

Guarded at three points, and none implies another: inside `media.New` (M4), at
`cmd/api`'s wiring (M4b), and against the real signature.

### The twin refuses what the bucket refuses

`media.Memory` enforces the digest, the exact length, the write-once **and a
bucket that has to exist**, and its errors carry the S3 codes the real server
answers. A twin that accepts what MinIO refuses turns an R3 handler leg into
evidence about nothing. What it deliberately does not do is sign — its URLs are
`memory.invalid`, a reserved TLD, so a test that accidentally fetches one gets
a DNS failure rather than somebody's server.

### THREE ASSERTIONS THE ARC CARRIED OUT OF R1, ALL RED, ALL FOUND BY RUNNING IT

Not by reading. Each is a literal in `scripts/slice-arc.sh` that a shipped
change moved:

| step | the arc said | the stack answers | what moved it |
|---|---|---|---|
| A8/A9/A11/A15 | `W/"1-1"` | `W/"2-1"` | `EmitterVersion` 1 → 2 (DEC-91) |
| A10 | `false\|false\|false` | `true\|true\|false` | migration 0002's DEFAULTs (DEC-82) |
| A13 | `not_found` | `unsupported_route` | DEC-103 |

**The ETag stays a literal rather than being read out of `internal/logbook`.** A
client caches that exact string, so an emitter bump SHOULD break the arc; what
it must not do is break it silently a step later. A10's leg still says what it
was written to say, through the third flag: the body asks for
`shareCoordinates:true`, `TripWrite` has no slot for it, and the stored value
is still `false`.

**And A14's volume name is now derived from the running project.** It said
`travellog_pgdata`, so the arc could only run under the default project — and
the one time you want it elsewhere is when a live stack is holding somebody's
log. `COMPOSE_PROJECT_NAME` now moves the whole arc, which is how R2's own runs
were done.

### Divergences from the step, each deliberate

- **`S3_INTERNAL_ENDPOINT` is REQUIRED**, where DEC-42 says it "defaults to the
  public one when unset". Stricter, not weaker, and it is `internal/config`'s
  own standing rule: *nothing has a default*, because "the internal one is the
  public one unless you say otherwise" is exactly the silent value nobody chose
  that the rule refuses. compose sets both.
- **The interface is FOUR methods, not three.** The step's acceptance check
  says *"the interface is three methods, not four (OE-12)"* while its own work
  item 1 names four — `PresignPut`, `PresignGet`, `Stat`, `EnsureBucket`. The
  grep it actually runs is `grep -c 'Remove' … # -> 0`, which passes: there is
  no deletion method, which is what OE-12 is about. The count in the comment is
  a leftover from before `EnsureBucket` was added by DEC-98.
- **`PresignPut` takes no audience.** An upload capability belongs to the
  traveller who asked for it; nothing public ever writes.
- **`internal/media/minio.go`'s doc names the four headers in wire spelling**,
  because the acceptance check greps that file for the length half and the
  construction lives in `keys.go` where both implementations share it. The
  right place for the construction, the wrong place for "what does this URL let
  its holder do".

### What R2 leaves guarded by nothing

- **`S3_PRESIGN_TTL_PUBLIC`'s DEFAULT value.** Bounded at 1s..168h (minio-go's
  own limits) and the audience is asserted to get the configured one. **Nothing
  pins 15m**, which is the number four sentences of client copy are written
  against. Set it to `168h` and the suite is green and the copy is a lie. Same
  hole R1 recorded for `REQUEST_TIMEOUT` and VS8-SEC for the two rate limits —
  now **four** variables wide.
- **`MEDIA_MAX_BYTES` is loaded, bounded and read by nothing.** The route that
  refuses to mint above it is R3's.
- **Every `mem_limit`, `restart` and `logging` VALUE.** The acceptance check
  reads them out of `docker compose config`; no leg does, and nothing says
  `256m` is right. A human with a box.
- **Real S3.** Every S3 code asserted here is MinIO's. DEC-43's asymmetry cuts
  the right way for what matters — a REFUSAL here is strong evidence, since a
  stricter server also refuses — but `If-None-Match: *` on S3 is documentation
  and not a measurement, and so is S3's answer to a chunked PUT.
- **Nothing reclaims an object** (OE-12), and nothing backs the bucket up. A
  database restore without a bucket restore is 308 references that all resolve,
  pointing at nothing, with no error anywhere.
  `docs/BEFORE-A-PUBLIC-DEPLOY.md` carries both with their triggers.
- **`up -d --wait` does not rebuild.** The acceptance check's compose line ran
  once against a stale image left by a mutation and reported three healthy
  services with **zero** buckets. `make up` passes `--build`; the plan's line
  does not. Same class as VS6/VS7's "green in `go test`, 404 in the container".

### Commands, not numbers

```bash
# the module floor did not move, and what actually ships
go mod tidy && grep '^go ' go.mod                       # go 1.25.0
go build -o bin/api ./cmd/api && go version -m bin/api | grep -c '^	dep'   # 24

# the environment this build reads
grep -oE '"[A-Z][A-Z0-9_]+"' internal/config/config.go | sort -u | wc -l

# the legs
TEST_DATABASE_URL=... go test ./... -count=1 -v | grep -c -- '--- PASS'
TEST_S3_ENDPOINT=...  go test -tags integration ./internal/media/ -count=1 -v \
  | grep -c -- '--- PASS'

# the two stacks, neither of which is the one holding a log
make test-s3     # the pinned MinIO the integration legs use
COMPOSE_PROJECT_NAME=... make slice   # exit 0, 80 assertions, from cold
```

