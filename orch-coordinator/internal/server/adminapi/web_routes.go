package adminapi

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/server/adminapi/web"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/diff"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/receive"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/roster"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// WebDeps bundles the read-only services the web UI handlers need.
type WebDeps struct {
	Builder        *candidate.Builder
	Scrape         *scrape.Service
	Published      *published.Store
	Audit          *audit.Log
	Receive        *receive.Service
	OrchEthAddress string
	Version        string
}

// WebRoutes wires the operator-facing web UI onto the admin mux.
func (s *Server) WebRoutes(deps WebDeps) error {
	pages, err := loadTemplates()
	if err != nil {
		return fmt.Errorf("adminapi: load templates: %w", err)
	}
	assets, err := fs.Sub(web.FS, "assets")
	if err != nil {
		return fmt.Errorf("adminapi: assets sub: %w", err)
	}
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	s.mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		renderPage(w, pages["login"], loginPage{
			pageHeader: pageHeader{Title: "Operator login"},
			Error:      "",
		})
	})
	s.mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "parse form", http.StatusBadRequest)
			return
		}
		sessionID, err := s.auth.login(r.PostForm.Get("admin_token"), r.PostForm.Get("actor"))
		if err != nil {
			renderPage(w, pages["login"], loginPage{
				pageHeader: pageHeader{Title: "Operator login"},
				Error:      err.Error(),
			})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	s.mux.HandleFunc("POST /logout", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			s.auth.logout(cookie.Value)
		}
		clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}))

	s.mux.HandleFunc("GET /", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderPage(w, pages["roster"], buildRosterPage(deps, r))
	}))
	s.mux.HandleFunc("GET /diff", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["diff"], buildDiffPage(deps, r))
	}))
	s.mux.HandleFunc("GET /audit", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["audit"], buildAuditPage(deps, r))
	}))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	s.mux.HandleFunc("POST /refresh-roster", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if deps.Scrape == nil || deps.Builder == nil {
			redirectRefreshFeedback(w, r, "error", "refresh is not configured")
			return
		}
		deps.Scrape.ScrapeOnce(r.Context())
		if _, err := deps.Builder.Rebuild(); err != nil {
			redirectRefreshFeedback(w, r, "error", "candidate rebuild failed: "+err.Error())
			return
		}
		redirectRefreshFeedback(w, r, "accepted", "fetched latest broker state and rebuilt candidate")
	}))
	s.mux.HandleFunc("POST /upload-signed-manifest", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if deps.Receive == nil {
			redirectUploadFeedback(w, r, "error", "signed-manifest receive service is not configured", 0)
			return
		}
		res, outcome, msg, status := receiveUpload(deps.Receive, r)
		if status == http.StatusOK && res != nil {
			redirectUploadFeedback(w, r, "accepted", "published manifest", res.PublicationSeq)
			return
		}
		redirectUploadFeedback(w, r, string(outcome), msg, 0)
	}))
	return nil
}

// loadTemplates parses one template tree per page so each tree
// carries the correct {{define "content"}} override.
func loadTemplates() (map[string]*template.Template, error) {
	layout, err := fs.ReadFile(web.FS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	out := make(map[string]*template.Template)
	for _, page := range []string{"roster", "diff", "audit", "login"} {
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
	return out, nil
}

type pageHeader struct {
	Title          string
	OrchEthAddress string
	Version        string
	Actor          string
}

type rosterPage struct {
	pageHeader
	Rows         []roster.Row
	BrokerStatus []scrape.BrokerStatus
	DriftCounts  map[string]int
	DriftKinds   []string
	Filter       roster.Filter
	UploadFlash  *uploadFlash
	RefreshFlash *actionFlash
}

type diffPage struct {
	pageHeader
	Rows        []diff.Row
	DriftCounts map[string]int
}

type auditPage struct {
	pageHeader
	Events []audit.Event
}

type loginPage struct {
	pageHeader
	Error string
}

type uploadFlash struct {
	Outcome        string
	Message        string
	PublicationSeq uint64
}

type actionFlash struct {
	Outcome string
	Message string
}

func buildRosterPage(deps WebDeps, r *http.Request) rosterPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	var snap scrape.Snapshot
	if deps.Scrape != nil {
		snap = deps.Scrape.Snapshot()
	}
	view, _ := roster.BuildView(deps.OrchEthAddress, cand, pub, snap)
	if view == nil {
		view = &roster.View{OrchEthAddress: deps.OrchEthAddress}
	}
	q := r.URL.Query()
	filter := roster.Filter{
		CapabilitySubstring: strings.TrimSpace(q.Get("q")),
		Mode:                strings.TrimSpace(q.Get("mode")),
		BrokerName:          strings.TrimSpace(q.Get("broker")),
		DriftKind:           strings.TrimSpace(q.Get("drift")),
	}
	out := view.Apply(filter)
	driftKinds := []string{
		diff.DriftNone, diff.DriftAdded, diff.DriftRemoved,
		diff.DriftPriceChanged, diff.DriftModeChanged,
		diff.DriftExtraChanged, diff.DriftWorkerChanged,
	}
	if len(out.DriftCounts) == 0 {
		out.DriftCounts = view.DriftCounts
	}
	return rosterPage{
		pageHeader: pageHeader{
			Title:          "Roster",
			OrchEthAddress: deps.OrchEthAddress,
			Version:        deps.Version,
			Actor:          actorFromRequest(r),
		},
		Rows:         out.Rows,
		BrokerStatus: view.BrokerStatus,
		DriftCounts:  out.DriftCounts,
		DriftKinds:   driftKinds,
		Filter:       filter,
		UploadFlash:  readUploadFlash(r),
		RefreshFlash: readRefreshFlash(r),
	}
}

func buildDiffPage(deps WebDeps, r *http.Request) diffPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	res, _ := diff.Compute(cand, pub)
	if res == nil {
		res = &diff.Result{Counts: map[string]int{}}
	}
	return diffPage{
		pageHeader:  pageHeader{Title: "Diff", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version, Actor: actorFromRequest(r)},
		Rows:        res.Rows,
		DriftCounts: res.Counts,
	}
}

func buildAuditPage(deps WebDeps, r *http.Request) auditPage {
	events, _ := deps.Audit.Recent(50)
	return auditPage{
		pageHeader: pageHeader{Title: "Audit", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version, Actor: actorFromRequest(r)},
		Events:     events,
	}
}

func getCandidatePayload(deps WebDeps) *types.ManifestPayload {
	if deps.Builder == nil {
		return nil
	}
	if c := deps.Builder.Latest(); c != nil {
		p := c.Manifest
		return &p
	}
	return nil
}

func readPublishedPayload(deps WebDeps) *types.ManifestPayload {
	if deps.Published == nil {
		return nil
	}
	body, _, err := deps.Published.Read()
	if err != nil {
		return nil
	}
	sm, err := types.ParseSignedManifest(body)
	if err != nil {
		return nil
	}
	p := sm.Manifest
	return &p
}

func renderPage(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Vary", "Cookie")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
}

func readUploadFlash(r *http.Request) *uploadFlash {
	q := r.URL.Query()
	outcome := strings.TrimSpace(q.Get("upload_outcome"))
	message := strings.TrimSpace(q.Get("upload_message"))
	if outcome == "" && message == "" {
		return nil
	}
	var seq uint64
	if raw := strings.TrimSpace(q.Get("upload_publication_seq")); raw != "" {
		fmt.Sscanf(raw, "%d", &seq)
	}
	return &uploadFlash{Outcome: outcome, Message: message, PublicationSeq: seq}
}

func redirectUploadFeedback(w http.ResponseWriter, r *http.Request, outcome, message string, publicationSeq uint64) {
	q := make(url.Values)
	q.Set("upload_outcome", outcome)
	q.Set("upload_message", message)
	if publicationSeq > 0 {
		q.Set("upload_publication_seq", fmt.Sprintf("%d", publicationSeq))
	}
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}

func readRefreshFlash(r *http.Request) *actionFlash {
	q := r.URL.Query()
	outcome := strings.TrimSpace(q.Get("refresh_outcome"))
	message := strings.TrimSpace(q.Get("refresh_message"))
	if outcome == "" && message == "" {
		return nil
	}
	return &actionFlash{Outcome: outcome, Message: message}
}

func redirectRefreshFeedback(w http.ResponseWriter, r *http.Request, outcome, message string) {
	q := make(url.Values)
	q.Set("refresh_outcome", outcome)
	q.Set("refresh_message", message)
	http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
}
