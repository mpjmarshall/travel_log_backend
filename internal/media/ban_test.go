// THE BAN, INSIDE `make check` RATHER THAN INSIDE A DOCUMENT.
//
// R2's acceptance check greps this directory for the two calls that fail open.
// A grep is the right check for a stale record and it is not a guard: there is
// no CI here, `make check` is the only gate, and it does not run greps. This
// walks the package's own AST instead, so reaching for a banned call is a RED
// TEST rather than a note somebody has to remember to run.
//
// WHY THE NAMES ARE SPELLED IN HALVES, AND IT IS NOT COYNESS. R2's acceptance
// check greps this directory for both names and must return NOTHING (the
// pattern is in the step, and is deliberately not quoted here for the same
// reason the names are not). A guard that wrote its subject out in full would
// make that grep match its own explanation — which is the exact defect
// CLAUDE.md records seven times over at phase 2: an artefact check that
// matches its own replacement can only fail against correct work. So the two
// names are assembled here, once, with this paragraph beside them, and nowhere
// else in the directory does the literal appear.
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

// The two calls that sign `host` and nothing else. Both take no extra-header
// parameter at all, so a URL from either covers the method, the path and the
// expiry — and NOT the digest, the length, the type or the write-once.
// Measured against real MinIO through the first of them: a PUT of entirely
// different bytes to a key naming sha256(X) answers 200, and the object is
// stored at the poisoned address.
var banned = map[string]string{
	"Presigned" + "PutObject": "signs host only; the digest, length, type and " +
		"write-once are all outside the signature, so a lying client writes " +
		"arbitrary bytes at a content address and every later reader trusts it",
	"Presi" + "gn": "the generic form of the same hole — presignURL with a nil " +
		"extra-header set",
}

// theOnlyPresignerAllowed is the control. Without it a walker that parsed
// nothing, or looked at the wrong field, would pass this test by finding no
// banned call in a file it never read.
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
