package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type templateSet struct {
	tmpl *template.Template
}

func loadTemplates() (*templateSet, error) {
	funcs := template.FuncMap{
		"upper":   strings.ToUpper,
		"lower":   strings.ToLower,
		"prettyJSON": func(v any) (template.HTML, error) {
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return "", err
			}
			escaped := template.HTMLEscapeString(string(b))
			return template.HTML(escaped), nil
		},
	}
	t, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &templateSet{tmpl: t}, nil
}

func (ts *templateSet) render(w io.Writer, name string, data any) error {
	var buf bytes.Buffer
	if err := ts.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	if rw, ok := w.(http.ResponseWriter); ok {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, err := buf.WriteTo(w)
	return err
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Errorf("static sub: %w", err))
	}
	return http.FileServer(http.FS(sub))
}
