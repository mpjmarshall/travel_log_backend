// The deployment files, against the variables this package reads.
package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func readDeployFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "deploy", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// composeAPIEnvironment is the api service's `environment:` block, by
// indentation.
func composeAPIEnvironment(t *testing.T) string {
	t.Helper()
	compose := readDeployFile(t, "docker-compose.yml")
	start := strings.Index(compose, "\n    environment:\n")
	if start < 0 {
		t.Fatal("deploy/docker-compose.yml has no api environment block")
	}
	rest := compose[start+len("\n    environment:\n"):]
	end := strings.Index(rest, "\n    ports:")
	if end < 0 {
		t.Fatal("the api environment block does not end at `ports:`")
	}
	return rest[:end]
}

func TestComposeSetsEveryVariableTheConfigPackageReads(t *testing.T) {
	env := composeAPIEnvironment(t)

	for _, name := range allVars {
		if !strings.Contains(env, name+":") {
			t.Errorf("deploy/docker-compose.yml does not set %s on the api service.\n"+
				"    config.Load refuses to start without it, so the container would come\n"+
				"    up, fail to load its config, and exit 2 — which is the design working\n"+
				"    and a stack that does not run.", name)
		}
	}
}

// interpolated is every ${var} and ${var:-default} in the compose file.
var interpolated = regexp.MustCompile(`\$\{([A-Z0-9_]+)`)

func TestEveryComposeOverrideIsDocumentedInTheEnvTemplate(t *testing.T) {
	compose := readDeployFile(t, "docker-compose.yml")
	template := readDeployFile(t, ".env.example")

	seen := map[string]bool{}
	var missing []string
	for _, match := range interpolated.FindAllStringSubmatch(compose, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if !strings.Contains(template, "\n"+name+"=") && !strings.HasPrefix(template, name+"=") {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("deploy/docker-compose.yml reads %v from the environment and\n"+
			"    deploy/.env.example does not document them. The template is the only\n"+
			"    place a knob is written down; one that is not there is one nobody\n"+
			"    knows they can turn.", missing)
	}
}

// readsVariable finds every environment variable named in Load's body, so
// this list cannot drift from the one the package actually reads.
var readsVariable = regexp.MustCompile(`"([A-Z][A-Z0-9_]{2,})"`)

func variablesLoadReads(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "internal", "config", "config.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	body := string(raw)
	start := strings.Index(body, "func Load() (Config, error) {")
	if start < 0 {
		t.Fatal("internal/config/config.go has no func Load")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of func Load")
	}

	seen := map[string]bool{}
	var names []string
	for _, m := range readsVariable.FindAllStringSubmatch(body[start:start+end], -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("found no environment variables in Load, so this test proves nothing")
	}
	sort.Strings(names)
	return names
}

func TestComposeSetsEveryVariableLoadReads(t *testing.T) {
	env := composeAPIEnvironment(t)

	for _, name := range variablesLoadReads(t) {
		if !strings.Contains(env, name+":") {
			t.Errorf("config.Load reads %s and deploy/docker-compose.yml does not set\n"+
				"    it on the api service. A variable read through os.Getenv rather than\n"+
				"    the loader is one no required-variable list mentions, so the stack\n"+
				"    starts and behaves as though it were unset.", name)
		}
	}
}
