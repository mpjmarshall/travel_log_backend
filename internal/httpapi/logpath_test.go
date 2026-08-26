// The capability in the log, at every site that prints a path (PD-08).
//
// EXECUTED AGAINST THE RUNNING CONTAINER BEFORE THIS FILE EXISTED. `curl
// http://127.0.0.1:8080/l/CAPABILITY7XY` wrote
//
//	{"time":"…","level":"INFO","msg":"request","method":"GET",
//	 "path":"/l/CAPABILITY7XY","status":404,…}
//
// to the api's stdout. `internal/logging`'s attribute redactor decides on the
// attribute KEY — `token`, `passphrase`, `authorization` — and the key here is
// `path`, so it can never fire on this. Four sites print `r.URL.Path` and the
// plan's own text called the fix singular.
//
// THE RATE LIMITER'S LINE IS THE SHARP ONE. It fires precisely when somebody
// is enumerating tokens, so without the redaction the one line written during
// an attack is the line recording the capability being attacked.
//
// THE POSITIVE CONTROL IS FIRST IN EVERY LEG AND IT IS NOT CEREMONY.
// `strings.Contains(logs, "CAPABILITY7XY") == false` passes against an empty
// buffer just as well as against a redacted one, and an absence assertion is
// the easiest kind to write so that it cannot fail — this repository has
// caught that exact shape twice (the arc's 304 body, R2's checksum leg). So
// each leg asserts the redacted form is PRESENT before it asserts the token is
// absent, and the mutation "delete the provocation" reddens it.
package httpapi

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"travellog/internal/httpx"
)

// theCapability is a share token that could not occur by accident anywhere
// else in a log line, so a Contains check is about this and nothing else.
const theCapability = "CAPABILITY7XY"

const capabilityPath = "/l/" + theCapability

// redacted is written out as a LITERAL rather than read off
// httpx.RedactedSharePath, and that is the opposite of this project's usual
// rule about a number living in two places. The reason is what the assertion
// is ABOUT: the bytes an operator greps for. Interpolating the constant would
// make this leg agree with whatever httpx currently says, so re-pointing that
// constant at `/l/x` would stay green here and silently break
// `make logs | grep -c '/l/\[redacted\]'`, which is R8's own acceptance
// check and cannot read a Go constant.
const redacted = "/l/[redacted]"

func TestNoLogSiteEverWritesSomethingThatLooksLikeAShareToken(t *testing.T) {
	for _, site := range []struct {
		name    string
		provoke func(*testing.T, *slog.Logger)
	}{
		{"access log", provokeAccessLog},
		{"recover", provokePanic},
		{"rate limiter", provokeRateLimit},
		{"rate limiter, unkeyable", provokeUnkeyable},
		{"auth failure", provokeAuthFailure},
	} {
		t.Run(site.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			site.provoke(t, log)

			if !strings.Contains(buf.String(), redacted) {
				t.Fatalf("%s wrote no redacted path at all — the leg is not measuring\n"+
					"anything, and an absence assertion over an empty buffer is the\n"+
					"easiest test in the world to pass. Log was: %s", site.name, buf.String())
			}
			if strings.Contains(buf.String(), theCapability) {
				t.Errorf("%s wrote the share capability to the log.\n"+
					"    A share token is a pure bearer capability: whoever reads this\n"+
					"    file reads the trip. Log was: %s", site.name, buf.String())
			}
		})
	}
}

func provokeAccessLog(t *testing.T, log *slog.Logger) {
	t.Helper()
	serve(httpx.AccessLog(log)(okHandler()))
}

func provokePanic(t *testing.T, log *slog.Logger) {
	t.Helper()
	serve(httpx.Recover(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("the public read fell over")
	})))
}

// provokeRateLimit spends the one token this limiter has and then asks again,
// which is the WARN line — the one written while somebody is enumerating.
func provokeRateLimit(t *testing.T, log *slog.Logger) {
	t.Helper()
	limited := httpx.RateLimit(httpx.NewLimiter(1, nil), log)(okHandler())
	serve(limited)
	serve(limited)
}

// provokeUnkeyable is the OTHER line in RateLimitBy, and it is a separate leg
// because it is a separate call to the logger: a middleware mounted where the
// fact it keys on is not on the request yet answers 500 and writes an ERROR
// line of its own. Redacting one and not the other is a mutation this leg
// reddens on and the one above does not.
func provokeUnkeyable(t *testing.T, log *slog.Logger) {
	t.Helper()
	serve(httpx.RateLimitBy(httpx.NewLimiter(10, nil), log, "traveller",
		func(*http.Request) (string, bool) { return "", false })(okHandler()))
}

// provokeAuthFailure calls the site directly. There is no authenticated route
// under `/l/`, and inventing one to reach this line would be testing a mux
// entry rather than the log site — `logFailure` takes the request, so the
// request is what this hands it.
func provokeAuthFailure(t *testing.T, log *slog.Logger) {
	t.Helper()
	logFailure(httptest.NewRequest(http.MethodGet, capabilityPath, nil), log,
		errors.New("the store is unreachable"))
}

func serve(h http.Handler) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, capabilityPath, nil))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// THE FIVE LEGS ABOVE ARE ABOUT FIVE SITES THAT EXIST; THIS ONE IS ABOUT THE
// SIXTH (PD-08).
//
// A leg per site is a list somebody has to remember to add to, which is the
// same objection docs/PUBLIC-ENVELOPE.md makes to a deny-list. Every log line
// in this application that names a path writes it under the attribute key
// `path`, so the rule is expressible structurally: the second argument of
// `slog.String("path", …)` in lib code is a call to `LoggedPath` and nothing
// else. A handler added next month inherits the guard by writing the line.
//
// IT IS AN AST WALK AND NOT A GREP, for internal/httpx/sweep_test.go's reason:
// a grep matches its own source and cannot tell an argument from a mention.
// Two of this project's acceptance greps have been red against correct work.
//
// TEST FILES ARE EXCLUDED. This file constructs raw paths deliberately.
func TestEveryLogSiteThatNamesAPathAsksLoggedPathForIt(t *testing.T) {
	root := moduleRootFromHTTPAPI(t)
	sites := 0

	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall || len(call.Args) != 2 || !isSlogString(call.Fun) {
					return true
				}
				key, isLit := call.Args[0].(*ast.BasicLit)
				if !isLit || key.Value != `"path"` {
					return true
				}
				sites++
				if !isLoggedPathCall(call.Args[1]) {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s writes a `path` attribute that did not come from\n"+
						"    LoggedPath. `GET /l/{token}` puts a bearer capability in the\n"+
						"    request line, and a site that prints it raw writes that\n"+
						"    capability to disk for as long as the logs are kept.", rel)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	// THE POSITIVE CONTROL, for the reason each leg above has one: a walk that
	// matched nothing would pass this test against an application that had
	// stopped logging paths altogether, or against a matcher that had stopped
	// matching. Four sites are named in this file; the count is a floor rather
	// than an equality, because a fifth site arriving is exactly the case the
	// walk exists to cover and it must not have to edit a number here.
	if sites < 4 {
		t.Fatalf("the walk found %d `path` log attributes in lib code, want at least 4 — "+
			"the matcher has stopped matching and this leg is measuring nothing", sites)
	}
}

func isSlogString(fun ast.Expr) bool {
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "String" {
		return false
	}
	pkg, isIdent := sel.X.(*ast.Ident)
	return isIdent && pkg.Name == "slog"
}

// isLoggedPathCall accepts both spellings: `LoggedPath(r)` inside httpx and
// `httpx.LoggedPath(r)` everywhere else.
func isLoggedPathCall(arg ast.Expr) bool {
	call, isCall := arg.(*ast.CallExpr)
	if !isCall {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "LoggedPath"
	case *ast.SelectorExpr:
		return fun.Sel.Name == "LoggedPath"
	}
	return false
}

func moduleRootFromHTTPAPI(t *testing.T) string {
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
