// The os.Getenv monopoly, as a mechanism rather than a discipline.
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
// holding go.mod.
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

// walkSource calls fn for every non-test.go file in the module, with its
// path relative to the module root and its parsed ast.
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

// environmentExemptions is the one file outside internal/config that reads
// the environment, with the reason it may.
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
