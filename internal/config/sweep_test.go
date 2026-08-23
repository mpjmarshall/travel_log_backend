// The os.Getenv monopoly, as a MECHANISM rather than a discipline.
//
// go_backend.md L30: "Read all configuration (DB DSN, ports) strictly via
// os.Getenv(). No third-party config managers in the Go code." VS2's step text
// makes internal/config the only package allowed to make that call.
//
// WHY THIS IS NOT THE STEP'S `grep -rn 'os.Getenv' --include='*.go' .`. That
// grep matches its OWN source — the pattern is in the file doing the searching
// the moment the check is written down — and it matches this comment, and a
// comment mentioning os.Getenv in an unrelated file would fail it. It also
// cannot see os.LookupEnv, which reads the same environment and is a one-word
// bypass. This walks the AST instead: it sees calls, not text, and it asserts
// the set of callers is EXACTLY internal/config/config.go — so it fails both
// when a second file starts reading the environment AND when config.go stops.
package config_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's working directory to the directory
// holding go.mod. Relative "../.." would be a fact about where this file
// happens to sit today.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// walkSource calls fn for every non-test .go file in the module, with its path
// relative to the module root and its parsed AST.
//
// Test files are EXCLUDED, and that is a decision rather than convenience: a
// test helper reading TEST_DATABASE_URL is not application configuration, and
// VS5's internal/store/testdb does exactly that by design. Application code is
// what L30 governs.
func walkSource(t *testing.T, fn func(rel string, file *ast.File)) {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// environmentReaders returns every non-test file calling into the process
// environment, by any of the four names that reach it.
//
// A file that renames the os package, or shadows it, is out of scope: the sweep
// reads what a reviewer reads.
func environmentReaders(t *testing.T) []string {
	t.Helper()
	readers := map[string]bool{}

	walkSource(t, func(rel string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			switch sel.Sel.Name {
			case "Getenv", "LookupEnv", "Environ", "ExpandEnv":
				readers[rel] = true
			}
			return true
		})
	})

	out := make([]string, 0, len(readers))
	for f := range readers {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// environmentExemptions is the one file outside internal/config that reads the
// environment, with the reason it may.
//
// CORRECTED AT VS4. The comment on walkSource above says test files are
// excluded "and VS5's internal/store/testdb does exactly that by design" —
// but testdb.go is NOT a test file. It is an ordinary package, imported by
// other packages' tests, and the exclusion its own comment cited never covered
// it. The sweep therefore went red at VS4 against correct work:
//
//	files reading the environment = [internal/config/config.go
//	internal/postgres/testdb/testdb.go], want exactly [internal/config/config.go]
//
// The premise, not the sweep, was what was wrong. The exemption is NAMED, and
// the assertion below is EQUALITY, so a second exemption has to be added here
// and argue for itself — the same shape internal/httpx's wireCodeExemptions
// already uses, and keyed on the file path rather than on a line number.
//
// It is also NOT a hole in what L30 governs. TEST_DATABASE_URL is not
// application configuration: no build reads it, the binary never sees it, and
// the one function that does read it exists to SKIP a test when it is unset.
var environmentExemptions = map[string]string{
	"internal/postgres/testdb/testdb.go": "TEST_DATABASE_URL: the test seam, not application configuration",
}

func TestInternalConfigIsTheOnlyPackageThatReadsTheEnvironment(t *testing.T) {
	want := []string{"internal/config/config.go"}
	for f := range environmentExemptions {
		want = append(want, f)
	}
	sort.Strings(want)

	got := environmentReaders(t)

	if len(got) != len(want) {
		t.Fatalf("files reading the environment = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("files reading the environment = %v, want %v", got, want)
			break
		}
	}
}

// The exemption list is not allowed to grow by accident, and a list nobody
// checks against reality is how one entry survives the file it named.
func TestEveryEnvironmentExemptionStillExists(t *testing.T) {
	root := moduleRoot(t)
	for f, why := range environmentExemptions {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s is exempted (%s) and is not in the tree: %v", f, why, err)
		}
	}
	if len(environmentExemptions) != 1 {
		t.Errorf("there are %d environment exemptions; there was one at VS4, and a second needs its own argument",
			len(environmentExemptions))
	}
}
