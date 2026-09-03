// The panel's own vocabulary: what crosses its Store seam is declared here,
// so a second adapter is possible and a template renders no database row.
package admin

import (
	"go/parser"
	gotoken "go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// persistencePackages are the imports that would put a storage adapter's own
// types in this package's vocabulary.
var persistencePackages = map[string]string{
	"travellog/internal/postgres": "the panel's Store and Writer are ports; a row type " +
		"from the adapter behind them cannot be part of what they promise",
	"database/sql": "a port that names sql.NullString has chosen an adapter",
}

func TestThePanelImportsNoPersistenceAdapter(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/admin: %v", err)
	}

	fset := gotoken.NewFileSet()
	offenders := map[string][]string{}
	read := 0

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		read++
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			if _, banned := persistencePackages[path]; banned {
				offenders[path] = append(offenders[path], name)
			}
		}
	}

	if read == 0 {
		t.Fatal("no non-test file was parsed, so this sweep would pass having read nothing")
	}

	paths := make([]string, 0, len(offenders))
	for path := range offenders {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		sort.Strings(offenders[path])
		t.Errorf("internal/admin imports %s in %s — %s",
			path, strings.Join(offenders[path], ", "), persistencePackages[path])
	}
}
