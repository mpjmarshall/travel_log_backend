// Package image_test guards the VS1 infrastructure claims that `go test ./...`
// cannot reach: what the `scratch` runtime image contains, who it runs as,
// whether its HEALTHCHECK works, and whether the named volume survives a
// restart. Every one of those was recorded in CLAUDE.md as "guarded by
// nothing" after VS1-BACKFILL.
//
// THIS IS AN OPT-IN TIER, in the shape the project already uses for the
// database tests: it needs Docker and it needs TRAVELLOG_IMAGE_TESTS=1, and
// without either it SKIPS and names `make test-image`. `make check` stays four
// commands and stays fast — nothing here runs inside it.
//
// A SKIP THAT NOBODY SEES IS A PASS THAT LIES, and the mechanism for saying so
// was measured rather than assumed. Measured 22 August 2026, Go 1.26.5: a
// package whose tests all pass or skip prints exactly one line under
// `go test ./...` — `ok  <pkg>  0.2s`. t.Skip's message, t.Log, and anything
// TestMain writes to stdout OR stderr are ALL suppressed; they appear only
// under `-v` or when the package fails. So the skip reason is written a second
// time to /dev/tty, which `go test` does not own and cannot capture. When
// there is no controlling terminal — a pipe, a CI runner — the write fails and
// nothing is printed, which is why the t.Skip message carries the same
// sentence and is what `-v` and test2json see.
//
// THE MUTATION HARNESS IS TWO ENVIRONMENT VARIABLES, and it is that rather
// than an edit-and-restore because two other agents are working in this
// repository. TRAVELLOG_REPO points the build at a COPY of the repository, so
// a Dockerfile or compose mutation happens in /tmp and `git diff` is clean by
// construction rather than by remembering to restore. TRAVELLOG_TEST_IMAGE
// skips the build and points every image leg at a tag built by hand.
package image_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// optInVar is the opt-in. Unset is a skip, not a failure: `go test ./...` on a
// machine with no Docker has to stay green.
const optInVar = "TRAVELLOG_IMAGE_TESTS"

// makeTarget is the make target every skip message names.
const makeTarget = "make test-image"

// imageTag is what the image under test is built to, unless TRAVELLOG_TEST_IMAGE
// names another. Deliberately NOT `travellog-api`, which is what a developer's
// own `make up` produces — a test must not silently test the image somebody
// built by hand an hour ago.
const imageTag = "travellog-imagetest:under-test"

const (
	buildTimeout   = 10 * time.Minute
	composeTimeout = 5 * time.Minute
	shortTimeout   = 60 * time.Second
)

// Host ports for the tiers' own compose projects. NOT 8080/5434: a developer's
// `make up` stack may be running, and a test that cannot run beside the thing
// it tests is a test nobody runs.
const (
	stackAPIPort = "18080"
	stackPGPort  = "15434"
	volumePGPort = "15435"
	// R2 gives the stack a third published port, and it needs its own for the
	// same reason the two above do: a developer's `make up` may be holding
	// 9000, and so may anything else — it is a popular port.
	stackMinioPort = "19000"
	stackProject   = "travellog-imagetest"
	volumeProject  = "travellog-imagetest-vol"
	composeRelPath = "deploy/docker-compose.yml"
)

var (
	buildOnce sync.Once
	buildErr  error
	builtTag  string
)

// TestMain says once, on the way in, why the tier is not running.
//
// Whatever the tier started it takes away, including the volumes — which is the
// one `docker compose down` flag these tests must never aim at a developer's
// own project. They cannot: every stack here runs under its own -p name.
func TestMain(m *testing.M) {
	if reason, ok := unavailable(); !ok {
		writeNotice("/dev/tty", "travellog/test/image: "+reason)
	}
	code := m.Run()
	tearDownStacks()
	os.Exit(code)
}

// unavailable reports why the tier cannot run, or ok if it can.
func unavailable() (reason string, ok bool) {
	if os.Getenv(optInVar) != "1" {
		return fmt.Sprintf(
			"SKIPPED — the image tier is opt-in. Set %s=1, or run `%s`.",
			optInVar, makeTarget,
		), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		return fmt.Sprintf(
			"SKIPPED — %s=1 but no Docker daemon answered (`docker version`: %v: %s). `%s` needs one.",
			optInVar, err, strings.TrimSpace(string(out)), makeTarget,
		), false
	}
	return "", true
}

// requireDocker is the first line of every leg in this tier.
func requireDocker(t *testing.T) {
	t.Helper()
	if reason, ok := unavailable(); !ok {
		t.Skip(reason)
	}
}

// writeNotice puts msg somewhere `go test` cannot capture. It is best effort by
// design: with no controlling terminal the open fails and the caller carries on.
//
// The path is a parameter rather than a constant so the mechanism itself has a
// test — TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt — which runs with no
// Docker and inside `make check`.
func writeNotice(path, msg string) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, msg)
}

// repoRoot is the tree the image is built from: two directories up, or
// whatever TRAVELLOG_REPO names. The override is the mutation harness.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return root
}

// findRepoRoot is the half of repoRoot that TestMain can call, because
// teardown runs after m.Run and there is no *testing.T left to fail.
func findRepoRoot() (string, error) {
	if r := os.Getenv("TRAVELLOG_REPO"); r != "" {
		return r, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("no go.mod at %s, so this is not the repository root: %w", root, err)
	}
	return root, nil
}

// image builds the root Dockerfile once per test binary and returns the tag.
func image(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if tag := os.Getenv("TRAVELLOG_TEST_IMAGE"); tag != "" {
			builtTag = tag
			return
		}
		root := repoRoot(t)
		builtTag = imageTag
		_, buildErr = runIn(root, buildTimeout, "docker", "build",
			"-f", filepath.Join(root, "Dockerfile"),
			"-t", imageTag, root)
	})
	if buildErr != nil {
		t.Fatalf("docker build: %v", buildErr)
	}
	return builtTag
}

// run executes a command and fails the test with its combined output.
func run(t *testing.T, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	out, err := runIn("", timeout, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// execute is the one place an external command is run: every other helper is
// a name for a particular set of arguments to it.
func execute(timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("timed out after %s: %w", timeout, ctx.Err())
	}
	return string(out), err
}

func runIn(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	return execute(timeout, dir, nil, name, args...)
}

func runCompose(t *testing.T, timeout time.Duration, env []string, args []string) (string, error) {
	t.Helper()
	return execute(timeout, "", env, "docker", args...)
}

func mustCompose(t *testing.T, timeout time.Duration, env []string, args []string) string {
	t.Helper()
	out, err := runCompose(t, timeout, env, args)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// compose builds a `docker compose` invocation against this repository's file
// under its own project name. The project name is why these tests can run
// beside a developer's `make up` stack instead of tearing it down: `-p` beats
// the `name: travellog` in the compose file, so the volume created here is
// travellog-imagetest_pgdata and nobody's data is at risk.
func compose(t *testing.T, project string, args ...string) []string {
	t.Helper()
	root := repoRoot(t)
	base := []string{"compose", "-f", filepath.Join(root, composeRelPath), "-p", project}
	return append(base, args...)
}

// composeEnv puts the port overrides in the environment. Compose reads the
// shell environment ahead of deploy/.env, so this wins over a developer's own.
//
// S3_PUBLIC_BASE_URL MOVES WITH THE PORT, and that is not tidiness: it is the
// address a SIGNATURE covers (DEC-42), so leaving it pointed at 9000 while
// MinIO is published on 19000 would mint URLs for a port nothing is listening
// on. Nothing in this tier presigns yet; the pair is kept honest here so that
// the day something does, it is not a two-hour puzzle.
func composeEnv(pgPort, apiPort string) []string {
	return append(os.Environ(),
		"POSTGRES_PORT="+pgPort,
		"API_PORT="+apiPort,
		"MINIO_PORT="+stackMinioPort,
		"S3_PUBLIC_BASE_URL=http://127.0.0.1:"+stackMinioPort,
	)
}

// TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt guards the mechanism the
// skip message depends on. It needs no Docker and runs inside `make check`,
// because the claim it defends — "a machine with no Docker is told, rather
// than shown a silent green" — is the one claim of this file that a developer
// with no Docker can still break.
//
// The last write is the other half: it must not panic or fail on a path that
// cannot be opened, which is every machine without a controlling terminal.
func TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notice")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("seeding the notice file: %v", err)
	}

	writeNotice(path, "hello from the skip")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the notice back: %v", err)
	}
	if want := "hello from the skip\n"; string(got) != want {
		t.Errorf("notice = %q, want %q", got, want)
	}

	writeNotice(filepath.Join(t.TempDir(), "no", "such", "path"), "swallowed")
}

// TestTheSkipReasonNamesTheMakeTarget keeps the message honest. A skip that
// says only "skipping" leaves the reader with nothing to run.
func TestTheSkipReasonNamesTheMakeTarget(t *testing.T) {
	saved, had := os.LookupEnv(optInVar)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(optInVar, saved)
		} else {
			_ = os.Unsetenv(optInVar)
		}
	})
	_ = os.Unsetenv(optInVar)

	reason, ok := unavailable()
	if ok {
		t.Fatalf("with %s unset the tier must report itself unavailable", optInVar)
	}
	for _, want := range []string{makeTarget, optInVar, "SKIPPED"} {
		if !strings.Contains(reason, want) {
			t.Errorf("skip reason %q does not name %q", reason, want)
		}
	}
}
