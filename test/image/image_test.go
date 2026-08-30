// Package image_test guards the infrastructure claims that `go test./...`
// cannot reach.
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

// optInVar is the opt-in.
const optInVar = "TRAVELLOG_IMAGE_TESTS"

// makeTarget is the make target every skip message names.
const makeTarget = "make test-image"

// imageTag is what the image under test is built to, unless
// TRAVELLOG_TEST_IMAGE names another.
const imageTag = "travellog-imagetest:under-test"

const (
	buildTimeout   = 10 * time.Minute
	composeTimeout = 5 * time.Minute
	shortTimeout   = 60 * time.Second
)

// Host ports for the tiers' own compose projects.
const (
	stackAPIPort   = "18080"
	stackPGPort    = "15434"
	volumePGPort   = "15435"
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

// requireDocker is's first line of every leg in this tier.
func requireDocker(t *testing.T) {
	t.Helper()
	if reason, ok := unavailable(); !ok {
		t.Skip(reason)
	}
}

// writeNotice puts msg somewhere `go test` cannot capture.
func writeNotice(path, msg string) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, msg)
}

// repoRoot is the tree the image is built from: two directories up, or
// whatever TRAVELLOG_REPO names.
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
// under its own project name.
func compose(t *testing.T, project string, args ...string) []string {
	t.Helper()
	root := repoRoot(t)
	base := []string{"compose", "-f", filepath.Join(root, composeRelPath), "-p", project}
	return append(base, args...)
}

// composeEnv puts the port overrides in the environment.
func composeEnv(pgPort, apiPort string) []string {
	return append(os.Environ(),
		"POSTGRES_PORT="+pgPort,
		"API_PORT="+apiPort,
		"MINIO_PORT="+stackMinioPort,
		"S3_PUBLIC_BASE_URL=http://127.0.0.1:"+stackMinioPort,
	)
}

// TestSkipNoticeIsWrittenWhereGoTestCannotCaptureIt guards the mechanism the
// skip message depends on.
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

// TestTheSkipReasonNamesTheMakeTarget keeps the message honest.
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
