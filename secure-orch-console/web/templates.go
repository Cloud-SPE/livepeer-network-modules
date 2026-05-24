package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

var zeroTime time.Time

type templateSet struct {
	tmpl *template.Template
}

func loadTemplates() (*templateSet, error) {
	funcs := template.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"anchorID": func(parts ...string) string {
			var b strings.Builder
			for i, part := range parts {
				if i > 0 {
					b.WriteByte('-')
				}
				for _, r := range strings.ToLower(part) {
					switch {
					case unicode.IsLetter(r), unicode.IsDigit(r):
						b.WriteRune(r)
					default:
						b.WriteByte('-')
					}
				}
			}
			return b.String()
		},
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

func staticHandler(version string) http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Errorf("static sub: %w", err))
	}
	return versionedAssetHandler(sub, version)
}

func versionedAssetHandler(fsys fs.FS, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if name == "." || name == "/" {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		sum := sha256.Sum256(body)
		etag := fmt.Sprintf("\"%x\"", sum[:])
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", assetCacheControl(version))
		if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		http.ServeContent(w, r, name, zeroTime, bytes.NewReader(body))
	})
}

func assetCacheControl(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return "no-cache"
	}
	return "public, max-age=31536000, immutable"
}
