// The pgx blank-import monopoly, as a mechanism.
//
// go_backend.md L20: "Use the standard `database/sql` package exclusively. Do
// not use GORM, sqlc, or sqlx. Use `_ \"github.com/jackc/pgx/v5/stdlib\"`
// solely as a blank import driver."
//
// Three claims live in that sentence and this file asserts all three: the
// driver is imported EXACTLY ONCE, it is imported in cmd/api/main.go, and it is
// imported BLANK. The third is the one a grep cannot make — `grep -rn
// 'jackc/pgx'` returning one line is equally satisfied by `import pgx
// "github.com/jackc/pgx/v5/stdlib"` followed by a direct call into it, which is
// exactly what "solely as a blank import driver" forbids.
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

func TestPgxIsImportedExactlyOnceBlankAndInMain(t *testing.T) {
	got := pgxImports(t)

	if len(got) != 1 {
		t.Fatalf("pgx imports = %+v (%d), want exactly 1", got, len(got))
	}
	if got[0].file != "cmd/api/main.go" {
		t.Errorf("pgx imported in %q, want cmd/api/main.go", got[0].file)
	}
	if got[0].path != "github.com/jackc/pgx/v5/stdlib" {
		t.Errorf("pgx import path = %q, want github.com/jackc/pgx/v5/stdlib", got[0].path)
	}
	if !got[0].blank {
		t.Errorf("pgx imported as a NAMED import in %s; spec L20 says solely as a blank import driver", got[0].file)
	}
}
