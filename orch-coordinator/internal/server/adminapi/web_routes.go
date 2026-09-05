package adminapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/providers/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/server/adminapi/web"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/diff"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/receive"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/roster"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

// WebDeps bundles the read-only services the web UI handlers need.
type WebDeps struct {
	Builder        *candidate.Builder
	Scrape         *scrape.Service
	Published      *published.Store
	Audit          *audit.Log
	Receive        *receive.Service
	OrchEthAddress string
	SecureOrchURL  string
	Version        string
	// Hotzone wires the operator pages that manage runners and offers
	// over the broker admin API (plan 0043 §3.6). Absent means the
	// broker admin surface is not configured and those pages are not
	// registered at all.
	Hotzone *HotzoneDeps
}

// HotzoneDeps is the broker-admin half of the console.
type HotzoneDeps struct {
	Admin   brokeradmin.Client
	Brokers []HotzoneBroker
	Timeout time.Duration
}

// HotzoneBroker is one broker the console can manage.
type HotzoneBroker struct {
	Name          string
	BaseURL       string
	Administrable bool
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
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", versionedAssetHandler(assets, deps.Version)))
	s.mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		renderPage(w, pages["login"], loginPage{
			pageHeader: pageHeader{Title: "Operator login", Version: deps.Version},
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
				pageHeader: pageHeader{Title: "Operator login", Version: deps.Version},
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
			MaxAge:   int(sessionAbsoluteTTL / time.Second),
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
		renderPage(w, pages["overview"], buildOverviewPage(deps, r))
	}))
	s.mux.HandleFunc("GET /roster", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["roster"], buildRosterPage(deps, r))
	}))
	s.mux.HandleFunc("GET /diff", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["diff"], buildDiffPage(deps, r))
	}))
	s.mux.HandleFunc("GET /audit", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, pages["audit"], buildAuditPage(deps, r))
	}))
	if deps.Hotzone != nil {
		hz := hotzoneDeps{Admin: deps.Hotzone.Admin, Timeout: deps.Hotzone.Timeout}
		if hz.Timeout <= 0 {
			hz.Timeout = 10 * time.Second
		}
		for _, b := range deps.Hotzone.Brokers {
			hz.Brokers = append(hz.Brokers, brokerTarget(b))
		}
		s.registerHotzoneRoutes(pages, deps, hz)
	}
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
	funcs := template.FuncMap{
		// gib renders a byte count the way an operator reads a GPU spec.
		"gib": func(b uint64) string {
			if b == 0 {
				return "—"
			}
			return fmt.Sprintf("%.0f GiB", float64(b)/(1<<30))
		},
		// kv renders a declared value compactly: the runner facts these
		// pages show are strings, string lists, and small objects.
		"kv": func(v any) string {
			switch t := v.(type) {
			case nil:
				return "—"
			case string:
				return t
			case []any:
				parts := make([]string, 0, len(t))
				for _, item := range t {
					parts = append(parts, fmt.Sprint(item))
				}
				return strings.Join(parts, ", ")
			case map[string]any:
				keys := make([]string, 0, len(t))
				for k := range t {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, fmt.Sprintf("%s=%v", k, t[k]))
				}
				return strings.Join(parts, " ")
			default:
				return fmt.Sprint(t)
			}
		},
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
	}
	partial, err := fs.ReadFile(web.FS, "templates/_hotzone.html")
	if err != nil {
		return nil, fmt.Errorf("read hotzone partial: %w", err)
	}
	out := make(map[string]*template.Template)
	for _, page := range []string{"overview", "roster", "diff", "audit", "login",
		"runners", "offers", "enroll", "certification"} {
		body, err := fs.ReadFile(web.FS, "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", page, err)
		}
		t, err := template.New(page).Funcs(funcs).Parse(string(layout))
		if err != nil {
			return nil, fmt.Errorf("parse layout for %s: %w", page, err)
		}
		if _, err := t.Parse(string(partial)); err != nil {
			return nil, fmt.Errorf("parse hotzone partial for %s: %w", page, err)
		}
		if _, err := t.Parse(string(body)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		out[page] = t
	}
	return out, nil
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

type pageHeader struct {
	Title          string
	ActivePage     string
	OrchEthAddress string
	Version        string
	Actor          string
}

type rosterPage struct {
	pageHeader
	Rows                 []roster.Row
	BrokerStatus         []scrape.BrokerStatus
	DriftCounts          map[string]int
	DriftKinds           []string
	Filter               roster.Filter
	UploadFlash          *uploadFlash
	RefreshFlash         *actionFlash
	PublishState         string
	PublishTitle         string
	PublishNote          string
	RiskItems            []alertItem
	BrokerAlerts         []alertItem
	CandidateSeq         uint64
	CandidateEthAddress  string
	CandidateCanonHash   string
	HasCandidateIdentity bool
	CycleStage           string
	CycleTitle           string
	CycleNote            string
	CycleEvents          []cycleEventView
	ReconcileSteps       []checkpointStepView
}

type overviewPage struct {
	pageHeader
	TotalRows           int
	PublishedRows       int
	BrokerCount         int
	DriftCounts         map[string]int
	CandidateSeq        uint64
	CandidateEthAddress string
	CandidateCanonHash  string
	HasCandidate        bool
	PublishedSeq        uint64
	HasPublished        bool
	LatestAuditOutcome  string
	LatestAuditAt       string
	PublishState        string
	PublishTitle        string
	PublishNote         string
	RiskItems           []alertItem
	BrokerAlerts        []alertItem
	CycleStage          string
	CycleTitle          string
	CycleNote           string
	CycleEvents         []cycleEventView
	ReconcileSteps      []checkpointStepView
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

type alertItem struct {
	Message string
	Href    string
}

type cycleEventView struct {
	Anchor  string
	Outcome string
	At      string
	Actor   string
	Note    string
}

type checkpointStepView struct {
	Label  string
	Status string
	Note   string
	Href   string
}

func buildOverviewPage(deps WebDeps, r *http.Request) overviewPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	var snap scrape.Snapshot
	if deps.Scrape != nil {
		snap = deps.Scrape.Snapshot()
	}
	view, _ := roster.BuildView(deps.OrchEthAddress, cand, pub, snap)
	if view == nil {
		view = &roster.View{OrchEthAddress: deps.OrchEthAddress, DriftCounts: map[string]int{}}
	}
	var events []audit.Event
	if deps.Audit != nil {
		events, _ = deps.Audit.Recent(20)
	}
	out := overviewPage{
		pageHeader: pageHeader{
			Title:          "Overview",
			ActivePage:     "overview",
			OrchEthAddress: deps.OrchEthAddress,
			Version:        deps.Version,
			Actor:          actorFromRequest(r),
		},
		TotalRows:   len(view.Rows),
		BrokerCount: len(view.BrokerStatus),
		DriftCounts: view.DriftCounts,
	}
	for _, row := range view.Rows {
		if row.Published {
			out.PublishedRows++
		}
	}
	if cand != nil {
		out.HasCandidate = true
		out.CandidateSeq = cand.PublicationSeq
		out.CandidateEthAddress = cand.Orch.EthAddress
		out.CandidateCanonHash = manifestCanonicalHash(cand)
	}
	if pub != nil {
		out.HasPublished = true
		out.PublishedSeq = pub.PublicationSeq
	}
	if len(events) > 0 {
		out.LatestAuditOutcome = string(events[0].Outcome)
		out.LatestAuditAt = events[0].At.UTC().Format(time.RFC3339)
	}
	out.PublishState, out.PublishTitle, out.PublishNote = assessPublishReadiness(view, out.HasCandidate, out.HasPublished)
	out.RiskItems = collectDriftAlerts(view.Rows)
	out.BrokerAlerts = collectBrokerAlerts(view.BrokerStatus)
	out.CycleStage, out.CycleTitle, out.CycleNote = assessCoordinatorCycle(
		out.HasCandidate,
		out.CandidateSeq,
		out.HasPublished,
		out.PublishedSeq,
		downloadedCandidate(events, out.CandidateCanonHash),
		signedReturned(events, out.CandidateCanonHash),
	)
	out.CycleEvents = cycleTimeline(events, out.CandidateCanonHash)
	out.ReconcileSteps = coordinatorChecklist(out.CandidateCanonHash, events, out.CycleEvents, deps.SecureOrchURL)
	return out
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
		Protocol:            strings.TrimSpace(q.Get("protocol")),
		BrokerName:          strings.TrimSpace(q.Get("broker")),
		DriftKind:           strings.TrimSpace(q.Get("drift")),
	}
	out := view.Apply(filter)
	driftKinds := []string{
		diff.DriftNone, diff.DriftAdded, diff.DriftRemoved,
		diff.DriftPriceChanged, diff.DriftProtocolChanged,
		diff.DriftExtraChanged, diff.DriftWorkerChanged,
	}
	if len(out.DriftCounts) == 0 {
		out.DriftCounts = view.DriftCounts
	}
	publishState, publishTitle, publishNote := assessPublishReadiness(view, cand != nil, pub != nil)
	page := rosterPage{
		pageHeader: pageHeader{
			Title:          "Roster",
			ActivePage:     "roster",
			OrchEthAddress: deps.OrchEthAddress,
			Version:        deps.Version,
			Actor:          actorFromRequest(r),
		},
		Rows:                 out.Rows,
		BrokerStatus:         view.BrokerStatus,
		DriftCounts:          out.DriftCounts,
		DriftKinds:           driftKinds,
		Filter:               filter,
		UploadFlash:          readUploadFlash(r),
		RefreshFlash:         readRefreshFlash(r),
		PublishState:         publishState,
		PublishTitle:         publishTitle,
		PublishNote:          publishNote,
		RiskItems:            collectDriftAlerts(out.Rows),
		BrokerAlerts:         collectBrokerAlerts(view.BrokerStatus),
		HasCandidateIdentity: cand != nil,
	}
	if cand != nil {
		page.CandidateSeq = cand.PublicationSeq
		page.CandidateEthAddress = cand.Orch.EthAddress
		page.CandidateCanonHash = manifestCanonicalHash(cand)
	}
	var events []audit.Event
	if deps.Audit != nil {
		events, _ = deps.Audit.Recent(20)
	}
	publishedSeq := uint64(0)
	if pub != nil {
		publishedSeq = pub.PublicationSeq
	}
	page.CycleStage, page.CycleTitle, page.CycleNote = assessCoordinatorCycle(
		cand != nil,
		page.CandidateSeq,
		pub != nil,
		publishedSeq,
		downloadedCandidate(events, page.CandidateCanonHash),
		signedReturned(events, page.CandidateCanonHash),
	)
	page.CycleEvents = cycleTimeline(events, page.CandidateCanonHash)
	page.ReconcileSteps = coordinatorChecklist(page.CandidateCanonHash, events, page.CycleEvents, deps.SecureOrchURL)
	return page
}

func assessPublishReadiness(view *roster.View, hasCandidate, hasPublished bool) (state, title, note string) {
	if !hasCandidate {
		return "warn", "Candidate not built", "Refresh the roster and rebuild the candidate before carrying anything to secure-orch."
	}
	if view == nil || len(view.Rows) == 0 {
		return "warn", "No roster rows", "The candidate currently contains no capability tuples. Verify broker availability before continuing."
	}
	hasBrokerIssue := false
	for _, broker := range view.BrokerStatus {
		if broker.Freshness != "ok" || broker.LastError != "" || broker.HealthError != "" {
			hasBrokerIssue = true
			break
		}
	}
	changed := 0
	for kind, count := range view.DriftCounts {
		if kind != diff.DriftNone {
			changed += count
		}
	}
	if changed == 0 && hasPublished {
		return "warn", "No-op publish", "The current candidate does not change the published manifest. Review whether a hand-carry sign cycle is still necessary."
	}
	if view.DriftCounts[diff.DriftRemoved] > 0 {
		return "warn", "Destructive drift present", "One or more tuples would be removed on the next publish. Review the candidate diff carefully before carrying it to secure-orch."
	}
	if hasBrokerIssue {
		return "warn", "Broker health needs review", "The candidate exists, but one or more brokers are stale or reporting health issues. Review roster details before the next publish."
	}
	if !hasPublished {
		return "ok", "Ready for first publish", "The candidate is built and no published manifest exists yet. Carry the candidate to secure-orch for the initial sign cycle."
	}
	return "ok", "Ready for secure-orch review", "Candidate and broker state look healthy enough for operator review. Continue with candidate diff inspection, then hand-carry to secure-orch."
}

func assessCoordinatorCycle(hasCandidate bool, candidateSeq uint64, hasPublished bool, publishedSeq uint64, downloaded, returned bool) (state, title, note string) {
	switch {
	case hasCandidate && !hasPublished:
		if returned {
			return "warn", "Signed manifest returned", "A signed manifest came back from secure-orch for this candidate, but no publish is live yet. Review the audit trail for publish acceptance or failure."
		}
		if downloaded {
			return "warn", "Initial candidate downloaded", "The initial candidate tarball was downloaded from coordinator. The next step is secure-orch review, signing, and upload back here."
		}
		return "warn", "Initial candidate awaiting secure-orch", "A candidate exists locally and no manifest is published yet. Download it, carry it to secure-orch, then return the signed result here."
	case hasCandidate && candidateSeq > publishedSeq:
		if returned {
			return "warn", "Signed manifest returned", "A signed manifest for the newer candidate was uploaded back to coordinator. Check whether publish acceptance succeeded or whether follow-up is needed."
		}
		if downloaded {
			return "warn", "Awaiting signed return", "This candidate has been downloaded from coordinator and is now waiting on secure-orch signing and upload back to coordinator."
		}
		return "warn", "Awaiting secure-orch return", "The candidate is newer than the published manifest. The current stage is secure-orch review, signing, and upload back to coordinator."
	case hasCandidate && hasPublished && candidateSeq == publishedSeq:
		return "ok", "Current candidate already published", "The current candidate identity matches the published manifest. No pending hand-carry return is visible."
	case hasPublished:
		return "info", "Published state only", "A manifest is published, but there is no current candidate in the coordinator view."
	default:
		return "info", "Coordinator idle", "No candidate or published manifest is available yet."
	}
}

func downloadedCandidate(events []audit.Event, manifestHash string) bool {
	if manifestHash == "" {
		return false
	}
	for _, event := range events {
		if event.Outcome == audit.OutcomeCandidateDownloaded && event.ManifestSHA256 == manifestHash {
			return true
		}
	}
	return false
}

func signedReturned(events []audit.Event, manifestHash string) bool {
	if manifestHash == "" {
		return false
	}
	for _, event := range events {
		if event.Outcome == audit.OutcomeSignedReturned && event.ManifestSHA256 == manifestHash {
			return true
		}
	}
	return false
}

func cycleTimeline(events []audit.Event, manifestHash string) []cycleEventView {
	if manifestHash == "" {
		return nil
	}
	out := make([]cycleEventView, 0, 8)
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.ManifestSHA256 != manifestHash {
			continue
		}
		out = append(out, cycleEventView{
			Anchor:  fmt.Sprintf("cycle-%d", len(out)+1),
			Outcome: string(event.Outcome),
			At:      event.At.UTC().Format(time.RFC3339),
			Actor:   event.Actor,
			Note:    event.Note,
		})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func coordinatorChecklist(manifestHash string, events []audit.Event, timeline []cycleEventView, secureOrchURL string) []checkpointStepView {
	hasDownload := downloadedCandidate(events, manifestHash)
	hasReturned := signedReturned(events, manifestHash)
	hasAccepted := acceptedCandidate(events, manifestHash)
	return []checkpointStepView{
		{Label: "1. Candidate downloaded", Status: checkpointStatus(hasDownload), Note: "Recorded in coordinator when candidate.tar.gz is downloaded.", Href: timelineHref(timeline, string(audit.OutcomeCandidateDownloaded))},
		{Label: "2. Candidate loaded on secure-orch", Status: "remote", Note: remoteChecklistNote("Tracked on secure-orch after the upload reaches the cold-key host.", "--secure-orch-url", secureOrchURL), Href: remoteEvidenceHref(secureOrchURL, "/manifests#review-timeline")},
		{Label: "3. Diff reviewed on secure-orch", Status: "remote", Note: remoteChecklistNote("Tracked on secure-orch when the operator opens the candidate diff.", "--secure-orch-url", secureOrchURL), Href: remoteEvidenceHref(secureOrchURL, "/manifests#review-timeline")},
		{Label: "4. Manifest signed on secure-orch", Status: "remote", Note: remoteChecklistNote("Tracked on secure-orch when sign and write_signed complete.", "--secure-orch-url", secureOrchURL), Href: remoteEvidenceHref(secureOrchURL, "/manifests#review-timeline")},
		{Label: "5. Signed manifest returned", Status: checkpointStatus(hasReturned), Note: "Recorded in coordinator when the signed manifest is uploaded back.", Href: timelineHref(timeline, string(audit.OutcomeSignedReturned))},
		{Label: "6. Manifest published", Status: checkpointStatus(hasAccepted), Note: "Recorded in coordinator when publish acceptance completes.", Href: timelineHref(timeline, string(audit.OutcomeAccepted))},
	}
}

func acceptedCandidate(events []audit.Event, manifestHash string) bool {
	if manifestHash == "" {
		return false
	}
	for _, event := range events {
		if event.Outcome == audit.OutcomeAccepted && event.ManifestSHA256 == manifestHash {
			return true
		}
	}
	return false
}

func checkpointStatus(done bool) string {
	if done {
		return "done"
	}
	return "pending"
}

func timelineHref(events []cycleEventView, outcomes ...string) string {
	for _, event := range events {
		for _, outcome := range outcomes {
			if event.Outcome == outcome && event.Anchor != "" {
				return "#" + event.Anchor
			}
		}
	}
	return ""
}

func remoteEvidenceHref(baseURL, suffix string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + suffix
}

func remoteChecklistNote(baseNote, flagName, baseURL string) string {
	if strings.TrimSpace(baseURL) == "" {
		return baseNote + " Set " + flagName + " to enable a direct jump to the peer console."
	}
	return baseNote + " Match canonical_sha256 on arrival before continuing the hand-carry cycle."
}

func collectDriftAlerts(rows []roster.Row) []alertItem {
	alerts := make([]alertItem, 0)
	for _, row := range rows {
		switch row.Drift {
		case diff.DriftRemoved:
			alerts = append(alerts, alertItem{
				Message: row.CapabilityID + " / " + row.OfferingID + " will be removed on the next publish.",
				Href:    "/diff#diff-row-" + anchorID(row.CapabilityID, row.OfferingID),
			})
		case diff.DriftPriceChanged:
			alerts = append(alerts, alertItem{
				Message: row.CapabilityID + " / " + row.OfferingID + " changed price from " + row.OldPriceWei + " to " + row.NewPriceWei + ".",
				Href:    "/diff#diff-row-" + anchorID(row.CapabilityID, row.OfferingID),
			})
		case diff.DriftProtocolChanged:
			alerts = append(alerts, alertItem{
				Message: row.CapabilityID + " / " + row.OfferingID + " changed protocol or declared axes.",
				Href:    "/diff#diff-row-" + anchorID(row.CapabilityID, row.OfferingID),
			})
		case diff.DriftWorkerChanged:
			alerts = append(alerts, alertItem{
				Message: row.CapabilityID + " / " + row.OfferingID + " now points to a different worker URL.",
				Href:    "/diff#diff-row-" + anchorID(row.CapabilityID, row.OfferingID),
			})
		}
		if len(alerts) >= 6 {
			break
		}
	}
	return alerts
}

func collectBrokerAlerts(brokers []scrape.BrokerStatus) []alertItem {
	alerts := make([]alertItem, 0)
	for _, broker := range brokers {
		switch {
		case broker.Freshness != scrape.FreshnessOK:
			alerts = append(alerts, alertItem{
				Message: broker.Name + " is " + broker.Freshness + " and needs review before the next publish.",
				Href:    "#broker-" + anchorID(broker.Name),
			})
		case broker.HealthError != "":
			alerts = append(alerts, alertItem{
				Message: broker.Name + " health check failed: " + broker.HealthError,
				Href:    "#broker-" + anchorID(broker.Name),
			})
		case broker.LastError != "":
			alerts = append(alerts, alertItem{
				Message: broker.Name + " scrape error: " + broker.LastError,
				Href:    "#broker-" + anchorID(broker.Name),
			})
		}
		if len(alerts) >= 6 {
			break
		}
	}
	return alerts
}

func anchorID(parts ...string) string {
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
}

func manifestCanonicalHash(m *types.ManifestPayload) string {
	if m == nil {
		return ""
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	canon, err := candidate.CanonicalBytesFromJSON(raw)
	if err != nil {
		return ""
	}
	return candidate.SHA256Hex(canon)
}

func buildDiffPage(deps WebDeps, r *http.Request) diffPage {
	cand := getCandidatePayload(deps)
	pub := readPublishedPayload(deps)
	res, _ := diff.Compute(cand, pub)
	if res == nil {
		res = &diff.Result{Counts: map[string]int{}}
	}
	return diffPage{
		pageHeader:  pageHeader{Title: "Diff", ActivePage: "diff", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version, Actor: actorFromRequest(r)},
		Rows:        res.Rows,
		DriftCounts: res.Counts,
	}
}

func buildAuditPage(deps WebDeps, r *http.Request) auditPage {
	events, _ := deps.Audit.Recent(50)
	return auditPage{
		pageHeader: pageHeader{Title: "Audit", ActivePage: "audit", OrchEthAddress: deps.OrchEthAddress, Version: deps.Version, Actor: actorFromRequest(r)},
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

// renderPage buffers the render so a template error becomes a clean
// 500 instead of a half-written page.
//
// html/template writes as it executes, so executing straight into the
// ResponseWriter meant a failure mid-template shipped 200 OK with the
// page truncated at the failure point and the Go error text pasted into
// the body — followed by a superfluous WriteHeader. An operator saw a
// plausible-looking page missing everything below the fault.
func renderPage(w http.ResponseWriter, tmpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, fmt.Sprintf("render: %s", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Vary", "Cookie")
	_, _ = w.Write(buf.Bytes())
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
	http.Redirect(w, r, "/roster?"+q.Encode(), http.StatusSeeOther)
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
	http.Redirect(w, r, "/roster?"+q.Encode(), http.StatusSeeOther)
}
