// The template set: it parses, every page executes, and the CSRF token and
// the vendored assets are actually there.
package admin_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"travellog/internal/admin"
)

func templates(t *testing.T) *admin.Templates {
	t.Helper()
	set, err := admin.NewTemplates(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("parsing the template set: %v", err)
	}
	return set
}

// sample fills every field, so a page referencing one that does not exist
// fails here rather than on the screen.
func sample() admin.PageData {
	return admin.PageData{
		Title:    "Sample",
		CSRF:     "csrf-token-value",
		SignedIn: true,
		Cards:    []admin.Card{{Label: "Travellers", Value: "13"}},
		Sessions: []admin.SessionRow{{Email: "ada@example.com",
			CreatedAt: "2026-08-30", LastUsedAt: "2026-08-30"}},
	}
}

func TestEveryTemplateParsesAndExecutes(t *testing.T) {
	set := templates(t)

	names := set.Names()
	if len(names) == 0 {
		t.Fatal("the set holds no pages, so this test proves nothing")
	}
	for _, name := range names {
		if err := set.Execute(io.Discard, name, sample()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheLayoutCarriesTheCSRFTokenForHtmx(t *testing.T) {
	var out strings.Builder
	if err := templates(t).Execute(&out, "dashboard", sample()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), `hx-headers='{"X-CSRF-Token": "csrf-token-value"}'`) {
		t.Error("the layout does not attach the CSRF token, so every htmx request " +
			"the panel makes would be refused by requireCSRF")
	}
}

func TestASignedOutPageDrawsNoNavigation(t *testing.T) {
	data := sample()
	data.SignedIn = false

	var out strings.Builder
	if err := templates(t).Execute(&out, "login", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Sign out") {
		t.Error("the login page offers a sign-out, so the nav is not gated on SignedIn")
	}
}

func TestTheVendoredAssetsAreServedFromThisOrigin(t *testing.T) {
	h := admin.StaticHandler()

	for path, want := range map[string]string{
		"/admin/static/htmx.min.js": "htmx",
		"/admin/static/admin.css":   "--surface-app",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
}

func TestTheStylesheetTranscribesEveryColourToken(t *testing.T) {
	rec := httptest.NewRecorder()
	admin.StaticHandler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/admin/static/admin.css", nil))

	css := rec.Body.String()
	for _, token := range []string{
		"--ink900", "--ink850", "--ink800", "--ink700", "--ink600", "--ink500",
		"--paper000", "--paper100", "--paper200",
		"--grey400", "--grey300", "--grey200",
		"--sky100", "--sky200", "--sky300", "--sky400",
		"--white-a08", "--white-a14", "--white-a70", "--transparent",
		"--border-control", "--scrim-chip",
		"--surface-app", "--surface-sheet", "--surface-raised", "--surface-field",
		"--surface-canvas", "--text-primary", "--text-secondary", "--text-tertiary",
		"--text-on-inverse", "--border-hairline", "--accent-action",
		"--accent-action-text",
	} {
		if !strings.Contains(css, token+":") {
			t.Errorf("the stylesheet is missing %s, which the client's app_colors.dart defines", token)
		}
	}
}
