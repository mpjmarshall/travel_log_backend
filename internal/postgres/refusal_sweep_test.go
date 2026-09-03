// The field vocabulary's home, as a mechanism rather than as a discipline: a
// refusal authored here is one internal/logbook cannot state or test.
package postgres

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// refusalsAuthoredHere is every function in this package that may construct a
// logbook.InvalidFieldError, with the reason it is not internal/logbook's.
var refusalsAuthoredHere = map[string]string{
	"city_store.go:requireTrip":            "existence: a fact about storage, not a rule",
	"logbook_store.go:requireCities":       "existence",
	"logbook_store.go:requireCover":        "existence, and that the object is committed",
	"photo_store.go:requireTripForPhoto":   "existence",
	"photo_store.go:requireCityForPhoto":   "existence",
	"photo_store.go:requirePhotoAsset":     "existence, and that the object is committed",
	"photo_store.go:readPlaceCity":         "existence: the place is not a pin in this log",
	"place_store.go:requireCityForPlace":   "existence",
	"place_store.go:requireTripsForVisits": "existence, for a whole array in one statement",
	"walk_store.go:requireTripForWalk":     "existence",
	"walk_store.go:requireCityForWalk":     "existence",

	"place_store.go:refuseVisitsHeldElsewhere": "inherently transactional: the comparison IS " +
		"the SQL predicate and Go does none of it",
	"place_store.go:refuseDroppingAnOccupiedOccasion": "inherently transactional: the set " +
		"difference and the filing count are both computed in the statement, over state " +
		"the same transaction is about to mutate",
}

func TestOnlyTheNamedFunctionsAuthorARefusalHere(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/postgres: %v", err)
	}
	fset := gotoken.NewFileSet()
	found := map[string]bool{}
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
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "InvalidFieldError" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "logbook" {
					found[name+":"+fn.Name.Name] = true
				}
				return true
			})
		}
	}
	if read == 0 {
		t.Fatal("no non-test file was parsed, so this sweep would pass having read nothing")
	}
	if len(found) == 0 {
		t.Fatal("no refusal was found anywhere, so this sweep cannot fail for a reason " +
			"about where the vocabulary lives")
	}

	for _, site := range sortedSites(found) {
		if _, allowed := refusalsAuthoredHere[site]; !allowed {
			t.Errorf("%s authors a logbook.InvalidFieldError — the rule belongs in "+
				"internal/logbook, where it can be decided and tested without a database. "+
				"If it genuinely cannot, add it here with the reason", site)
		}
	}
	for site := range refusalsAuthoredHere {
		if !found[site] {
			t.Errorf("refusalsAuthoredHere names %s and nothing there authors a refusal — "+
				"a stale exemption is a hole with a comment over it", site)
		}
	}
}

func sortedSites(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
