package admin

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ui/web"
)

// uiVersion is the asset/cache-busting version for the operator UI. "dev"
// yields no-cache asset responses, which is appropriate for the pool UI today.
const uiVersion = "dev"

// uiPages lists the operator-UI content templates, keyed by ActivePage.
var uiPages = []string{
	"overview",
	"pool",
	"offers",
	"audit",
}

// pageHeader carries the per-page values the shared shell needs.
type pageHeader struct {
	Title      string
	ActivePage string
	Version    string
	Actor      string
}

// loginPageData is the standalone login page model.
type loginPageData struct {
	Version string
	Error   string
}

// loadTemplates parses one template tree per page so each tree carries the
// correct {{define "content"}} override against the shared layout.
func loadTemplates() (map[string]*template.Template, error) {
	layout, err := fs.ReadFile(web.FS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	out := make(map[string]*template.Template)
	for _, page := range uiPages {
		body, err := fs.ReadFile(web.FS, "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", page, err)
		}
		t, err := template.New(page).Parse(string(layout))
		if err != nil {
			return nil, fmt.Errorf("parse layout for %s: %w", page, err)
		}
		if _, err := t.Parse(string(body)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		out[page] = t
	}
	loginBody, err := fs.ReadFile(web.FS, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("read login: %w", err)
	}
	loginTmpl, err := template.New("login").Parse(string(loginBody))
	if err != nil {
		return nil, fmt.Errorf("parse login: %w", err)
	}
	out["login"] = loginTmpl
	return out, nil
}

func renderLogin(w http.ResponseWriter, tmpl *template.Template, data loginPageData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	if tmpl == nil {
		http.Error(w, "render: login template not loaded", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
}

func renderPage(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	if tmpl == nil {
		http.Error(w, "render: template not loaded", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
}

var zeroTime time.Time

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
