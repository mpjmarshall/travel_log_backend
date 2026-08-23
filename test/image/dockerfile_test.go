// What the runtime image IS, read off the built artefact rather than off the
// Dockerfile's text. A grep of deploy/Dockerfile can only prove that somebody
// typed a line; these legs prove the line survived the build — which is a
// different claim, and the one that matters when a base image, a COPY path or
// a build flag changes underneath it.
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

// The four things `scratch` does not supply, per deploy/Dockerfile's own
// comment. These constants are the paths those compensations land at.
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

// exportImage lists every path in the image's filesystem and returns the bytes
// of one of them.
//
// `docker export` of a created-but-never-started container is the only way to
// see inside `scratch` from the host: there is no shell to exec, and `docker
// cp` needs a path you already know. Note that export adds the runtime's own
// mounts — .dockerenv, /dev, /etc/hosts, /proc, /sys — so absence assertions
// below name specific paths rather than claiming an exact inventory.
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
		// Directories arrive with a trailing slash and files without, so the
		// key is normalised. MEASURED, not tidied: without this the absence
		// assertions below looked for "usr/share/zoneinfo" while the export
		// held "usr/share/zoneinfo/", and a mutation that COPYed the whole
		// zone database into the image left them green. The layer count is
		// what caught it.
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

// TestRuntimeImageRunsAsANumericNonRootUser guards the third of the four
// scratch compensations. The NUMERIC half is the part that cannot be inferred
// from "non-root": scratch has no /etc/passwd, so `USER nonroot` builds
// happily and fails at container start with "unable to find user nonroot".
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
	// Numeric on both sides of the colon. A name here resolves against an
	// /etc/passwd this image does not have.
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

// TestRuntimeImageHealthcheckInvokesTheBinarysOwnFlag is the HEALTHCHECK
// WIRING leg, as distinct from the -healthcheck flag it invokes — which VS1's
// backfill already covers with eleven unit legs. The wiring is what turns a
// working flag into a health status, and nothing in `go test` can see it.
//
// The exec form is the load-bearing half. A HEALTHCHECK written as a bare
// string becomes ["CMD-SHELL", …] and needs /bin/sh, which scratch does not
// have — it would build, ship, and report every container unhealthy forever.
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
	// A timeout at or above the interval overlaps probes rather than spacing
	// them, and a probe that never finishes before the next one starts turns a
	// slow dependency into a permanently pending health status.
	if hc.Timeout >= hc.Interval {
		t.Errorf("HEALTHCHECK timeout %s >= interval %s", hc.Timeout, hc.Interval)
	}
	// The flag's own budget. deploy/Dockerfile documents four budgets that
	// must nest, innermost first: /healthz's database ping < probe's request
	// < this timeout < this interval. Only the outer two relations are
	// asserted here — the inner two are constants in cmd/api, another
	// package's to change, and a leg that pins them from here would break on
	// a correct edit. What is guarded is that this timeout leaves room for
	// the innermost budget at all.
	if hc.Timeout <= 2*time.Second {
		t.Errorf("HEALTHCHECK timeout %s does not clear cmd/api's own healthz ping budget", hc.Timeout)
	}
}

// TestRuntimeImageEntrypointIsTheBinary — with no CMD, so no argument survives
// from a base image and `docker run <image>` starts the server rather than a
// shell.
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

// TestRuntimeImageCarriesTheCABundle guards the first scratch compensation as
// an artefact: the file is there, it is not empty, and every user can read it.
// That last part is not decoration — the bundle is copied from a stage where
// it is owned by root, and this container runs as 65532, so a 0600 bundle
// would be invisible to the process that needs it. Whether it FUNCTIONS is a
// separate leg, in container_test.go, and it runs the container.
func TestRuntimeImageCarriesTheCABundle(t *testing.T) {
	requireDocker(t)
	files, _ := exportImage(t, image(t), "")

	h, ok := files[caBundlePath]
	if !ok {
		t.Fatalf("/%s is not in the image: `scratch` has no roots, so every outbound TLS dial fails with x509: certificate signed by unknown authority", caBundlePath)
	}
	// A real Debian bundle is ~220 KB. The floor is deliberately far below
	// that and far above zero: what it rejects is a COPY that produced an
	// empty or truncated file.
	if h.Size < 50_000 {
		t.Errorf("/%s is %d bytes, want a real bundle (>50 KB)", caBundlePath, h.Size)
	}
	if h.Mode&0o004 == 0 {
		t.Errorf("/%s mode %04o is not world-readable, and the container runs as 65532", caBundlePath, h.Mode&0o7777)
	}
}

// TestTheShippedBinaryIsReadableAndExecutableByAnyUser is the second half of
// the numeric-USER claim, and it is the half that is easy to miss: a correct
// USER line in front of a 0700 root-owned binary is a container that exits
// immediately with "permission denied" and no other clue.
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

// TestRuntimeImageIsScratchAndHasNothingToFallBackOn measures the PREMISE the
// other three compensations rest on. Every one of them is only load-bearing
// because these paths are absent; if a base image ever supplied them, the
// Dockerfile's comment would be describing a different image and this leg is
// what says so.
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

	// Two layers: the CA bundle and the binary. Anything more means stage 2
	// gained a base image or another COPY, which is the change that quietly
	// invalidates the absences above.
	cfg := inspectImage(t, image(t))
	if n := len(cfg.RootFS.Layers); n != 2 {
		t.Errorf("the runtime image has %d layers, want 2 (the CA bundle and the binary)", n)
	}
}

// TestTheShippedBinaryEmbedsTheTimezoneDatabase reads the artefact rather than
// cmd/api/main.go's import line, because the import can be present and the
// linker can still drop it — and because the source is another agent's file.
//
// The signature is the embedded zoneinfo.zip's own entry names, which a zip
// stores uncompressed in both the local header and the central directory.
// Measured on this image: "Asia/Tokyo" ×2, "America/New_York" ×2, "TZif" ×598.
// The negative control is in container_test.go, where the SAME image is shown
// to have no zoneinfo on disk at all.
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
	// TZif is the magic at the head of every compiled zone file. One or two
	// could be an accident of some other string; six hundred is the database.
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
