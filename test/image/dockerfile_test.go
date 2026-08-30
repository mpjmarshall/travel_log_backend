// What the runtime image is, read off the built artefact rather than off the
// Dockerfile's text.
package image_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The four things `scratch` does not supply, per the root Dockerfile's own
// comment.
const (
	caBundlePath = "etc/ssl/certs/ca-certificates.crt"
	binaryPath   = "api"
)

// imageConfig is the subset of `docker image inspect` these legs read.
type imageConfig struct {
	Config struct {
		User         string
		Entrypoint   []string
		ExposedPorts map[string]struct{}
		Healthcheck  *struct {
			Test        []string
			Interval    time.Duration
			Timeout     time.Duration
			StartPeriod time.Duration
			Retries     int
		}
	}
	RootFS struct {
		Layers []string
	}
}

func inspectImage(t *testing.T, tag string) imageConfig {
	t.Helper()
	out := run(t, shortTimeout, "docker", "image", "inspect", tag)
	var got []imageConfig
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding docker image inspect: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("docker image inspect returned %d entries, want 1", len(got))
	}
	return got[0]
}

// exportImage lists every path in the image's filesystem and returns the
// bytes of one of them.
func exportImage(t *testing.T, tag, want string) (map[string]*tar.Header, []byte) {
	t.Helper()

	cid := strings.TrimSpace(run(t, shortTimeout, "docker", "create", tag))
	t.Cleanup(func() {
		_, _ = runIn("", shortTimeout, "docker", "rm", "-f", cid)
	})

	ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "export", cid)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("docker export pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("docker export: %v", err)
	}

	headers := make(map[string]*tar.Header)
	var wanted []byte
	tr := tar.NewReader(stdout)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading the export tar: %v (stderr: %s)", err, stderr.String())
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		headers[name] = h
		if name == want {
			if wanted, err = io.ReadAll(tr); err != nil {
				t.Fatalf("reading %s out of the export: %v", want, err)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("docker export exited %v (stderr: %s)", err, stderr.String())
	}
	return headers, wanted
}

// The user must be numeric on both sides of the colon.
func TestRuntimeImageRunsAsANumericNonRootUser(t *testing.T) {
	requireDocker(t)
	cfg := inspectImage(t, image(t))

	got := cfg.Config.User
	if got == "" {
		t.Fatalf("Config.User is empty, so the container runs as root")
	}
	if got == "root" || strings.HasPrefix(got, "0:") || got == "0" {
		t.Fatalf("Config.User = %q, which is root", got)
	}
	uid, gid, found := strings.Cut(got, ":")
	if !found {
		t.Fatalf("Config.User = %q, want uid:gid — a name has no /etc/passwd here to resolve against, and a bare uid leaves the group at 0", got)
	}
	for label, part := range map[string]string{"uid": uid, "gid": gid} {
		if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			t.Errorf("Config.User %s = %q, want digits: scratch has no /etc/passwd to resolve a name against", label, part)
		}
	}
	if uid == "0" || gid == "0" {
		t.Errorf("Config.User = %q, want a non-zero uid and gid", got)
	}
}

// TestRuntimeImageHealthcheckInvokesTheBinarysOwnFlag is the healthcheck
// wiring leg, as distinct from the -healthcheck flag it invokes.
func TestRuntimeImageHealthcheckInvokesTheBinarysOwnFlag(t *testing.T) {
	requireDocker(t)
	cfg := inspectImage(t, image(t))

	hc := cfg.Config.Healthcheck
	if hc == nil {
		t.Fatalf("the image declares no HEALTHCHECK, so Docker can never report this container unhealthy")
	}
	if len(hc.Test) == 0 {
		t.Fatalf("HEALTHCHECK test is empty")
	}
	if hc.Test[0] != "CMD" {
		t.Fatalf("HEALTHCHECK is %q form, want CMD (exec): scratch has no shell for %q", hc.Test[0], hc.Test[0])
	}
	if got, want := hc.Test[1:], []string{"/" + binaryPath, "-healthcheck"}; !equalStrings(got, want) {
		t.Errorf("HEALTHCHECK runs %v, want %v", got, want)
	}
	if hc.Interval <= 0 || hc.Timeout <= 0 || hc.Retries <= 0 {
		t.Errorf("HEALTHCHECK interval=%s timeout=%s retries=%d: each must be set explicitly", hc.Interval, hc.Timeout, hc.Retries)
	}
	if hc.Timeout >= hc.Interval {
		t.Errorf("HEALTHCHECK timeout %s >= interval %s", hc.Timeout, hc.Interval)
	}
	if hc.Timeout <= 2*time.Second {
		t.Errorf("HEALTHCHECK timeout %s does not clear cmd/api's own healthz ping budget", hc.Timeout)
	}
}

// TestRuntimeImageEntrypointIsTheBinary — with no cmd.
func TestRuntimeImageEntrypointIsTheBinary(t *testing.T) {
	requireDocker(t)
	cfg := inspectImage(t, image(t))

	if got, want := cfg.Config.Entrypoint, []string{"/" + binaryPath}; !equalStrings(got, want) {
		t.Errorf("Entrypoint = %v, want %v", got, want)
	}
	if _, ok := cfg.Config.ExposedPorts["8080/tcp"]; !ok {
		t.Errorf("ExposedPorts = %v, want 8080/tcp", cfg.Config.ExposedPorts)
	}
}

// TestRuntimeImageCarriesTheCABundle guards's first scratch compensation as
// an artefact.
func TestRuntimeImageCarriesTheCABundle(t *testing.T) {
	requireDocker(t)
	files, _ := exportImage(t, image(t), "")

	h, ok := files[caBundlePath]
	if !ok {
		t.Fatalf("/%s is not in the image: `scratch` has no roots, so every outbound TLS dial fails with x509: certificate signed by unknown authority", caBundlePath)
	}
	if h.Size < 50_000 {
		t.Errorf("/%s is %d bytes, want a real bundle (>50 KB)", caBundlePath, h.Size)
	}
	if h.Mode&0o004 == 0 {
		t.Errorf("/%s mode %04o is not world-readable, and the container runs as 65532", caBundlePath, h.Mode&0o7777)
	}
}

// TestTheShippedBinaryIsReadableAndExecutableByAnyUser is's second half of
// the numeric-user claim, and it is the half that is easy to miss.
func TestTheShippedBinaryIsReadableAndExecutableByAnyUser(t *testing.T) {
	requireDocker(t)
	files, _ := exportImage(t, image(t), "")

	h, ok := files[binaryPath]
	if !ok {
		t.Fatalf("/%s is not in the image", binaryPath)
	}
	if h.Mode&0o005 != 0o005 {
		t.Errorf("/%s mode %04o: the uid in USER is not the owner, so it needs o+rx", binaryPath, h.Mode&0o7777)
	}
	if h.Size < 1_000_000 {
		t.Errorf("/%s is %d bytes, which is not a Go binary", binaryPath, h.Size)
	}
}

// The layer count is two — the CA bundle and the binary.
func TestRuntimeImageIsScratchAndHasNothingToFallBackOn(t *testing.T) {
	requireDocker(t)
	files, _ := exportImage(t, image(t), "")

	for path, why := range map[string]string{
		"usr/share/zoneinfo": "if this existed, `_ \"time/tzdata\"` in cmd/api would no longer be load-bearing",
		"etc/passwd":         "if this existed, USER could be a name rather than numeric",
		"bin/sh":             "if this existed, HEALTHCHECK could use the shell form",
		"usr/bin/curl":       "if this existed, HEALTHCHECK would not need the binary's own flag",
	} {
		if _, ok := files[path]; ok {
			t.Errorf("/%s is present in the image — %s", path, why)
		}
	}

	cfg := inspectImage(t, image(t))
	if n := len(cfg.RootFS.Layers); n != 2 {
		t.Errorf("the runtime image has %d layers, want 2 (the CA bundle and the binary)", n)
	}
}

// TestTheShippedBinaryEmbedsTheTimezoneDatabase reads the artefact rather
// than cmd/api/main.go's import line.
func TestTheShippedBinaryEmbedsTheTimezoneDatabase(t *testing.T) {
	requireDocker(t)
	_, binary := exportImage(t, image(t), binaryPath)

	if len(binary) == 0 {
		t.Fatalf("could not read /%s out of the image", binaryPath)
	}
	for _, zone := range []string{"Asia/Tokyo", "America/New_York", "Europe/London"} {
		if !bytes.Contains(binary, []byte(zone)) {
			t.Errorf("the shipped binary contains no %q: the embedded IANA database is not linked in, and `scratch` has no /usr/share/zoneinfo to fall back on", zone)
		}
	}
	if n := bytes.Count(binary, []byte("TZif")); n < 100 {
		t.Errorf("the shipped binary holds %d TZif headers, want the whole database (measured: 598)", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
