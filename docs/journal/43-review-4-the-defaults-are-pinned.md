## REVIEW-4 — the shipped defaults are pinned, not merely present

`deploy/docker-compose.yml` carries **28 `${VAR:-default}` defaults**, and
`internal/config/deploy_files_test.go` asserted each variable was **present**.
Nothing asserted what any of them **was**. Measured before this leg existed:
setting the public presign lifetime to seven days, the public ceiling to 1/min
and the api's memory limit to 16 MB left `make check` at **exit 0, zero FAIL
lines**.

Ten values are pinned now — the nine a review named, plus `BIND_HOST`, which is
the one outside the `environment:` block and the one whose wrong value publishes
the API to the whole LAN.

| variable | pinned | what the wrong value does |
|---|---|---|
| `S3_PRESIGN_TTL_PUBLIC` | `15m` | four sentences of client copy are written against it |
| `S3_PRESIGN_TTL_PRIVATE` | `2m` | the revocation window |
| `ADMIN_COOKIE_INSECURE` | `0` | `1` ships the panel cookie in the clear |
| `MAIL_LOG_SENDER` | `0` | `1` writes every sign-in code to the log |
| `AUTH_RATE_LIMIT_PER_MIN` | `10` | the only bound on unauthenticated work |
| `TRAVELLER_RATE_LIMIT_PER_MIN` | `600` | what a stolen token meets |
| `PUBLIC_RATE_LIMIT_PER_MIN` | `120` | the only bound on a route with no identity |
| `MEDIA_MAX_BYTES` | `26214400` | `1<<20` refuses an ordinary photograph |
| `REQUEST_TIMEOUT` | `15s` | `1s` makes every sign-in a 503 |
| `BIND_HOST` | `127.0.0.1` | `0.0.0.0` publishes the API to the LAN |

**Every value was re-derived from the file before the table was written**, not
copied from the review, and all ten agreed.

### THE PARSE IS SCOPED TO ONE SERVICE, AND THE PRECONDITION IS THE HALF THAT
### MATTERS

`composeServiceBlock` takes a service's own YAML by indentation — the device
`composeAPIEnvironment` already used, one level out, so `ports:` and
`mem_limit:` are in scope and `BIND_HOST` is reachable. `composeDefaults` reads
`${VAR:-default}`, which is the only shape compose takes a default in.

Both refuse to measure nothing: an empty block is a `t.Fatal`, and **fewer than
twenty defaults parsed is a `t.Fatal` saying so** — because a parse that finds
nothing makes every assertion below it pass. Proven by mutation rather than
asserted: inserting one line so the block boundary matches immediately gives
*"the api service block is empty, so this leg would measure nothing"*, and
renaming the service gives *"declares no api service"*.

### The mutations, restored by file copy and verified with `cmp -s`

The first is the exact set the record measured as leaving the gate green:

```
S3_PRESIGN_TTL_PUBLIC defaults to "168h", want "15m".
    It matters because four sentences of client copy are written against
    fifteen minutes, and a public share envelope embeds these URLs for anyone
    holding the link.
MAIL_LOG_SENDER defaults to "1", want "0".
    It matters because 1 writes every sign-in code to the container log, where
    anyone who can read logs can sign in as anyone.
PUBLIC_RATE_LIMIT_PER_MIN defaults to "1", want "120".
    It matters because the public share read's own bucket, and the only bound
    on a route with no identity at all.
```

and separately, the one outside the environment block:

```
BIND_HOST defaults to "0.0.0.0", want "127.0.0.1".
```

**A fourth mutation was discarded rather than reported.** `sed 's|^  api:|  api:|'`
changed nothing, and this project's standing rule is that a mutation which does
not change the file is a green suite proving nothing. `cmp -s` before the run is
what caught it, which is why that assertion is in the harness rather than in the
method.

### What this leaves guarded by nothing

- **Eighteen of the twenty-eight defaults.** Ports, credentials, pool sizes,
  memory ceilings, `LOG_LEVEL`, `S3_REGION`. Each is a judgement about whether a
  wrong value is a security or correctness problem, and the ten above are the
  ones where it is.
- **`deploy/.env.example`'s values.** It documents the knobs and nothing
  compares its numbers to compose's.
- **Whether any pinned number is RIGHT.** The leg pins what is documented; the
  derivations live in the compose file's own comments and in
  `docs/BEFORE-A-PUBLIC-DEPLOY.md`.

### Commands, not numbers

```bash
grep -oE '\$\{[A-Z_0-9]+:-[^}]*\}' deploy/docker-compose.yml | sort -u | wc -l   # 28
go test ./internal/config/ -run TestTheShippedDefaults -count=1 -v
```
