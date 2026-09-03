// The port's own monopoly: a method nothing reaches through Service.Store
// costs every implementation and every twin a method for nothing.
package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// declaredStoreMethods reads the method set off the Store interface itself, so
// the expectation cannot drift from the declaration it is about.
func declaredStoreMethods(t *testing.T, files map[string]*ast.File) map[string]bool {
	t.Helper()

	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "Store" {
					continue
				}
				iface, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				out := map[string]bool{}
				for _, m := range iface.Methods.List {
					for _, name := range m.Names {
						out[name.Name] = true
					}
				}
				return out
			}
		}
	}
	t.Fatal("no `type Store interface` was found in this package, so the sweep is asserting nothing")
	return nil
}

// reachedStoreMethods collects every s.Store.X call in this package's non-test
// files. Service.Store is the only value of type Store in the tree.
func reachedStoreMethods(files map[string]*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			outer, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			inner, ok := outer.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "Store" {
				return true
			}
			if recv, ok := inner.X.(*ast.Ident); !ok || recv.Name != "s" {
				return true
			}
			out[outer.Sel.Name] = true
			return true
		})
	}
	return out
}

func parsePackageFiles(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/auth: %v", err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = file
	}
	if len(files) == 0 {
		t.Fatal("no non-test files were parsed, so the sweep would pass having read nothing")
	}
	return files
}

func sortedKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestEveryMethodOnTheStorePortIsReachedThroughIt(t *testing.T) {
	files := parsePackageFiles(t)
	declared := declaredStoreMethods(t, files)
	reached := reachedStoreMethods(files)

	if len(reached) == 0 {
		t.Fatal("no s.Store.X call was found at all, so this sweep cannot fail for a reason about the port")
	}

	for _, name := range sortedKeys(declared) {
		if !reached[name] {
			t.Errorf("Store declares %s and nothing in this package calls s.%s.%s — "+
				"every implementation and every twin carries it for no caller",
				name, "Store", name)
		}
	}
	for _, name := range sortedKeys(reached) {
		if !declared[name] {
			t.Errorf("s.Store.%s is called and Store does not declare %s", name, name)
		}
	}
}
