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
	"internal/rest/auth_handlers.go": "DEC-50's ONE exception: POST /v1/auth/register inserts the traveller " +
		"row the lock is keyed on, so it can take neither helper (arrives at VS6)",
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

// An ARTEFACT CHECK, and labelled as one: it can only fail if somebody edits
// the map above, never because the code is wrong. It is here because DEC-50's
// own text says the register exemption must be allowlisted EXPLICITLY rather
// than left as the one exception a worker discovers, and the cheapest way for
// that instruction to be quietly undone is for the entry to be deleted while
// the file that needs it does not exist yet.
func TestTheRegisterExemptionIsWrittenDown(t *testing.T) {
	for file, reason := range transactionAllowlist {
		if strings.Contains(file, "auth_handlers.go") {
			if !strings.Contains(reason, "register") {
				t.Errorf("%s is allowlisted but its reason does not name register: %q", file, reason)
			}
			return
		}
	}
	t.Errorf("DEC-50's one exception — POST /v1/auth/register — is not in transactionAllowlist.\n" +
		"    It inserts the traveller row the per-traveller lock is keyed on, so it can take\n" +
		"    neither helper. Deleting the entry does not make it stop being the exception.")
}
