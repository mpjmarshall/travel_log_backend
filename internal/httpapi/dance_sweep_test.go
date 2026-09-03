// The parts of the request dance that must exist once: a tag written by hand
// in nine files is a rule nine files have to remember.
package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// callSitesOf answers every non-test file in this package that calls the named
// function of the named package, with the function it was called from.
func callSitesOf(t *testing.T, pkg, fn string) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/httpapi: %v", err)
	}
	fset := token.NewFileSet()
	var sites []string

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			outer, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(outer, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != fn {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == pkg {
					sites = append(sites, name+":"+outer.Name.Name)
				}
				return true
			})
		}
	}
	sort.Strings(sites)
	return sites
}

func TestTheEntityTagIsComputedInOnePlace(t *testing.T) {
	sites := callSitesOf(t, "httpx", "FormatETag")

	if len(sites) == 0 {
		t.Fatal("nothing in this package calls httpx.FormatETag, so this sweep cannot " +
			"fail for a reason about the tag")
	}
	if len(sites) > 1 {
		t.Errorf("httpx.FormatETag is called from %d places — %s — so the rule that "+
			"pairs the emitter version with the logbook version is written out once per "+
			"route and a change to it is that many edits",
			len(sites), strings.Join(sites, ", "))
	}
}

// credentialExemptions names the call sites that read the credential for
// something other than a handler's own preamble.
var credentialExemptions = map[string]string{
	"handler.go:travellerOf": "the preamble itself",
	"auth_handlers.go:limitByTraveller": "middleware, not a handler: it answers the " +
		"limiter a key and must not write a status of its own",
}

func TestTheCredentialPreambleIsWrittenOnce(t *testing.T) {
	all := callSitesOf(t, "auth", "TravellerFrom")
	var sites []string
	for _, site := range all {
		if _, exempt := credentialExemptions[site]; !exempt {
			sites = append(sites, site)
		}
	}
	for site := range credentialExemptions {
		if !slices.Contains(all, site) {
			t.Errorf("credentialExemptions names %s and nothing there reads the credential", site)
		}
	}

	if len(all) == 0 {
		t.Fatal("nothing in this package calls auth.TravellerFrom, so this sweep is " +
			"asserting nothing")
	}
	if len(sites) > 0 {
		t.Errorf("auth.TravellerFrom is read outside the preamble in %d place(s) — %s — "+
			"and each one repeats the same refusal for a credential the middleware "+
			"has already resolved", len(sites), strings.Join(sites, ", "))
	}
}

// reconcileBlock is the rule that a client-minted id in the path wins and a
// body naming a different one is refused.
const reconcileBlock = `if *bodyID == nil {`

func TestThePathIdIsReconciledInOnePlace(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/httpapi: %v", err)
	}

	var sites []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if n := strings.Count(string(src), reconcileBlock); n > 0 {
			sites = append(sites, name)
		}
	}
	sort.Strings(sites)

	if len(sites) == 0 {
		t.Fatal("the reconcile block appears nowhere, so this sweep cannot fail for a " +
			"reason about the rule it names")
	}
	if len(sites) > 1 {
		t.Errorf("the path id is reconciled with the body id in %d files — %s — so the "+
			"rule that decides where a write lands is written out once per route",
			len(sites), strings.Join(sites, ", "))
	}
}
