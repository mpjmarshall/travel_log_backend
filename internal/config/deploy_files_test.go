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
