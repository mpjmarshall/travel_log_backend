// What the runtime image DOES, measured by running something inside it.
//
// dockerfile_test.go proves the Dockerfile's lines survived the build. These
// legs prove the result works, which is a different claim: a CA bundle can be
// present and unreadable, a USER can be numeric and unable to execute the
// binary, and a timezone database can be embedded in a binary that never asks
// for one.
//
// THE PROBE IS THE MECHANISM, and it exists because `scratch` cannot be
// inspected from the inside: no shell, no ls, nothing to exec but /api itself,
// and /api takes no argument that would report any of this. So a tiny Go
// program is cross-compiled for the daemon's platform, layered onto THE IMAGE
// UNDER TEST with a two-line Dockerfile, and run. It inherits that image's
// filesystem and its USER, so what it reports is a fact about the real image
// and not about a lookalike.
//
// The probe is a string constant rather than a package under test/, on
// purpose. `go build ./...` — the literal first command of `make check` —
// writes the executable of a single main package into the current directory,
// which is why CLAUDE.md has a paragraph about ./api being git-ignored. A
// SECOND main package in the module would put a second binary there every time
// anybody ran the gate.
package image_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// probeSource is compiled twice: once as written, and once with TZIMPORT
// replaced by the blank import. The pair is the negative control for the
// timezone claim — see TestScratchWithoutEmbeddedTzdataCannotResolveAZone.
const probeSource = `package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"
__IMPORTS__)

func main() {
	fmt.Printf("uid=%d\n", os.Getuid())
	fmt.Printf("gid=%d\n", os.Getgid())

	if _, err := time.LoadLocation("Asia/Tokyo"); err != nil {
		fmt.Printf("tokyo=err:%v\n", err)
	} else {
		fmt.Printf("tokyo=ok\n")
	}

	if _, err := os.Stat("/usr/share/zoneinfo"); err != nil {
		fmt.Printf("zoneinfodir=missing\n")
	} else {
		fmt.Printf("zoneinfodir=present\n")
	}

	if pool, err := x509.SystemCertPool(); err != nil {
		fmt.Printf("certpool=err:%v\n", err)
	} else {
		fmt.Printf("certpool=%d\n", len(pool.Subjects()))
	}

	f, err := os.Open("/api")
	if err != nil {
		fmt.Printf("apiread=err:%v\n", err)
	} else {
		b := make([]byte, 64)
		n, rerr := f.Read(b)
		if rerr != nil {
			fmt.Printf("apiread=err:%v\n", rerr)
		} else {
			fmt.Printf("apiread=ok:%d\n", n)
		}
		f.Close()
	}
	if st, err := os.Stat("/api"); err == nil {
		fmt.Printf("apimode=%04o\n", st.Mode().Perm())
	}

	if len(os.Args) > 1 {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", os.Args[1], nil)
		if err != nil {
			fmt.Printf("tls=err:%v\n", err)
		} else {
			conn.Close()
			fmt.Printf("tls=ok\n")
		}
	}
}
`

// probeReport is the parsed key=value output of one probe run.
type probeReport map[string]string

func (p probeReport) get(t *testing.T, key string) string {
	t.Helper()
	v, ok := p[key]
	if !ok {
		t.Fatalf("the probe reported no %q (it printed: %v)", key, map[string]string(p))
	}
	return v
}

var (
	probeOnce sync.Once
	probeTags map[bool]string // embedTzdata -> image tag
	probeDir  string
)

// probeImage builds (once) the probe images layered on the image under test.
func probeImage(t *testing.T, embedTzdata bool) string {
	t.Helper()
	base := image(t)

	probeOnce.Do(func() {
		probeTags = map[bool]string{}
		probeDir = t.TempDir()

		arch := strings.TrimSpace(run(t, shortTimeout, "docker", "version", "--format", "{{.Server.Arch}}"))
		if arch == "" {
			t.Fatalf("could not read the daemon's architecture")
		}

		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(probeDir, name), []byte(body), 0o644); err != nil {
				t.Fatalf("writing %s: %v", name, err)
			}
		}
		write("go.mod", "module probe\n\ngo 1.25.0\n")
		write("Dockerfile", "ARG BASE\nFROM ${BASE}\nARG PROBE\nCOPY ${PROBE} /probe\nENTRYPOINT [\"/probe\"]\n")

		for _, embed := range []bool{false, true} {
			imports := ""
			if embed {
				// The exact line cmd/api/main.go carries, and the only
				// difference between the two binaries.
				imports = "\n\t_ \"time/tzdata\"\n"
			}
			src := strings.Replace(probeSource, "__IMPORTS__", imports, 1)
			write("main.go", src)

			bin := "probe-notz"
			tag := "travellog-imagetest-probe:notz"
			if embed {
				bin, tag = "probe-tz", "travellog-imagetest-probe:tz"
			}

			// GOOS/GOARCH are the whole reason the daemon's architecture is
			// read above: the host is darwin/arm64 and the container is
			// linux/arm64, and a probe built for the host produces
			// "exec /probe: exec format error" — measured, on the first run.
			// CGO_ENABLED=0 for the same reason deploy/Dockerfile sets it:
			// scratch has no libc.
			buildEnv := append(os.Environ(),
				"GOOS=linux",
				"GOARCH="+arch,
				"CGO_ENABLED=0",
			)
			out, err := execute(buildTimeout, probeDir, buildEnv, "go", "build",
				"-o", filepath.Join(probeDir, bin), ".")
			if err != nil {
				t.Fatalf("cross-compiling the probe (embed=%v): %v\n%s", embed, err, out)
			}
			probeTags[embed] = tag

			out, err = runIn(probeDir, buildTimeout, "docker", "build",
				"--build-arg", "BASE="+base,
				"--build-arg", "PROBE="+bin,
				"-t", tag, probeDir)
			if err != nil {
				t.Fatalf("building the probe image (embed=%v): %v\n%s", embed, err, out)
			}
		}
	})

	tag, ok := probeTags[embedTzdata]
	if !ok {
		t.Fatalf("no probe image for embedTzdata=%v", embedTzdata)
	}
	return tag
}

// runProbe runs a probe image and parses its report.
func runProbe(t *testing.T, embedTzdata bool, args ...string) probeReport {
	t.Helper()
	argv := append([]string{"run", "--rm", probeImage(t, embedTzdata)}, args...)
	out := run(t, composeTimeout, "docker", argv...)

	report := probeReport{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			report[k] = v
		}
	}
	if len(report) == 0 {
		t.Fatalf("the probe printed nothing parseable:\n%s", out)
	}
	return report
}

// TestTheContainerProcessRunsAsTheNumericUser is the RUNNING half of the USER
// claim. `docker image inspect` proves the Dockerfile said 65532:65532; this
// proves the kernel gave the process that uid, which is what would fail if the
// value were a name scratch cannot resolve.
func TestTheContainerProcessRunsAsTheNumericUser(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, true)

	for _, key := range []string{"uid", "gid"} {
		got := r.get(t, key)
		n, err := strconv.Atoi(got)
		if err != nil {
			t.Fatalf("%s = %q, want a number", key, got)
		}
		if n == 0 {
			t.Errorf("%s = 0: the container is running as root", key)
		}
		if n != 65532 {
			t.Errorf("%s = %d, want 65532 (deploy/Dockerfile's USER)", key, n)
		}
	}
}

// TestTheShippedBinaryIsReadableByTheUserItRunsAs opens /api from inside the
// image, as 65532, and it is the ONLY leg in this tier that can see a
// permission defect on the binary.
//
// That is not the arrangement this file started with. The stack coming up was
// supposed to be the executable half, and a `COPY --chmod=700` mutation left
// that leg green: runc execve's the entrypoint while it still holds
// CAP_DAC_OVERRIDE from the default capability set, and the caps vanish in the
// execve because a non-root euid inherits none. This probe runs AFTER that
// point, which is why its open() reports `permission denied` on the same
// image that boots fine. The full measurement is in stack_test.go.
func TestTheShippedBinaryIsReadableByTheUserItRunsAs(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, true)

	if got := r.get(t, "apiread"); !strings.HasPrefix(got, "ok:") {
		t.Errorf("opening /api as uid 65532: %s", got)
	}
	if got := r.get(t, "apimode"); got != "0755" {
		t.Errorf("/api mode = %s, want 0755", got)
	}
}

// TestScratchWithoutEmbeddedTzdataCannotResolveAZone is the measurement
// VS1-BACKFILL could not make. It compiled a program with and without
// `_ "time/tzdata"` ON MACOS and got four identical answers, because
// /usr/share/zoneinfo and /var/db/timezone/zoneinfo both exist there and the
// embedded database is consulted only after every filesystem source fails. It
// recorded a test_strategy of "none" for exactly that reason.
//
// Inside `scratch` the filesystem sources are gone, so the same experiment
// finally separates. This leg is the NEGATIVE half: a binary without the
// import cannot load Asia/Tokyo in this image. If it ever passes silently —
// because a base image gained zoneinfo — the import stops being load-bearing
// and the next leg stops proving anything, so this one is not decoration.
func TestScratchWithoutEmbeddedTzdataCannotResolveAZone(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, false)

	if got := r.get(t, "zoneinfodir"); got != "missing" {
		t.Errorf("/usr/share/zoneinfo is %s in the runtime image", got)
	}
	got := r.get(t, "tokyo")
	if got == "ok" {
		t.Fatalf("a binary WITHOUT `_ \"time/tzdata\"` loaded Asia/Tokyo inside this image, so the import in cmd/api is no longer what makes timezones work — something is supplying zoneinfo on disk")
	}
	if !strings.Contains(got, "unknown time zone") {
		t.Errorf("tokyo = %q, want an unknown-time-zone error", got)
	}
}

// TestEmbeddedTzdataResolvesInsideScratch is the positive half: the same
// program, in the same image, with the one import cmd/api/main.go carries.
// Measured together with the leg above, this is the whole of the tzdata claim
// — the image has no zone files, and the embedded database is what answers.
func TestEmbeddedTzdataResolvesInsideScratch(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, true)

	if got := r.get(t, "tokyo"); got != "ok" {
		t.Errorf("LoadLocation(\"Asia/Tokyo\") inside the runtime image: %s", got)
	}
}

// TestTheCABundleGivesTheContainerARealRootStore is the FUNCTIONAL half of the
// CA claim. x509.SystemCertPool is what crypto/tls consults, and on Linux it
// reads exactly the path the Dockerfile copies — so an empty pool here is the
// shape of every outbound TLS dial failing with "unknown authority".
//
// Measured on this image: 150 roots. The floor is 100.
func TestTheCABundleGivesTheContainerARealRootStore(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, true)

	got := r.get(t, "certpool")
	n, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("certpool = %q, want a count", got)
	}
	if n < 100 {
		t.Errorf("x509.SystemCertPool() holds %d roots inside the image, want the Debian bundle (measured: 150). Outbound TLS has nothing to verify against", n)
	}
}

// TestOutboundTLSVerifiesAgainstTheCopiedBundle is the strongest form of the
// CA claim — a real handshake, verified against the real roots — and it is
// also the only leg here that needs the internet.
//
// The failure classification is the point: a dial that cannot resolve or
// connect SKIPS, because that is a fact about the network. A dial that
// connects and then fails to VERIFY is a hard failure, because that is the
// defect this leg exists for.
func TestOutboundTLSVerifiesAgainstTheCopiedBundle(t *testing.T) {
	requireDocker(t)
	r := runProbe(t, true, "proxy.golang.org:443")

	got := r.get(t, "tls")
	switch {
	case got == "ok":
		return
	case strings.Contains(got, "x509") || strings.Contains(got, "certificate"):
		t.Fatalf("the handshake reached the server and failed to verify it: %s", got)
	default:
		t.Skipf("no route to proxy.golang.org from the container, so the handshake proves nothing either way: %s", got)
	}
}
