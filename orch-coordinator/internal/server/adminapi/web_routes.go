package adminapi

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/server/adminapi/web"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/diff"
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

	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderPage(w, pages["roster"], buildRosterPage(deps, r))
	})
	s.mux.HandleFunc("GET /diff", func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["diff"], buildDiffPage(deps))
	})
	s.mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["audit"], buildAuditPage(deps))
	})
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
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
	for _, page := range []string{"roster", "diff", "audit"} {
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
}

type rosterPage struct {
	pageHeader
	Rows         []roster.Row
	BrokerStatus []scrape.BrokerStatus
	DriftCounts  map[string]int
	DriftKinds   []string
	Filter       roster.Filter
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

func buildRosterPage(deps WebDeps, r *http.Request) rosterPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	view, _ := roster.BuildView(deps.OrchEthAddress, cand, pub, deps.Scrape.Snapshot())
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
		},
		Rows:         out.Rows,
		BrokerStatus: view.BrokerStatus,
		DriftCounts:  out.DriftCounts,
		DriftKinds:   driftKinds,
		Filter:       filter,
	}
}

func buildDiffPage(deps WebDeps) diffPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	res, _ := diff.Compute(cand, pub)
	if res == nil {
		res = &diff.Result{Counts: map[string]int{}}
	}
	return diffPage{
		pageHeader: pageHeader{Title: "Diff", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version},
		Rows:       res.Rows,
		DriftCounts: res.Counts,
	}
}

func buildAuditPage(deps WebDeps) auditPage {
	events, _ := deps.Audit.Recent(50)
	return auditPage{
		pageHeader: pageHeader{Title: "Audit", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version},
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
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
}
