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

// composeServiceBlock is one service's own YAML, by indentation — the same
// device composeAPIEnvironment uses, one level out.
func composeServiceBlock(t *testing.T, service string) string {
	t.Helper()
	compose := readDeployFile(t, "docker-compose.yml")
	head := "\n  " + service + ":\n"
	start := strings.Index(compose, head)
	if start < 0 {
		t.Fatalf("deploy/docker-compose.yml declares no %s service", service)
	}
	rest := compose[start+len(head):]
	if end := regexp.MustCompile(`(?m)^  [a-z][a-z0-9_-]*:`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	if strings.TrimSpace(rest) == "" {
		t.Fatalf("the %s service block is empty, so this leg would measure nothing", service)
	}
	return rest
}

// interpolatedDefault is `${VAR:-default}`, which is the only shape compose
// takes a default in.
var interpolatedDefault = regexp.MustCompile(`\$\{([A-Z0-9_]+):-([^}]*)\}`)

func composeDefaults(t *testing.T, block string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range interpolatedDefault.FindAllStringSubmatch(block, -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 20 {
		t.Fatalf("parsed %d defaults out of the api service, expected at least 20 — "+
			"the parse is wrong, so every assertion below would pass while "+
			"measuring nothing", len(out))
	}
	return out
}

// THE VALUE AND NOT THE PRESENCE. The three legs above assert every variable is
// SET; this asserts what it is set TO, which nothing did.
func TestTheShippedDefaultsAreWhatTheyAreDocumentedToBe(t *testing.T) {
	defaults := composeDefaults(t, composeServiceBlock(t, "api"))

	for _, c := range []struct{ name, want, matters string }{
		{"S3_PRESIGN_TTL_PUBLIC", "15m",
			"four sentences of client copy are written against fifteen minutes, and " +
				"a public share envelope embeds these URLs for anyone holding the link"},
		{"S3_PRESIGN_TTL_PRIVATE", "2m",
			"the revocation window: a read capability the phone minted outlives a " +
				"revoked session by exactly this long"},
		{"ADMIN_COOKIE_INSECURE", "0",
			"1 drops Secure from the admin panel's session cookie, and the panel is " +
				"a second principal over every traveller's log"},
		{"MAIL_LOG_SENDER", "0",
			"1 writes every sign-in code to the container log, where anyone who can " +
				"read logs can sign in as anyone"},
		{"AUTH_RATE_LIMIT_PER_MIN", "10",
			"the only bound on unauthenticated work at the credential routes"},
		{"TRAVELLER_RATE_LIMIT_PER_MIN", "600",
			"the ceiling a stolen token meets"},
		{"PUBLIC_RATE_LIMIT_PER_MIN", "120",
			"the public share read's own bucket, and the only bound on a route with " +
				"no identity at all"},
		{"MEDIA_MAX_BYTES", "26214400",
			"1<<20 refuses an ordinary 5 MB photograph and the suite stays green"},
		{"REQUEST_TIMEOUT", "15s",
			"1s makes every sign-in a 503"},
		{"BIND_HOST", "127.0.0.1",
			"0.0.0.0 publishes the API to the whole LAN, and the deploy is " +
				"loopback-bound by decision"},
	} {
		got, held := defaults[c.name]
		if !held {
			t.Errorf("deploy/docker-compose.yml sets no default for %s on the api "+
				"service.\n    It matters because %s.", c.name, c.matters)
			continue
		}
		if got != c.want {
			t.Errorf("%s defaults to %q, want %q.\n    It matters because %s.",
				c.name, got, c.want, c.matters)
		}
	}
}
