// The acceptance check for, as a mechanism rather than a discipline.
package postgres

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

// transactionAllowlist is the spec, not a record of what happens to be true.
var transactionAllowlist = map[string]string{
	"internal/postgres/tx.go":      "WithTravellerTx and WithTravellerLock — the two helpers themselves",
	"internal/postgres/read_tx.go": "WithReadSnapshot — the repeatable-read snapshot the reader runs in",
	"internal/postgres/migrate.go": "the migration runner, which predates the helpers and is not traveller-scoped",

	"internal/seed/load.go": "the seed's ten-table load: it CREATES the traveller the " +
		"advisory lock would be keyed on, writes ten tables in one transaction, and " +
		"refuses to run when any traveller row exists (DEC-97)",
}

// transactionOpeners answers the module's non-test files that call Begin or
// BeginTx, and the files it walked.
func transactionOpeners(t *testing.T) map[string]bool {
	t.Helper()
	root := moduleRootFrom(t)
	fset := token.NewFileSet()
	found := map[string]bool{}

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
		rel = filepath.ToSlash(rel)
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Begin" || sel.Sel.Name == "BeginTx" {
				found[rel] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	return found
}

func moduleRootFrom(t *testing.T) string {
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

func TestNothingOutsideTheAllowlistOpensATransaction(t *testing.T) {
	var stray []string
	for file := range transactionOpeners(t) {
		if _, ok := transactionAllowlist[file]; !ok {
			stray = append(stray, file)
		}
	}
	sort.Strings(stray)
	if len(stray) > 0 {
		t.Errorf("these files open a transaction and are not on the allowlist: %v\n"+
			"    DEC-50: a traveller-scoped write goes through WithTravellerTx (which bumps\n"+
			"    logbook_version) or WithTravellerLock (which does not). Opening one directly\n"+
			"    drops the advisory lock that DEC-02's cross-kind check and DEC-38's upsert\n"+
			"    both declare themselves race-free under. If this is genuinely the exception,\n"+
			"    put it in transactionAllowlist with its reason.", stray)
	}
}

// A stale allowlist entry is the way this check rots.
func TestNoAllowlistEntryIsStale(t *testing.T) {
	root := moduleRootFrom(t)
	opens := transactionOpeners(t)

	for file, reason := range transactionAllowlist {
		if reason == "" {
			t.Errorf("%s is allowlisted with no reason", file)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
			continue
		}
		if !opens[file] {
			t.Errorf("%s is allowlisted to open a transaction and does not — "+
				"remove the entry, or the next file to inherit that path inherits the exemption", file)
		}
	}
}

// The membership split of, as a walk over the file that implements it.
func TestTheSessionWritesTakeTheLockingHelperAndNotTheBumpingOne(t *testing.T) {
	calls := callsByFunction(t, "internal/postgres/auth_store.go")

	for _, method := range []string{"CreateSession"} {
		made, defined := calls[method]
		if !defined {
			t.Errorf("AuthStore.%s is not in auth_store.go", method)
			continue
		}
		if !made["WithTravellerLock"] {
			t.Errorf("AuthStore.%s does not call WithTravellerLock.\n"+
				"    DEC-50: a session write is traveller-scoped and must hold the advisory\n"+
				"    lock, so it cannot go straight to the pool.", method)
		}
		if made["WithTravellerTx"] {
			t.Errorf("AuthStore.%s calls WithTravellerTx, which BUMPS logbook_version.\n"+
				"    `last_used_at` is written on EVERY authenticated request, so counting it\n"+
				"    invalidates the phone's whole cached log — 95,586 bytes at fixture\n"+
				"    scale, measured through this build — every time it asks, and\n"+
				"    GET /v1/logbook never once answers 304 in real use.", method)
		}
	}
}

// , AS A rule about the code rather than A measurement of one 304.
func TestTouchSessionTakesNeitherHelper(t *testing.T) {
	made := callsByFunction(t, "internal/postgres/auth_store.go")["TouchSession"]
	if made == nil {
		t.Fatalf("AuthStore.TouchSession is not in auth_store.go")
	}
	if made["WithTravellerLock"] {
		t.Errorf("AuthStore.TouchSession calls WithTravellerLock.\n" +
			"    DEC-100, measured with pg_stat_statements around exactly one 304:\n" +
			"    stamping last_used_at was FIVE of NINE round trips — begin, the\n" +
			"    advisory lock, SELECT 1 FROM travellers, the UPDATE, commit — against\n" +
			"    0.176ms of total server exec time. It also serialised every\n" +
			"    authenticated request against the phone's own in-flight writes.")
	}
	if made["WithTravellerTx"] {
		t.Errorf("AuthStore.TouchSession calls WithTravellerTx, which BUMPS logbook_version.\n" +
			"    `last_used_at` is written on some authenticated requests, so counting it\n" +
			"    invalidates the phone's whole cached log and GET /v1/logbook never once\n" +
			"    answers 304 in real use.")
	}
}

// The one exception, asserted about the code rather than about a map.
func TestRegisterTakesNeitherHelperAndOpensNoTransaction(t *testing.T) {
	made := callsByFunction(t, "internal/postgres/auth_store.go")["CreateTraveller"]
	if made == nil {
		t.Fatalf("AuthStore.CreateTraveller is not in auth_store.go")
	}
	for _, banned := range []string{"WithTravellerTx", "WithTravellerLock", "Begin", "BeginTx"} {
		if made[banned] {
			t.Errorf("AuthStore.CreateTraveller calls %s.\n"+
				"    It INSERTs the traveller row the per-traveller advisory lock is KEYED ON,\n"+
				"    so there is nothing to lock yet — it is the only write in the system\n"+
				"    outside both helpers. One INSERT is already atomic and needs no\n"+
				"    transaction of its own; if that stops being true, this needs a design\n"+
				"    decision and not an allowlist entry.", banned)
		}
	}
}

// callsByFunction answers, per top-level function or method in one file, the
// set of selector call names its body makes.
func callsByFunction(t *testing.T, rel string) map[string]map[string]bool {
	t.Helper()
	path := filepath.Join(moduleRootFrom(t), filepath.FromSlash(rel))
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}

	out := map[string]map[string]bool{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		made := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				made[fun.Name] = true
			case *ast.SelectorExpr:
				made[fun.Sel.Name] = true
			}
			return true
		})
		out[fn.Name.Name] = made
	}
	return out
}
