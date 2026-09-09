## REVIEW-2b — CI's first nightly run found `make test-image` broken

The `slice` workflow was dispatched the minute it reached `main`. It failed in
5m44s, at `make test-image`, and **it is a real defect rather than a CI
problem**:

```
api-1 | {"level":"ERROR","msg":"api: stopping","err":"no mail provider is
        configured and MAIL_LOG_SENDER is not set: mail: the log sender writes
        sign-in codes to the log and must be asked for by name"}
--- FAIL: TestTheStackComesUpAndAnswersHealthz (102.30s)
    stack_test.go:122: health status = "unhealthy" after 120s, want healthy
--- FAIL: TestDockerReportsTheContainerHealthy (120.81s)
FAIL	travellog/test/image	293.044s
```

Reproduced on the developer machine before anything was changed, so it is the
tree and not the runner.

**`test/image` brings the stack up through compose and supplied no bootable
configuration.** `MAIL_LOG_SENDER` defaults to `0` in
`deploy/docker-compose.yml`, correctly — shipping `1` would put sign-in codes
in every deployment's log — and the log sender is the **only** `mail.Sender`
that exists, so `0` is a stack that restart-loops. The tier has been red since
`DEVELOPMENT` was split into two switches, and nobody had run it.

**That is exactly what the workflows were added for, and it is the argument for
them in one measurement:** the gate was green the whole time, because
`test/image` is opt-in behind `TRAVELLOG_IMAGE_TESTS=1` and `go test ./...`
skips every leg in it.

The fix is one line in `composeEnv`, which is the one place the tier composes
its environment, and the reason is in the doc comment above it. It is **not** a
change to the compose default and it is **not** a variable set in the workflow:
a harness that brings a stack up owns supplying a configuration that boots, and
putting it in CI alone would have left `make test-image` broken for a developer.

Whole tier after the fix: **18 legs, exit 0, 50.5s.**

### And the arc is broken too, worse, and is NOT fixed here

`scripts/slice-arc.sh` sets no `MAIL_LOG_SENDER` either — and its auth steps
are written against a flow that no longer exists. A4, A5, A5b, A5c, A6 and A31
all post `{email, passphrase}` to `POST /v1/auth/register` and
`POST /v1/auth/session`. Migration 0007 deleted the passphrase column;
registration takes `{email, invite}` and sign-in takes `{email, code}`. A5 and
A5b assert that registration is *closed* after the first traveller, which 0006
("the end of the one-traveller rule") ended.

`scripts/slice-arc.sh` was last touched `97c1700`, the same day the passphrase
was deleted. **`make slice` has not run since.** It is a repair of the
project's main evidence tier and it changes what several steps assert, which is
a design question rather than a typo, so it is written into `TODO.md` with the
question rather than answered quietly here.

### Commands, not numbers

```bash
make test-image                                       # exit 0, 18 legs
grep -n 'MAIL_LOG_SENDER' test/image/image_test.go    # one line, with its reason
grep -c 'passphrase' scripts/slice-arc.sh             # the arc's staleness
```
