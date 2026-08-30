// the ban, inside `make check` rather than inside A DOCUMENT.
package media_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two calls that sign `host` and nothing else.
var banned = map[string]string{
	"Presigned" + "PutObject": "signs host only; the digest, length, type and " +
		"write-once are all outside the signature, so a lying client writes " +
		"arbitrary bytes at a content address and every later reader trusts it",
	"Presi" + "gn": "the generic form of the same hole — presignURL with a nil " +
		"extra-header set",
}

// theOnlyPresignerAllowed is the control.
const theOnlyPresignerAllowed = "PresignHeader"

func TestNoPresignCallInThisPackageFailsOpen(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	sawTheAllowedOne := false
	filesRead := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		filesRead++

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case theOnlyPresignerAllowed:
				sawTheAllowedOne = true
			default:
				if why, isBanned := banned[sel.Sel.Name]; isBanned {
					t.Errorf("%s calls the banned presigner at %s: %s",
						name, fset.Position(call.Pos()), why)
				}
			}
			return true
		})
	}

	if filesRead == 0 {
		t.Fatal("no non-test .go file was parsed, so this walk saw nothing at all")
	}
	if !sawTheAllowedOne {
		t.Fatalf("the walk read %d file(s) and never saw a call to %s — either "+
			"nothing in this package presigns any more, or this leg is looking at "+
			"the wrong thing and would miss a banned call the same way",
			filesRead, theOnlyPresignerAllowed)
	}
}
