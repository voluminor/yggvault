package view

import (
	"bytes"
	"embed"
	"html/template"
)

// // // // // // // // // //

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed templates/style.css
var styleCSS string

// // // // // // // // // //

// RendererObj holds parsed templates and embedded CSS; build it once with New.
// It is safe for concurrent reads because html/template.Execute is concurrent-safe.
type RendererObj struct {
	tmpl *template.Template
	css  template.CSS
}

// // // // // // // // // //

// New parses all templates and caches embedded CSS for application startup.
func New() (*RendererObj, error) {
	tmplObj, err := template.New("view").ParseFS(templatesFS, "templates/*.gohtml")
	if err != nil {
		return nil, err
	}
	return &RendererObj{tmpl: tmplObj, css: template.CSS(styleCSS)}, nil
}

func (obj *RendererObj) render(name string, viewModel any) ([]byte, error) {
	var bufObj bytes.Buffer
	if err := obj.tmpl.ExecuteTemplate(&bufObj, name, viewModel); err != nil {
		return nil, err
	}
	return bufObj.Bytes(), nil
}

// // // // // // // // // //

// Catalog renders the key catalog page.
func (obj *RendererObj) Catalog(inputObj CatalogObj) ([]byte, error) {
	return obj.render("catalog", buildCatalog(inputObj, obj.css))
}

// Key renders one key page with versions, pagination, and install snippets.
func (obj *RendererObj) Key(inputObj KeyObj) ([]byte, error) {
	return obj.render("key", buildKey(inputObj, obj.css))
}

// Version renders one version page with integrity, downloads, install snippets, notes, and history navigation.
func (obj *RendererObj) Version(inputObj VersionObj) ([]byte, error) {
	return obj.render("version", buildVersion(inputObj, obj.css))
}

// MetricsIndex renders the metric group list page.
func (obj *RendererObj) MetricsIndex(inputObj MetricsObj) ([]byte, error) {
	return obj.render("metrics", buildMetrics(inputObj, obj.css))
}
