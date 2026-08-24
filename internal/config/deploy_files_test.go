// The deployment files, against the variables this package reads.
//
// ARTEFACT TIER, AND LABELLED AS ONE. Nothing here can fail because the code is
// wrong; it fails when a variable is added to internal/config and not to the
// files that have to set it. That is a real regression and a cheap one to
// catch, and it is not evidence that anything works — see CLAUDE.md on what an
// artefact check can and cannot find.
//
// THE README ALREADY CLAIMED THIS TEST EXISTED. It said "deploy/.env.example is
// the template, and a test asserts it lists everything the config package
// reads", and no such test was in the tree — measured with
// `grep -rn 'env.example' --include='*_test.go' .`, which matched three
// comments and nothing executable. The claim was also not true as written:
// DATABASE_URL and PORT are read by this package and are deliberately NOT in
// .env.example, because compose composes DATABASE_URL out of the POSTGRES_*
// variables and pins PORT to the port the container publishes. So the guard is
// written in the two halves that ARE true:
//
//  1. every variable this package reads is set by the api service in
//     deploy/docker-compose.yml — the file that actually has to supply them;
//  2. every variable compose interpolates from the environment is documented in
//     deploy/.env.example — which is what makes that file the template.
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
// indentation. Reading the whole file instead would let a variable set on the
// POSTGRES service satisfy a claim about the API's.
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

// interpolated is every ${VAR} and ${VAR:-default} in the compose file.
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
