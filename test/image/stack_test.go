// The stack, run. Two claims live here and neither can be reached by anything
// smaller: that `docker compose up` produces a container Docker itself calls
// healthy, and that the named volume survives `down` then `up`.
//
// The volume is the one CLAUDE.md singles out — "the only thing standing
// between `docker compose down && up` and an empty log, and its failure stays
// invisible until a redeploy". VS8's arc is the end-to-end version of this
// through the API; the leg below is the same claim through psql, and it exists
// now rather than at VS8 because the failure it catches destroys production
// data and VS8 is six steps away.
package image_test

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	stackOnce    sync.Once
	stackStarted bool
	volStarted   bool
)

// stack brings the two-service project up once per test binary, on its own
// ports and under its own project name.
func stack(t *testing.T) {
	t.Helper()
	stackOnce.Do(func() {
		env := composeEnv(stackPGPort, stackAPIPort)
		args := compose(t, stackProject, "up", "-d", "--build")
		out, err := runCompose(t, composeTimeout, env, args)
		stackStarted = true
		if err != nil {
			t.Fatalf("bringing the stack up: %v\n%s", err, out)
		}
	})
}

// tearDownStacks runs after m.Run. It removes the volumes too: these projects
// are the tier's own, and leaving a 40 MB pgdata behind after every run is how
// a test suite becomes something people stop running.
func tearDownStacks() {
	root, err := findRepoRoot()
	if err != nil {
		return
	}
	for _, s := range []struct {
		project, pgPort, apiPort string
		started                  bool
	}{
		{stackProject, stackPGPort, stackAPIPort, stackStarted},
		{volumeProject, volumePGPort, stackAPIPort, volStarted},
	} {
		if !s.started {
			continue
		}
		_, _ = execute(composeTimeout, "", composeEnv(s.pgPort, s.apiPort), "docker",
			"compose", "-f", filepath.Join(root, composeRelPath), "-p", s.project,
			"down", "-v", "--remove-orphans")
	}
}

// TestTheStackComesUpAndAnswersHealthz is the leg VS1's own acceptance check
// described — `docker compose up -d && curl -sf localhost:8080/healthz` — and
// which nothing in the repository has ever run automatically.
//
// WHAT IT DOES NOT PROVE, corrected here because the first draft of this
// comment claimed it did: that the numeric user can execute the binary. A
// `COPY --chmod=700` mutation left this leg GREEN — measured, Docker 27.4.0 —
// and the reason is worth carrying. runc still holds the default capability
// set, CAP_DAC_OVERRIDE included, at the moment it execve's the entrypoint, so
// the exec of a 0700 root-owned file succeeds; the caps are then dropped by
// the execve itself, because a non-root euid inherits none without file
// capabilities. Confirmed from the other side: the same image under
// `docker run --cap-drop=ALL` fails with
// `exec: "/api": permission denied`.
//
// So a 0700 binary is a LATENT defect — invisible until somebody hardens the
// deploy with cap_drop — and the only leg in this tier that catches it is the
// probe's open() in container_test.go, which runs after the caps are gone.
func TestTheStackComesUpAndAnswersHealthz(t *testing.T) {
	requireDocker(t)
	stack(t)

	url := "http://127.0.0.1:" + stackAPIPort + "/healthz"
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	var body string
	var code int
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		code, body, lastErr = resp.StatusCode, string(b), nil
		if code == http.StatusOK {
			break
		}
		time.Sleep(time.Second)
	}
	if lastErr != nil {
		t.Fatalf("GET %s never answered: %v\n%s", url, lastErr, composeLogs(t, stackProject, stackPGPort, stackAPIPort))
	}
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %q, want 200\n%s", url, code, body, composeLogs(t, stackProject, stackPGPort, stackAPIPort))
	}
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("GET %s body = %q, want a status of ok", url, body)
	}
}

// TestDockerReportsTheContainerHealthy is the HEALTHCHECK WIRING leg's running
// half. dockerfile_test.go proves the image declares the right probe; this
// proves Docker ran it inside `scratch` — as uid 65532, with no shell — and
// got a zero exit.
//
// The distinction is not academic. Every way of getting this wrong that VS1's
// Dockerfile comment warns about produces the SAME symptom here: a container
// that serves traffic perfectly and never becomes healthy. Compose's
// `depends_on: condition: service_healthy` then waits forever on it.
//
// The 120s deadline is derived rather than picked: interval 5s x retries 12 is
// a 60s worst case from the first probe, and the rest leaves room for a cold
// start. A healthy status with an EMPTY probe log would mean Docker inherited
// health from somewhere rather than running the probe, so the log length is
// asserted too.
func TestDockerReportsTheContainerHealthy(t *testing.T) {
	requireDocker(t)
	stack(t)

	cid := strings.TrimSpace(mustCompose(t, shortTimeout,
		composeEnv(stackPGPort, stackAPIPort),
		compose(t, stackProject, "ps", "-q", "api")))
	if cid == "" {
		t.Fatalf("no api container in project %s", stackProject)
	}

	deadline := time.Now().Add(120 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		status = strings.TrimSpace(run(t, shortTimeout, "docker", "inspect",
			"-f", "{{if .State.Health}}{{.State.Health.Status}}{{else}}NO-HEALTHCHECK{{end}}", cid))
		if status == "healthy" || status == "NO-HEALTHCHECK" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if status == "NO-HEALTHCHECK" {
		t.Fatalf("the running container has no health state at all: nothing in the image declared a HEALTHCHECK, so `depends_on: service_healthy` on this service would never be satisfied")
	}
	if status != "healthy" {
		t.Fatalf("health status = %q after 120s, want healthy\nhealth log:\n%s", status, run(t, shortTimeout, "docker", "inspect", "-f", "{{json .State.Health.Log}}", cid))
	}

	log := run(t, shortTimeout, "docker", "inspect", "-f", "{{len .State.Health.Log}}", cid)
	if strings.TrimSpace(log) == "0" {
		t.Errorf("health status is healthy but no probe has ever run")
	}
}

// TestTheNamedVolumeSurvivesDownAndUp writes a row, takes the stack down the
// way a redeploy does — `down`, with no -v — brings it back and reads the row.
//
// It runs in its OWN compose project, because it has to call `down` and a
// shared stack cannot be pulled out from under the other legs. That is also
// what makes it safe beside a developer's `make up`: the volume it creates and
// destroys is travellog-imagetest-vol_pgdata.
//
// Three things in the body are decisions rather than steps:
//
//   - The probe table is this tier's own and is dropped first, so a rerun
//     starts clean.
//   - THE VOLUME MUST BE NAMED, and named after the project. An anonymous
//     volume also survives `down` on some compose versions and is garbage the
//     moment anybody runs `docker volume prune`.
//   - The redeploy is `down` and NOT `down -v`: the -v is the operator error
//     this design tolerates, not the one it must survive.
//
// The final read is soft on purpose. When the volume is gone the database
// comes up EMPTY rather than wrong, so psql fails with `relation
// "volume_probe" does not exist` — and a t.Fatal carrying a psql error is a
// worse report than the sentence that names what the failure costs.
func TestTheNamedVolumeSurvivesDownAndUp(t *testing.T) {
	requireDocker(t)

	env := composeEnv(volumePGPort, stackAPIPort)
	up := func() {
		t.Helper()
		volStarted = true
		out, err := runCompose(t, composeTimeout, env,
			compose(t, volumeProject, "up", "-d", "--wait", "postgres"))
		if err != nil {
			t.Fatalf("bringing postgres up: %v\n%s", err, out)
		}
	}
	psql := func(sql string) string {
		t.Helper()
		out, err := runCompose(t, composeTimeout, env,
			compose(t, volumeProject, "exec", "-T", "postgres",
				"psql", "-U", "travellog", "-d", "travellog", "-tAc", sql))
		if err != nil {
			t.Fatalf("psql %q: %v\n%s", sql, err, out)
		}
		return strings.TrimSpace(out)
	}

	up()

	psql(`DROP TABLE IF EXISTS volume_probe`)
	psql(`CREATE TABLE volume_probe (note text)`)
	marker := fmt.Sprintf("survives-%d", time.Now().UnixNano())
	psql(fmt.Sprintf(`INSERT INTO volume_probe VALUES ('%s')`, marker))
	if got := psql(`SELECT note FROM volume_probe`); got != marker {
		t.Fatalf("the row did not even survive the insert: %q", got)
	}

	want := volumeProject + "_pgdata"
	if out := run(t, shortTimeout, "docker", "volume", "ls", "--format", "{{.Name}}"); !strings.Contains(out, want) {
		t.Errorf("no volume named %q; compose declared no named volume for postgres data:\n%s", want, out)
	}

	out, err := runCompose(t, composeTimeout, env, compose(t, volumeProject, "down"))
	if err != nil {
		t.Fatalf("taking the stack down: %v\n%s", err, out)
	}

	up()

	out, err = runCompose(t, composeTimeout, env,
		compose(t, volumeProject, "exec", "-T", "postgres",
			"psql", "-U", "travellog", "-d", "travellog", "-tAc", `SELECT note FROM volume_probe`))
	got := strings.TrimSpace(out)
	if err != nil {
		got = fmt.Sprintf("unreadable (%v: %s)", err, got)
	}
	if got != marker {
		t.Fatalf("after down and up the row is %q, want %q: the database directory did not survive the restart, and a redeploy would destroy every trip in the log", got, marker)
	}
}

func composeLogs(t *testing.T, project, pgPort, apiPort string) string {
	t.Helper()
	out, _ := runCompose(t, shortTimeout, composeEnv(pgPort, apiPort),
		compose(t, project, "logs", "--tail", "40"))
	return out
}
