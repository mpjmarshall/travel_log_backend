// THE EMIT MONOPOLY, AS A MECHANISM RATHER THAN AS A DISCIPLINE (CF-BLO-3,
// PD-15, definition_of_done 9).
//
// The rule: no handler in this package writes a domain entity to
// `httpx.WriteJSON` without passing it through `internal/logbook` first. A nil
// slice marshals to `null` and every list key in this document is read by the
// client as a NON-NULLABLE List, so a bare entity carrying an empty list is
// the one shape the app throws on — measured twice now, against `"cityIds":
// null` on a running server before EmitTrip existed, and against
// `"visits":null` here.
//
// IT IS WRITTEN IN R6 AND NOT IN R8, because R6 IS THE FIRST STEP THAT CAN
// VIOLATE IT. `PUT /v1/places/{id}` is the first route to answer a bare entity
// that carries a list, and a sweep written after two more steps of unguarded
// handlers would be a cleanup rather than a guard.
//
// A GREP CANNOT MAKE THIS CHECK, which is the same finding VS3 recorded about
// the encoding/json monopoly: it matches its own source, it matches comments,
// and it cannot tell `logbook.EmitPlace(place)` from the word "EmitPlace" in a
// sentence. So this walks the AST of every non-test file in this package,
// finds every call to `httpx.WriteJSON`, and classifies its FOURTH argument.
//
// THREE CLASSIFICATIONS AND ONLY ONE OF THEM NEEDS A LIST ENTRY:
//
//   - a call to `logbook.Emit*` — the rule being kept, and always fine;
//   - a composite literal of a LOCAL type (`mediaBody{…}`) — httpapi's own
//     response shape rather than a domain entity, and it cannot be one,
//     because a domain entity's literal would be `logbook.Place{…}` and is
//     classified as a selector;
//   - anything else — a bare identifier, a field access, a conversion — which
//     must be on `bareBodies` with the reason it needs no normalisation.
//
// THE LIST IS EQUALITY AND NOT A CEILING, on the jsonImporters precedent: an
// entry that stops existing reddens this too, so a route that starts
// normalising cannot leave a stale exemption behind claiming it does not.
package httpapi

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// bareBodies is every fourth argument to httpx.WriteJSON in this package that
// is NOT a composite literal of a local type and NOT an Emit call, with the
// reason it may be written bare.
//
// EVERY ENTRY HERE HAS TO ARGUE THAT ITS VALUE CARRIES NO LIST FIELD, because
// that is the only thing EmitX does. Two of the three are `logbook.Emit`'s own
// return value, which is the whole document already normalised; the third is a
// domain entity and it is the one that had to be argued.
var bareBodies = map[string]string{
	"internal/httpapi/logbook_handlers.go:envelope": "logbook.Emit's own return value: " +
		"every list in the document was normalised by the function that built it",
	"internal/httpapi/trip_handlers.go:envelope": "logbook.Emit's own return value, " +
		"on D3's whole-log answer",
	"internal/httpapi/place_handlers.go:envelope": "logbook.Emit's own return value, " +
		"on D2's whole-log answer",
	"internal/httpapi/city_handlers.go:envelope": "logbook.Emit's own return value, " +
		"on the cascading half of T5's city write",
	"internal/httpapi/me_handlers.go:named": "a logbook.Traveller, which carries ONE key " +
		"and no list field — measured, it marshals to exactly {\"name\":\"Matt\"} — so " +
		"there is no nil slice for the marshaller to write as null",
	"internal/httpapi/city_handlers.go:written.City": "a logbook.City, which carries no " +
		"list field at all — measured, a bare one marshals with no list key in it — so " +
		"an EmitCity would be the empty forwarding method DEC-62 warns against",
	"internal/httpapi/photo_handlers.go:photo": "a logbook.Photo, which carries no " +
		"list field at all — measured, a bare one marshals with no list key in it — so " +
		"an EmitPhoto would be the empty forwarding method DEC-62 warns against. " +
		"TestACityATravellerAndAPhotoCarryNoListAndThereforeNeedNoEmitter asserts it " +
		"on the STRUCT and TestAPhotoAnswerCarriesNoListKeyAndThereforeNeedsNoEmitter " +
		"asserts it on the bytes this route writes",
	"internal/httpapi/photo_handlers.go:refiled.Photo": "the same logbook.Photo, on the " +
		"half of M2.2's re-file that moved one entity and is therefore a splice",
	"internal/httpapi/photo_handlers.go:envelope": "logbook.Emit's own return value, on " +
		"the half of M2.2's re-file that MINTED an occasion — two entities moved, so " +
		"the phone cannot splice what it was not sent",
	"internal/httpapi/public_handlers.go:envelope": "logbook.EmitPublic's own return " +
		"value, on the public read. It normalises for the same reason logbook.Emit " +
		"does and in two places the private document never reaches: `photos` is [] " +
		"and never null when sharePhotos is off, and `walks[].points` is [] and " +
		"never null when shareCoordinates is off",
	"internal/httpapi/media_handlers.go:answer": "httpapi's own beginBody, built in this " +
		"file; it is a local type and is only here because it is assembled across " +
		"branches rather than in one literal",
}

func TestEveryDomainEntityOnTheWireGoesThroughInternalLogbook(t *testing.T) {
	root := moduleRootFromHttpx(t)
	fset := token.NewFileSet()

	seen := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, "internal/httpapi"))
	if err != nil {
		t.Fatalf("reading internal/httpapi: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		rel := "internal/httpapi/" + name
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "WriteJSON" {
				return true
			}
			pkg, isIdent := sel.X.(*ast.Ident)
			if !isIdent || pkg.Name != "httpx" || len(call.Args) != 4 {
				return true
			}

			body := call.Args[3]
			if emitsThroughLogbook(body) {
				return true
			}
			if literal, isLiteral := body.(*ast.CompositeLit); isLiteral {
				if _, isForeign := literal.Type.(*ast.SelectorExpr); !isForeign {
					// A local type: httpapi's own response shape. A domain
					// entity written as a literal would be
					// `logbook.Place{…}` — a SelectorExpr — and falls through
					// to the list below.
					return true
				}
			}

			key := rel + ":" + render(t, fset, body)
			seen[key] = true
			if why, listed := bareBodies[key]; !listed {
				t.Errorf("%s writes %s to the wire without passing it through "+
					"internal/logbook.\n"+
					"    A nil slice marshals to `null`, and every list key in this\n"+
					"    document is read by the client as a non-nullable List — so a\n"+
					"    bare entity with an empty list is the one shape the app throws\n"+
					"    on. Route it through an EmitX, or add it to bareBodies with the\n"+
					"    argument that its type carries no list field.",
					rel, render(t, fset, body))
			} else if why == "" {
				t.Errorf("%s is listed with no reason", key)
			}
			return true
		})
	}

	var stale []string
	for key := range bareBodies {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is exempted from the emit rule (%s) and no longer writes anything to "+
			"the wire. An exemption nothing uses is a claim nobody can check", key, bareBodies[key])
	}
}

// emitsThroughLogbook answers whether the expression is a call into
// internal/logbook's emitter family.
//
// IT MATCHES THE PACKAGE AND THE PREFIX RATHER THAN A LIST OF NAMES, because
// the family grows one function per step — EmitTrip at R1, EmitPlace here,
// EmitWalk in R7 — and a list here would be a second place to remember them.
// `logbook.Emit` itself is included: it is the whole-document normaliser and is
// the strongest form of the rule being kept.
func emitsThroughLogbook(expr ast.Expr) bool {
	call, isCall := expr.(*ast.CallExpr)
	if !isCall {
		return false
	}
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return false
	}
	pkg, isIdent := sel.X.(*ast.Ident)
	return isIdent && pkg.Name == "logbook" && strings.HasPrefix(sel.Sel.Name, "Emit")
}

// THE CHECKER ITSELF IS CHECKED, on the precedent httpx's error sweep set:
// "a guard proven once against one mutation is proven against that mutation,
// not against its class". These are the four shapes the classifier has to tell
// apart, asserted directly rather than through a file on disk.
func TestTheEmitCheckerTellsTheFourShapesApart(t *testing.T) {
	for _, tc := range []struct {
		source string
		emits  bool
	}{
		{"logbook.EmitPlace(place)", true},
		{"logbook.EmitTrip(trip)", true},
		{"logbook.Emit(format, doc)", true},
		{"place", false},
		{"logbook.Place{ID: id}", false},
		{"emitPlace(place)", false},
		{"other.EmitPlace(place)", false},
	} {
		expr, err := parser.ParseExpr(tc.source)
		if err != nil {
			t.Fatalf("parsing %q: %v", tc.source, err)
		}
		if got := emitsThroughLogbook(expr); got != tc.emits {
			t.Errorf("emitsThroughLogbook(%s) = %v, want %v", tc.source, got, tc.emits)
		}
	}
}

func render(t *testing.T, fset *token.FileSet, expr ast.Expr) string {
	t.Helper()
	var out strings.Builder
	if err := printer.Fprint(&out, fset, expr); err != nil {
		t.Fatalf("rendering an expression: %v", err)
	}
	return out.String()
}

// moduleRootFromHttpx is httpx's own helper, re-spelled here because this
// package cannot import that package's tests.
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
