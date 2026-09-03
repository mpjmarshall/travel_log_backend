// Mount's guards, as a mechanism rather than as a discipline: a field added to
// Deps and not guarded is a nil that reaches a handler.
package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// unguardedDeps names the fields Mount deliberately does not check, with the
// reason each is safe to leave unset.
var unguardedDeps = map[string]string{
	"Now": "the clock is optional by design — Deps.Clock() answers time.Now when it is nil",
}

func TestEveryFieldOnDepsIsGuardedByMount(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "auth_handlers.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing auth_handlers.go: %v", err)
	}

	fields := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Deps" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				fields[name.Name] = true
			}
		}
		return true
	})
	if len(fields) == 0 {
		t.Fatal("no Deps struct was found, so this sweep is asserting nothing")
	}

	guarded := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Mount" {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			stmt, ok := inner.(*ast.IfStmt)
			if !ok {
				return true
			}
			cond, ok := stmt.Cond.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			sel, ok := cond.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "deps" {
				guarded[sel.Sel.Name] = true
			}
			return true
		})
		return false
	})
	if len(guarded) == 0 {
		t.Fatal("Mount checks no deps field at all, so this sweep cannot fail for a reason about wiring")
	}

	missing := []string{}
	for name := range fields {
		if guarded[name] {
			continue
		}
		if _, exempt := unguardedDeps[name]; exempt {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Mount guards no nil for %s — a caller that leaves one unset wires an "+
			"API that panics on the first request that reaches it, and the panic names "+
			"a line in a handler rather than the field nobody set",
			strings.Join(missing, ", "))
	}

	for name := range unguardedDeps {
		if !fields[name] {
			t.Errorf("unguardedDeps exempts %s and Deps has no such field", name)
		}
	}
}

// depsHolders are the two functions that are allowed to see the whole bag:
// the table that projects it, and the wiring that validates it.
var depsHolders = map[string]bool{"Routes": true, "Mount": true}

func TestNoHandlerTakesTheWholeDepsBag(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/httpapi: %v", err)
	}
	fset := token.NewFileSet()

	var takers []string
	read := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		read++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || depsHolders[fn.Name.Name] {
				continue
			}
			for _, param := range fn.Type.Params.List {
				if ident, ok := param.Type.(*ast.Ident); ok && ident.Name == "Deps" {
					takers = append(takers, name+":"+fn.Name.Name)
				}
			}
		}
	}
	if read == 0 {
		t.Fatal("no non-test file was parsed, so this sweep would pass having read nothing")
	}
	sort.Strings(takers)
	if len(takers) > 0 {
		t.Errorf("%d function(s) take the whole Deps bag — %s — so each learns eighteen "+
			"fields to reach the two it uses, and a test of one constructs all eighteen",
			len(takers), strings.Join(takers, ", "))
	}
}
