package admin

import (
	"embed"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Templates is one parsed set per page, each page sharing the layout. One set
// per page because every page defines a block called content.
type Templates struct {
	pages map[string]*template.Template
	log   *slog.Logger
}

func NewTemplates(log *slog.Logger) (*Templates, error) {
	names, err := fs.Glob(templateFS, "templates/*.gohtml")
	if err != nil {
		return nil, err
	}

	t := &Templates{pages: map[string]*template.Template{}, log: log}
	for _, file := range names {
		name := strings.TrimSuffix(path.Base(file), ".gohtml")
		if name == "layout" {
			continue
		}
		set, err := template.New("layout").ParseFS(templateFS, "templates/layout.gohtml", file)
		if err != nil {
			return nil, err
		}
		t.pages[name] = set
	}
	return t, nil
}

// Names is every page this set can draw, sorted so a test reads the same way
// twice.
func (t *Templates) Names() []string {
	out := make([]string, 0, len(t.pages))
	for name := range t.pages {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (t *Templates) Execute(w io.Writer, name string, data any) error {
	set, ok := t.pages[name]
	if !ok {
		return fs.ErrNotExist
	}
	return set.ExecuteTemplate(w, "layout", data)
}

// Page writes a whole page. A template that fails mid-write has already sent
// its status, so the failure is logged rather than turned into a second one.
func (t *Templates) Page(w http.ResponseWriter, status int, name string, data any) {
	d, ok := data.(PageData)
	if !ok {
		d = PageData{}
	}
	if d.Title == "" {
		d.Title = strings.ToUpper(name[:1]) + name[1:]
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.Execute(w, name, d); err != nil {
		t.log.Error("admin: rendering a page",
			slog.String("page", name), slog.String("err", err.Error()))
	}
}

// StaticHandler serves the vendored assets, which are the only third-party
// bytes the panel loads and are served from this origin so the CSP can say so.
func StaticHandler() http.Handler {
	return http.StripPrefix("/admin/", http.FileServerFS(staticFS))
}
