// The stack, run.
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

// stack brings's three-service project up once per test binary, on its own
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

// tearDownStacks runs after m.Run.
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

// TestTheStackComesUpAndAnswersHealthz is the leg's own acceptance check
// described.
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

// TestDockerReportsTheContainerHealthy is the healthcheck wiring leg's
// running half.
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
// way a redeploy does.
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
