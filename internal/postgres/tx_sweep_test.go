// The acceptance check for VS5, as a MECHANISM rather than a discipline.
//
// DEC-50 says every traveller-scoped write goes through one of the two helpers
// and names the single exception. A rule like that is worth exactly as much as
// the thing that notices when somebody opens a transaction somewhere else, so
// this walks the module's ASTs for calls named `Begin` or `BeginTx` and asserts
// the set of files making them is inside a stated allowlist.
//
// IT IS SYNTACTIC, AND THAT IS DELIBERATE. Proving the receiver is a `*sql.DB`
// needs go/types, which needs golang.org/x/tools — a dependency this project
// has not had the conversation for. Matching on the NAME is a superset: it also
// catches `conn.BeginTx`, which is what migrate.go does, and it would catch a
// hand-rolled `Begin` on anything else. A superset is the right error to make
// here: it over-reports, and an over-report is a comment away from resolved
// while an under-report is a silent hole.
//
// TEST FILES ARE EXCLUDED, for the reason internal/config's sweep excludes
// them: a leg opening its own transaction to ask a second session whether a
// lock is held is not application code, and this file's own siblings do exactly
// that.
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
// Every entry carries the reason it is on the list.
//
// The register entry is DEC-50's one exception and it is written down HERE,
// explicitly, rather than left as the surprise a later worker discovers when
// this test goes red on correct code: `POST /v1/auth/register` INSERTs the
// traveller row that the per-traveller advisory lock is keyed on, so it can
// take neither helper. It is the only write in the system outside both.
var transactionAllowlist = map[string]string{
	"internal/postgres/tx.go":      "WithTravellerTx and WithTravellerLock — the two helpers themselves",
	"internal/postgres/read_tx.go": "WithReadSnapshot — the repeatable-read snapshot the reader runs in",
	"internal/postgres/migrate.go": "the migration runner, which predates the helpers and is not traveller-scoped",

	// R4. `make seed` INSERTS the traveller row inside this transaction, so
	// there is no traveller to key an advisory lock on until it commits —
	// which is the same reason register was predicted to need an exemption.
	// Three further facts, and each is why neither helper fits rather than a
	// preference: it writes TEN tables and the helpers wrap one write; it sets
	// logbook_version to 1 in the travellers INSERT rather than bumping a
	// counter that does not exist yet; and it REFUSES to run at all when any
	// traveller row exists (DEC-97), so the concurrency the lock exists to
	// order is a state this function cannot be in. It is a developer command
	// and nothing in cmd/api imports it — cmd/api/routes_test.go asserts that
	// separately.
	"internal/seed/load.go": "the seed's ten-table load: it CREATES the traveller the " +
		"advisory lock would be keyed on, writes ten tables in one transaction, and " +
		"refuses to run when any traveller row exists (DEC-97)",
}

// DEC-50's ONE EXCEPTION IS NOT AN ALLOWLIST ENTRY, AND VS6 IS WHERE THAT WAS
// SETTLED. VS5 wrote one for `internal/rest/auth_handlers.go`, predicting that
// POST /v1/auth/register would open a transaction it could not hand to either
// helper. Two things about that prediction turned out wrong, and the second
// one is the interesting one:
//
//   - the package is `internal/httpapi`, not `internal/rest` (DEC-74), so the
//     entry named a path nothing would ever occupy; and
//   - register's write is ONE INSERT, which is already atomic, so it opens no
//     transaction at all. An allowlist entry grants an exemption; an exemption
//     nothing uses is a hole with a comment over it, and TestNoAllowlistEntryIsStale
//     exists precisely to say so.
//
// So the entry is gone and TestRegisterTakesNeitherHelperAndOpensNoTransaction
// below replaces it. That is a strictly stronger check: the old one could only
// fail if somebody edited the map, and this one fails if the CODE changes.

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

// A stale allowlist entry is the way this check rots: the file is renamed or
// stops opening a transaction, the entry stays, and the exemption it grants is
// now unattached to anything. Entries for files that do not exist YET are
// tolerated — the register entry is one, by design — but a file that exists and
// is exempt must be using its exemption.
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

// The membership split of DEC-50, as a walk over the file that implements it.
//
// IT IS ONE METHOD SINCE DEC-100, NOT TWO. TouchSession left
// WithTravellerLock: it writes one row keyed by session id, and what the
// advisory lock protects is MULTI-STATEMENT work. See
// TestTouchSessionTakesNeitherHelper below, which is the other half and asserts
// the opposite about the same file.
//
// IT IS SYNTACTIC, like the sweep above and for the same reason: proving a
// receiver's type needs go/types and therefore golang.org/x/tools, which this
// project has not had the dependency conversation for. Matching on the NAME
// over-reports, and an over-report is a comment away from resolved while an
// under-report is a silent hole.
//
// WHAT IT ADDS OVER THE BEHAVIOURAL LEG. auth_store_test.go asserts that a
// session write moves logbook_version by zero, which is the evidence. This
// asserts the RULE — that the session writes go through WithTravellerLock and
// never WithTravellerTx — so a future session write that happens not to be
// counted by an existing leg still cannot take the bumping helper.
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

// DEC-100, AS A RULE ABOUT THE CODE RATHER THAN A MEASUREMENT OF ONE 304.
//
// The behavioural leg is in auth_store_test.go — it holds the traveller's
// advisory lock in a second session and watches TouchSession complete anyway —
// and it can only see what happens with the lock held. This asserts the rule,
// so a future edit that "restores consistency" by wrapping the touch back up in
// WithTravellerLock is red at the file rather than at a timing.
//
// AND IT NAMES WithTravellerTx SEPARATELY, because the two mistakes are
// different sizes. Going back to WithTravellerLock costs four round trips per
// authenticated request and serialises against the phone's own writes. Reaching
// for WithTravellerTx instead bumps logbook_version on every request, which
// invalidates the phone's whole cached log and means GET /v1/logbook never once
// answers 304 in real use.
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

// DEC-50's one exception, asserted about the code rather than about a map.
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
