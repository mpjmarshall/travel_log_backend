// The capability in the log, at every site that prints a path.
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

// redacted is written out as a literal rather than read off
// httpx.RedactedSharePath.
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
// Is the warn line — the one written while somebody is enumerating.
func provokeRateLimit(t *testing.T, log *slog.Logger) {
	t.Helper()
	limited := httpx.RateLimit(httpx.NewLimiter(1, nil), log)(okHandler())
	serve(limited)
	serve(limited)
}

// provokeUnkeyable is the other line in RateLimitBy, and it is a separate leg
// It is a separate call to the logger.
func provokeUnkeyable(t *testing.T, log *slog.Logger) {
	t.Helper()
	serve(httpx.RateLimitBy(httpx.NewLimiter(10, nil), log, "traveller",
		func(*http.Request) (string, bool) { return "", false })(okHandler()))
}

// provokeAuthFailure calls the site directly.
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

// The five legs above are about five sites that exist; this one is about the
// sixth.
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
