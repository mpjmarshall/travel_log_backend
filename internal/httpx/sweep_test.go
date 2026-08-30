// The two monopolies, as MECHANISMS rather than as disciplines.
package httpx_test

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"travellog/internal/httpx"
)

func moduleRootFromHttpx(t *testing.T) string {
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

// walkLibSource calls fn for every non-test.go file in the module.
func walkLibSource(t *testing.T, fn func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	root := moduleRootFromHttpx(t)
	fset := token.NewFileSet()

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
		fn(filepath.ToSlash(rel), fset, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// jsonImporters is the named list, and it is EQUALITY rather than a count.
var jsonImporters = map[string]string{
	"internal/httpx/json.go": "spec L19's payload encoder and decoder: WriteJSON and DecodeJSON, " +
		"and the two-function half below is what keeps it to those two",
	"internal/logbook/rewrite.go": "DecodeEnvelope: `make seed` reads the captured client fixture " +
		"off disk (DEC-75), which is a file rather than a payload and has no ResponseWriter to " +
		"hand MaxBytesReader",
}

func TestOnlyNamedFilesImportTheJSONPackage(t *testing.T) {
	var importers []string
	walkLibSource(t, func(rel string, _ *token.FileSet, file *ast.File) {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			if path == "encoding/json" {
				importers = append(importers, rel)
			}
		}
	})
	sort.Strings(importers)

	seen := map[string]bool{}
	for _, rel := range importers {
		if _, ok := jsonImporters[rel]; !ok {
			t.Errorf("%s imports encoding/json and is not on the list — add it "+
				"with the reason, or route the call through internal/httpx", rel)
			continue
		}
		if seen[rel] {
			t.Errorf("encoding/json is imported twice in %s", rel)
		}
		seen[rel] = true
	}
	for rel, why := range jsonImporters {
		if !seen[rel] {
			t.Errorf("%s is listed as a JSON importer (%s) and does not import it", rel, why)
		}
	}
}

// The two-FUNCTION half, which the file-level grep cannot express at all.
func TestOnlyTwoFunctionsInJSONGoUseTheEncoder(t *testing.T) {
	root := moduleRootFromHttpx(t)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, "internal/httpx/json.go"), nil,
		parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing json.go: %v", err)
	}

	users := map[string]bool{}
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			sel, isSel := n.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == "json" {
				users[fn.Name.Name] = true
			}
			return true
		})
	}

	got := make([]string, 0, len(users))
	for name := range users {
		got = append(got, name)
	}
	sort.Strings(got)

	want := []string{"DecodeJSON", "WriteJSON"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("functions using the encoder = %v, want %v", got, want)
	}
}

// constNamesInBlock reads the Code constants out of errors.go's source.
func constNamesInBlock(t *testing.T) map[string]string {
	t.Helper()
	root := moduleRootFromHttpx(t)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, "internal/httpx/errors.go"), nil,
		parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing errors.go: %v", err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			typeName, isIdent := value.Type.(*ast.Ident)
			if !isIdent || typeName.Name != "Code" {
				continue
			}
			lit, isLit := value.Values[0].(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				continue
			}
			word, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: %v", value.Names[0].Name, err)
			}
			out[value.Names[0].Name] = word
		}
	}
	return out
}

func TestTheConstBlockAndTheRuntimeMapHoldTheSameWords(t *testing.T) {
	block := constNamesInBlock(t)
	if len(block) != len(decCodes) {
		t.Fatalf("the const block holds %d Code constants, want %d: %v",
			len(block), len(decCodes), block)
	}

	fromSource := make([]string, 0, len(block))
	for _, word := range block {
		fromSource = append(fromSource, word)
	}
	sort.Strings(fromSource)

	atRuntime := make([]string, 0, len(httpx.Codes()))
	for _, c := range httpx.Codes() {
		atRuntime = append(atRuntime, string(c))
	}

	if strings.Join(fromSource, ",") != strings.Join(atRuntime, ",") {
		t.Errorf("the const block says %v; StatusFor's map says %v", fromSource, atRuntime)
	}
}

type wireCodeUse struct {
	file string
	fn   string
	expr string
}

// badWireCodes reports every WriteError argument that is not a named constant
// from the block.
func badWireCodes(rel string, fset *token.FileSet, file *ast.File, known map[string]bool) []wireCodeUse {
	var bad []wireCodeUse

	for _, decl := range file.Decls {
		enclosing, isFunc := decl.(*ast.FuncDecl)
		if !isFunc {
			continue
		}
		ast.Inspect(enclosing, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}

			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			if name != "WriteError" || len(call.Args) != 3 {
				return true
			}

			switch arg := call.Args[2].(type) {
			case *ast.Ident:
				if known[arg.Name] {
					return true
				}
			case *ast.SelectorExpr:
				if known[arg.Sel.Name] {
					return true
				}
			}

			var buf strings.Builder
			_ = printer.Fprint(&buf, fset, call.Args[2])
			bad = append(bad, wireCodeUse{
				file: rel,
				fn:   enclosing.Name.Name,
				expr: buf.String(),
			})
			return true
		})
	}
	return bad
}

// the one exemption, named rather than allowed for.
var wireCodeExemptions = []wireCodeUse{
	{file: "internal/httpx/errors.go", fn: "WriteErrorFor", expr: "c"},
}

func TestEveryWordOnTheWireIsANamedConstantFromTheBlock(t *testing.T) {
	known := map[string]bool{}
	for name := range constNamesInBlock(t) {
		known[name] = true
	}

	var bad []wireCodeUse
	walkLibSource(t, func(rel string, fset *token.FileSet, file *ast.File) {
		bad = append(bad, badWireCodes(rel, fset, file, known)...)
	})
	sort.Slice(bad, func(i, j int) bool { return bad[i].file+bad[i].fn < bad[j].file+bad[j].fn })

	if len(bad) != len(wireCodeExemptions) {
		t.Fatalf("WriteError is passed a non-constant at %d sites, want exactly "+
			"the %d exempted:\n got  %+v\n want %+v",
			len(bad), len(wireCodeExemptions), bad, wireCodeExemptions)
	}
	for i := range bad {
		if bad[i] != wireCodeExemptions[i] {
			t.Errorf("non-constant WriteError argument at %+v, want %+v",
				bad[i], wireCodeExemptions[i])
		}
	}
}

// What makes the guard above evidence rather than decoration.
func TestTheCheckerRejectsALiteralAndAConversion(t *testing.T) {
	const src = `package fake

func f(w, r any) {
	httpx.WriteError(w, r, httpx.CodeNotFound)
	httpx.WriteError(w, r, "banana")
	httpx.WriteError(w, r, httpx.Code("teapot"))
	WriteError(w, r, CodeInternal)
	WriteError(w, r, Code("teapot"))
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fake.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}

	known := map[string]bool{"CodeNotFound": true, "CodeInternal": true}
	bad := badWireCodes("fake.go", fset, file, known)

	if len(bad) != 3 {
		t.Fatalf("the checker reported %d violations, want 3: %+v", len(bad), bad)
	}
	joined := ""
	for _, b := range bad {
		joined += b.expr + "|"
	}
	for _, want := range []string{`"banana"`, `httpx.Code("teapot")`, `Code("teapot")`} {
		if !strings.Contains(joined, want) {
			t.Errorf("the checker missed %s; it reported %s", want, joined)
		}
	}
}
