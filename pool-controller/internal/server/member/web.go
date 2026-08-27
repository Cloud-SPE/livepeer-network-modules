package member

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ui/web"
)

// The member portal (plan 0044 §3.6).
//
// It is served from the member listener and nowhere else. The admin mux
// is not mounted here at all, so a mistake in this file cannot expose
// the operator console — that separation is the whole reason the
// listeners were split.

const portalVersion = "dev"

// portalPages are the content templates, keyed by ActivePage.
var portalPages = []string{"start", "hosts", "earnings", "settings"}

// portalPageData is what the shared member shell needs.
type portalPageData struct {
	Title         string
	ActivePage    string
	Version       string
	MemberAddress string
}

func loadPortalTemplates() (map[string]*template.Template, error) {
	layout, err := fs.ReadFile(web.FS, "templates/member-layout.html")
	if err != nil {
		return nil, fmt.Errorf("read member layout: %w", err)
	}
	out := make(map[string]*template.Template, len(portalPages)+1)
	for _, page := range portalPages {
		body, err := fs.ReadFile(web.FS, "templates/member-"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("read member-%s: %w", page, err)
		}
		t, err := template.New(page).Parse(string(layout))
		if err != nil {
			return nil, fmt.Errorf("parse member layout for %s: %w", page, err)
		}
		if _, err := t.Parse(string(body)); err != nil {
			return nil, fmt.Errorf("parse member-%s: %w", page, err)
		}
		out[page] = t
	}
	signin, err := fs.ReadFile(web.FS, "templates/member-signin.html")
	if err != nil {
		return nil, fmt.Errorf("read member-signin: %w", err)
	}
	signinTmpl, err := template.New("signin").Parse(string(signin))
	if err != nil {
		return nil, fmt.Errorf("parse member-signin: %w", err)
	}
	out["signin"] = signinTmpl
	return out, nil
}

// renderPortal buffers before writing, so a template error is a 500
// rather than half a page served with a 200 and the error pasted into
// the body where a member would read it as content.
func renderPortal(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	if tmpl == nil {
		http.Error(w, "render: portal template not loaded", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	var err error
	if name == "" {
		err = tmpl.Execute(&buf, data)
	} else {
		err = tmpl.ExecuteTemplate(&buf, name, data)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	_, _ = w.Write(buf.Bytes())
}

// registerPortalRoutes mounts the pages and their assets.
func registerPortalRoutes(mux *http.ServeMux, deps Deps) {
	templates, err := loadPortalTemplates()
	if err != nil {
		// A portal that cannot render is not a reason to refuse the
		// member API: the agent's routes matter more than the pages,
		// and a host already running must keep working.
		return
	}
	assets, err := fs.Sub(web.FS, "assets")
	if err != nil {
		return
	}
	mux.Handle("GET /member/assets/", http.StripPrefix("/member/assets/", portalAssetHandler(assets)))

	mux.HandleFunc("GET /member/signin", func(w http.ResponseWriter, _ *http.Request) {
		renderPortal(w, templates["signin"], "", struct{ Version string }{Version: portalVersion})
	})
	mux.HandleFunc("POST /member/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(memberSessionCookieName); err == nil && deps.Sessions != nil {
			deps.Sessions.Delete(cookie.Value)
		}
		clearMemberSessionCookie(w)
		http.Redirect(w, r, "/member/signin", http.StatusSeeOther)
	})

	page := func(name, title string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			address, ok := deps.signedInAddress(r)
			if !ok {
				// Not signed in: the portal is useless without an
				// identity, so send them where they can get one rather
				// than rendering an empty shell.
				http.Redirect(w, r, "/member/signin", http.StatusSeeOther)
				return
			}
			renderPortal(w, templates[name], "layout", portalPageData{
				Title: title, ActivePage: name, Version: portalVersion, MemberAddress: address,
			})
		}
	}
	mux.HandleFunc("GET /member", page("start", "Get started"))
	mux.HandleFunc("GET /member/hosts", page("hosts", "Hosts"))
	mux.HandleFunc("GET /member/earnings", page("earnings", "Earnings"))
	mux.HandleFunc("GET /member/settings", page("settings", "Settings"))
}

// signedInAddress resolves the session to the member's own address,
// which is the only identity the portal ever displays.
func (d Deps) signedInAddress(r *http.Request) (string, bool) {
	memberID, ok := memberIDFromRequest(d.Sessions, r)
	if !ok || d.Repo == nil {
		return "", false
	}
	member, err := d.Repo.GetPoolMember(memberID)
	if err != nil {
		return "", false
	}
	return member.EthAddress, true
}

func portalAssetHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		http.FileServer(http.FS(fsys)).ServeHTTP(w, r)
	})
}
