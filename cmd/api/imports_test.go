// The pgx blank-import monopoly, as a mechanism.
package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func moduleRootFromHere(t *testing.T) string {
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

type pgxImport struct {
	file  string
	path  string
	blank bool
}

func pgxImports(t *testing.T) []pgxImport {
	t.Helper()
	root := moduleRootFromHere(t)
	fset := token.NewFileSet()
	var found []pgxImport

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
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !strings.Contains(importPath, "jackc/pgx") {
				continue
			}
			found = append(found, pgxImport{
				file:  filepath.ToSlash(rel),
				path:  importPath,
				blank: spec.Name != nil && spec.Name.Name == "_",
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].file < found[j].file })
	return found
}

// corrected at, and the correction is the interesting half.
var pgxImporters = map[string]string{
	"cmd/api/main.go":                    "the binary: registers the driver for database/sql, and calls nothing in it",
	"internal/postgres/testdb/testdb.go": "the test seam: opens the pool the store legs run against",
	"cmd/seed/main.go":                   "the developer command: registers the driver for its own pool, and calls nothing in it",
}

// TestPgxIsImportedOnlyBlankAndOnlyWhereItIsTheDriver makes the claim A grep
// cannot make.
func TestPgxIsImportedOnlyBlankAndOnlyWhereItIsTheDriver(t *testing.T) {
	got := pgxImports(t)

	seen := map[string]bool{}
	for _, imp := range got {
		if _, ok := pgxImporters[imp.file]; !ok {
			t.Errorf("pgx is imported in %s, which is not on the list — "+
				"add it with the reason, or stop importing it there", imp.file)
			continue
		}
		if seen[imp.file] {
			t.Errorf("pgx is imported twice in %s", imp.file)
		}
		seen[imp.file] = true

		if imp.path != "github.com/jackc/pgx/v5/stdlib" {
			t.Errorf("%s imports %q, want github.com/jackc/pgx/v5/stdlib", imp.file, imp.path)
		}
		if !imp.blank {
			t.Errorf("pgx is a NAMED import in %s; spec L20 says solely as a blank import driver", imp.file)
		}
	}

	for file, why := range pgxImporters {
		if !seen[file] {
			t.Errorf("%s is listed as a pgx importer (%s) and does not import it", file, why)
		}
	}
}

// cmd/api must not import internal/seed, and this is the mechanism rather
// than the sentence.
func TestNothingUnderCmdAPIReachesInternalSeed(t *testing.T) {
	root := moduleRootFromHere(t)
	pkgs := map[string][]string{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, perr := parser.ParseFile(token.NewFileSet(), path, nil,
			parser.ImportsOnly|parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			return rerr
		}
		pkg := "travellog/" + filepath.ToSlash(rel)
		for _, spec := range parsed.Imports {
			importPath, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil {
				return uerr
			}
			if strings.HasPrefix(importPath, "travellog/") {
				pkgs[pkg] = append(pkgs[pkg], importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if !reaches(pkgs, "travellog/cmd/seed", "travellog/internal/seed", map[string]bool{}) {
		t.Errorf("cmd/seed does not reach internal/seed — the walk found nothing, " +
			"so the assertion below is proving nothing either")
	}

	if reaches(pkgs, "travellog/cmd/api", "travellog/internal/seed", map[string]bool{}) {
		t.Errorf("cmd/api reaches internal/seed. It is a developer command that loads " +
			"a captured logbook; nothing that serves a real one may link it.")
	}
}

func reaches(graph map[string][]string, from, to string, seen map[string]bool) bool {
	if from == to {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, next := range graph[from] {
		if reaches(graph, next, to, seen) {
			return true
		}
	}
	return false
}
