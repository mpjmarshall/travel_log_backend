// The two monopolies, as MECHANISMS rather than as disciplines.
//
// VS3's acceptance check is `grep -rln 'encoding/json' internal/ cmd/`, and the
// step's own record already says why a grep is not the guard: it matches its
// own source, it matches comments, and it cannot tell an import from a
// mention. That is not a theoretical objection here — the check was RED
// against correct code when this file was written, because errors.go carried a
// comment saying the encoder is confined to two functions. The comment was
// reworded so the grep passes, and rewording a comment to satisfy a check is
// exactly the evidence that the check is looking at the wrong thing.
//
// So the guards below walk the AST:
//
//  1. exactly one non-test file IMPORTS encoding/json, and it is json.go
//  2. inside json.go, exactly two FUNCTIONS use it: WriteJSON and DecodeJSON
//  3. every Code handed to WriteError anywhere in lib code is a named constant
//     from the block — not a string literal, not a conversion
//  4. the const block and the runtime status map hold the same twelve words
//
// TEST FILES ARE EXCLUDED FROM (3) AND THE EXCLUSION IS LOAD-BEARING, not
// convenience: errors_test.go calls WriteError with `httpx.Code("banana")` ON
// PURPOSE, to prove the runtime half rejects a word the AST half would have
// caught. Including test files would make the deliberate proof of one guard
// fail the other.
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

// walkLibSource calls fn for every non-test .go file in the module.
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

func TestOnlyJSONGoImportsTheJSONPackage(t *testing.T) {
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

	want := []string{"internal/httpx/json.go"}
	if len(importers) != len(want) || (len(importers) == 1 && importers[0] != want[0]) {
		t.Errorf("files importing the JSON package = %v, want %v", importers, want)
	}
}

// The two-FUNCTION half, which the file-level grep cannot express at all. A
// third function in json.go reaching for the encoder is how "confined to two
// functions" quietly becomes "confined to one file".
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

// === The closed vocabulary, as a walk ===

// constNamesInBlock reads the Code constants out of errors.go's source. The
// count is DERIVED here and never carried: four counts in the client project's
// record went wrong from somebody trusting memory over a command.
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

func TestTheConstBlockAndTheRuntimeMapHoldTheSameTwelveWords(t *testing.T) {
	block := constNamesInBlock(t)
	if len(block) != 12 {
		t.Fatalf("the const block holds %d Code constants, want 12: %v", len(block), block)
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
//
// It takes a parsed file rather than reading the tree itself, so the SAME
// checker can be run over a synthetic source that violates it. Without that,
// this guard is a function that has never been shown to reject anything —
// which today, with few call sites, it would be.
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
			case *ast.Ident: // WriteError(...) from inside the package
				name = fun.Name
			case *ast.SelectorExpr: // httpx.WriteError(...)
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

// THE ONE EXEMPTION, NAMED RATHER THAN ALLOWED FOR.
//
// WriteErrorFor is where DEC-62's mapping lands: it hands WriteError whatever
// CodeFor returned, which is a variable and cannot be a named constant. That is
// safe for exactly one reason — CodeFor answers from the block or answers
// `internal`, and never echoes what it was given.
//
// The assertion is EQUALITY with this list rather than a skip, so a SECOND call
// site passing a variable reddens this leg and has to argue for itself. It is
// keyed on file and function and not on a line number: a line number moves
// whenever an edit above it is longer than the one it replaced, which is a
// check that fails against correct work.
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

// What makes the guard above evidence rather than decoration. The checker is
// run over source that breaks it in the two ways it exists to catch, and both
// must be reported.
//
// THE SECOND CASE IS THE ONE THAT MATTERS, and it is measured rather than
// argued: `Code` is a DEFINED STRING TYPE, so an untyped string constant
// converts implicitly and `WriteError(w, r, "banana")` COMPILES. Typing the
// parameter does not close the vocabulary. This file does.
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
