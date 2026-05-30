package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/protocol"
)

const auditPageSize = 10

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/overview", http.StatusSeeOther)
}

func (s *Server) handleOverviewPage(w http.ResponseWriter, r *http.Request) {
	last, err := loadLastSigned(s.cfg.LastSignedPath)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "load last-signed", err)
		return
	}
	view := overviewPageView{
		pageView: pageView{
			Title:           "Overview",
			ActivePage:      "overview",
			ContentTemplate: "overview-content",
			Actor:           actorFromRequest(r),
			SignerAddress:   s.signer.Address().String(),
			AssetVersion:    s.cfg.Version,
		},
		ProtocolConfigured: s.protocol != nil,
		ProtocolReachable:  false,
		LastSignedPath:     s.cfg.LastSignedPath,
		HasLastSigned:      last != nil,
	}
	if status := s.protocolStatusView(r); status != nil {
		view.ProtocolReachable = status.Health.Available && status.Health.Error == ""
	}
	if last != nil {
		view.LastSignedSummary = summarizeEnvelope(last)
	}
	s.mu.Lock()
	cand := s.candidate
	s.mu.Unlock()
	if cand != nil {
		view.HasCandidate = true
		view.CandidateLoadedAt = cand.loadedAt.Format(time.RFC3339)
		view.CandidateSource = cand.sourceName
		view.CandidateSeq = publicationSeqValue(cand.bytes)
		view.CandidateEthAddress = manifestEthAddress(cand.bytes)
		view.CandidateCanonHash = cand.canonHash
		view.CycleEvents = secureOrchCycleTimeline(s.readRecentAuditEvents(), cand.canonHash)
		view.ReconcileSteps = secureOrchChecklist(view.CycleEvents, s.cfg.CoordinatorURL)
	}
	view.CycleStage, view.CycleTitle, view.CycleNote = assessSecureOrchCycle(view.HasCandidate, view.CandidateSeq, view.HasLastSigned, view.LastSignedSummary.PublicationSeq)
	if err := s.templates.render(w, "page.html", view); err != nil {
		s.logger.Warn("render overview", "err", err)
	}
}

func (s *Server) handleProtocolStatusPage(w http.ResponseWriter, r *http.Request) {
	view := protocolStatusPageView{
		pageView:       s.basePageView(r, "Protocol status", "protocol-status", "protocol-status-content"),
		ProtocolStatus: s.protocolStatusView(r),
	}
	if err := s.templates.render(w, "page.html", view); err != nil {
		s.logger.Warn("render protocol status", "err", err)
	}
}

func (s *Server) handleProtocolActionsPage(w http.ResponseWriter, r *http.Request) {
	view := protocolActionsPageView{
		pageView:               s.basePageView(r, "Protocol actions", "protocol-actions", "protocol-actions-content"),
		ProtocolStatus:         s.protocolStatusView(r),
		ProtocolActionFeedback: protocolActionFeedbackFromRequest(r),
		TxIntentLookup:         buildTxIntentLookupView(r.Context(), s.protocol, r.URL.Query().Get("tx_intent_id")),
	}
	if s.protocol != nil {
		if cfg, err := s.protocol.GetConfig(r.Context()); err != nil {
			view.OperationalConfigError = err.Error()
		} else {
			view.OperationalConfig = &operationalConfigView{
				RoundInitEnabled:      cfg.RoundInitEnabled,
				RewardEnabled:         cfg.RewardEnabled,
				RewardBeforeTransfer:  cfg.RewardBeforeTransfer,
				TransferBondEnabled:   cfg.TransferBondEnabled,
				TransferBondReceiver:  cfg.TransferBondReceiver,
				TransferBondMinRetain: cfg.TransferBondMinRetain,
				WithdrawFeesEnabled:   cfg.WithdrawFeesEnabled,
				WithdrawFeesReceiver:  cfg.WithdrawFeesReceiver,
				WithdrawFeesThreshold: cfg.WithdrawFeesThreshold,
			}
		}
	}
	if err := s.templates.render(w, "page.html", view); err != nil {
		s.logger.Warn("render protocol actions", "err", err)
	}
}

func (s *Server) handleManifestsPage(w http.ResponseWriter, r *http.Request) {
	last, err := loadLastSigned(s.cfg.LastSignedPath)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "load last-signed", err)
		return
	}
	view := manifestsPageView{
		pageView:       s.basePageView(r, "Manifests", "manifests", "manifests-content"),
		LastSignedPath: s.cfg.LastSignedPath,
		HasLastSigned:  last != nil,
	}
	if last != nil {
		view.LastSignedSummary = summarizeEnvelope(last)
	}
	s.mu.Lock()
	cand := s.candidate
	s.mu.Unlock()
	if cand != nil {
		res, err := diff.Compute(last, cand.bytes)
		if err != nil {
			view.CandidateError = err.Error()
		} else {
			view.Candidate = &candidateView{
				LoadedAt:       cand.loadedAt.Format(time.RFC3339),
				SourceName:     cand.sourceName,
				PublicationSeq: res.Header.AfterSeq,
				EthAddress:     strings.ToLower(res.Header.AfterEthAddress),
				CanonHash:      cand.canonHash,
				Diff:           res,
			}
			view.ReviewState, view.ReviewTitle, view.ReviewMessage = assessManifestReview(last != nil, res)
			view.CycleEvents = secureOrchCycleTimeline(s.readRecentAuditEvents(), cand.canonHash)
			view.ReconcileSteps = secureOrchChecklist(view.CycleEvents, s.cfg.CoordinatorURL)
		}
	}
	if view.Candidate == nil && view.CandidateError == "" {
		view.ReviewState = "info"
		view.ReviewTitle = "Waiting for candidate"
		view.ReviewMessage = "Upload the next candidate manifest from orch-coordinator to begin the secure-orch review cycle."
	}
	if view.CandidateError != "" {
		view.ReviewState = "error"
		view.ReviewTitle = "Candidate review blocked"
		view.ReviewMessage = view.CandidateError
	}
	candidateSeq := uint64(0)
	if view.Candidate != nil {
		candidateSeq = view.Candidate.PublicationSeq
	}
	view.CycleStage, view.CycleTitle, view.CycleNote = assessSecureOrchCycle(view.Candidate != nil, candidateSeq, view.HasLastSigned, view.LastSignedSummary.PublicationSeq)
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindViewDiff,
		Actor:      actorFromRequest(r),
		EthAddress: s.signer.Address().String(),
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	if err := s.templates.render(w, "page.html", view); err != nil {
		s.logger.Warn("render manifests", "err", err)
	}
}

func publicationSeqValue(manifestBytes []byte) uint64 {
	if seq := publicationSeq(manifestBytes); seq != nil {
		return *seq
	}
	return 0
}

func manifestEthAddress(manifestBytes []byte) string {
	summary := summarizeEnvelope(manifestBytes)
	return strings.ToLower(summary.EthAddress)
}

func assessManifestReview(hasLastSigned bool, res *diff.Result) (state, title, message string) {
	if res == nil {
		return "info", "Waiting for candidate", "Upload a candidate manifest to begin diff review."
	}
	if !res.Header.SeqMonotonic {
		return "error", "Signing blocked", "publication_seq is not monotonic. Resolver-side verification will reject this manifest until the sequence is corrected."
	}
	if !res.Header.EthAddressStable {
		return "warn", "Identity changed", "orch.eth_address differs from the last signed manifest. Treat this as a rotation event and verify the handoff before signing."
	}
	if hasLastSigned && len(res.Added) == 0 && len(res.Removed) == 0 && len(res.Changed) == 0 {
		return "warn", "No-op candidate", "This candidate does not change any capability tuples relative to the last signed manifest."
	}
	if !hasLastSigned {
		return "ok", "Ready for first sign", "No prior signed manifest exists. Review the initial publication carefully, then complete the tap-to-sign confirmation."
	}
	return "ok", "Ready for sign review", "Header checks passed. Continue with tuple review, then complete the explicit tap-to-sign gesture if the diff matches operator intent."
}

func assessSecureOrchCycle(hasCandidate bool, candidateSeq uint64, hasLastSigned bool, lastSignedSeq uint64) (state, title, note string) {
	switch {
	case hasCandidate && !hasLastSigned:
		return "warn", "Initial sign in progress", "A candidate is loaded locally and no signed manifest exists yet. Review identity and diff details, then complete the first sign cycle."
	case hasCandidate && candidateSeq > lastSignedSeq:
		return "warn", "Awaiting secure-orch sign-off", "A newer candidate is loaded than the last signed publication. Verify the hand-carry identity and complete the sign review."
	case hasCandidate:
		return "info", "Candidate under review", "A candidate is loaded locally. Confirm whether this is a no-op review or a correction before signing."
	case hasLastSigned:
		return "ok", "Waiting for next candidate", "No candidate is loaded locally. The last signed manifest remains on disk and secure-orch is ready for the next hand-carry upload."
	default:
		return "info", "Console idle", "No candidate is loaded and no signed manifest exists yet."
	}
}

func (s *Server) readRecentAuditEvents() []audit.Event {
	events, err := audit.ReadRecent(s.audit.Path(), 64)
	if err != nil {
		return nil
	}
	return events
}

func secureOrchCycleTimeline(events []audit.Event, canonHash string) []cycleEventView {
	if canonHash == "" {
		return nil
	}
	out := make([]cycleEventView, 0, 8)
	for _, event := range events {
		if event.CanonHash != canonHash {
			continue
		}
		out = append(out, cycleEventView{
			Anchor: fmt.Sprintf("cycle-%d", len(out)+1),
			Kind:   string(event.Kind),
			At:     event.At.UTC().Format(time.RFC3339),
			Actor:  event.Actor,
			Note:   event.Note,
		})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func secureOrchChecklist(events []cycleEventView, coordinatorURL string) []checkpointStepView {
	return []checkpointStepView{
		{
			Label:  "Candidate downloaded",
			Status: "remote",
			Note:   remoteChecklistNote("This checkpoint is recorded on orch-coordinator when the candidate tarball is carried out for review.", "--coordinator-url", coordinatorURL),
			Href:   remoteEvidenceHref(coordinatorURL, "/roster#cycle-timeline"),
		},
		{
			Label:  "Candidate loaded on secure-orch",
			Status: checkpointStatus(hasSecureOrchEvent(events, string(audit.KindLoadCandidate))),
			Note:   "The uploaded candidate has been accepted locally and tied to the current review session.",
			Href:   eventHref(events, string(audit.KindLoadCandidate)),
		},
		{
			Label:  "Diff reviewed on secure-orch",
			Status: checkpointStatus(hasSecureOrchEvent(events, string(audit.KindViewDiff))),
			Note:   "The candidate diff has been opened on the cold-key side for operator review.",
			Href:   eventHref(events, string(audit.KindViewDiff)),
		},
		{
			Label:  "Manifest signed on secure-orch",
			Status: checkpointStatus(hasSecureOrchEvent(events, string(audit.KindSign), string(audit.KindWriteSigned))),
			Note:   "The explicit tap-to-sign flow has completed and a signed manifest is ready to hand back.",
			Href:   eventHref(events, string(audit.KindSign), string(audit.KindWriteSigned)),
		},
		{
			Label:  "Signed manifest returned",
			Status: "remote",
			Note:   remoteChecklistNote("This checkpoint is recorded on orch-coordinator after the signed manifest is uploaded back and verified.", "--coordinator-url", coordinatorURL),
			Href:   remoteEvidenceHref(coordinatorURL, "/roster#cycle-timeline"),
		},
		{
			Label:  "Manifest published",
			Status: "remote",
			Note:   remoteChecklistNote("Final publication acceptance is recorded on orch-coordinator after atomic swap succeeds.", "--coordinator-url", coordinatorURL),
			Href:   remoteEvidenceHref(coordinatorURL, "/roster#cycle-timeline"),
		},
	}
}

func hasSecureOrchEvent(events []cycleEventView, kinds ...string) bool {
	for _, event := range events {
		for _, kind := range kinds {
			if event.Kind == kind {
				return true
			}
		}
	}
	return false
}

func eventHref(events []cycleEventView, kinds ...string) string {
	for _, event := range events {
		for _, kind := range kinds {
			if event.Kind == kind && event.Anchor != "" {
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

func checkpointStatus(done bool) string {
	if done {
		return "done"
	}
	return "pending"
}

func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	view := auditPageView{
		pageView:   s.basePageView(r, "Audit", "audit", "audit-content"),
		AuditPath:  s.audit.Path(),
		NewestPath: "/audit",
	}
	page, err := audit.ReadPage(s.audit.Path(), auditPageSize, strings.TrimSpace(r.URL.Query().Get("before")))
	if err != nil {
		view.AuditError = err.Error()
	} else {
		view.AuditEvents = buildAuditEventViews(page.Events)
		view.HasOlder = page.HasOlder
		view.NextCursor = page.NextCursor
		view.IsPaginated = view.HasOlder || strings.TrimSpace(r.URL.Query().Get("before")) != ""
		view.CurrentCount = len(view.AuditEvents)
		if page.NextCursor != "" {
			view.OlderPath = "/audit?before=" + url.QueryEscape(page.NextCursor)
		}
	}
	if err := s.templates.render(w, "page.html", view); err != nil {
		s.logger.Warn("render audit", "err", err)
	}
}

func (s *Server) basePageView(r *http.Request, title, activePage, contentTemplate string) pageView {
	return pageView{
		Title:           title,
		ActivePage:      activePage,
		ContentTemplate: contentTemplate,
		Actor:           actorFromRequest(r),
		SignerAddress:   s.signer.Address().String(),
		AssetVersion:    s.cfg.Version,
	}
}

func (s *Server) protocolStatusView(r *http.Request) *protocolStatusView {
	if s.protocol == nil {
		return nil
	}
	snapshot := s.protocol.Snapshot(r.Context())
	return buildProtocolStatusView(snapshot, strings.ToLower(s.signer.Address().String()))
}

func buildProtocolStatusView(snapshot protocol.Snapshot, confirmAddress string) *protocolStatusView {
	return &protocolStatusView{
		Health: protocolFieldView{
			Title:     "Protocol daemon",
			Available: snapshot.Health.Available,
			Error:     snapshot.Health.Error,
			Rows: [][2]string{
				{"ok", fmt.Sprintf("%v", snapshot.Health.Value.OK)},
				{"mode", snapshot.Health.Value.Mode},
				{"version", snapshot.Health.Value.Version},
				{"chain_id", fmt.Sprintf("%d", snapshot.Health.Value.ChainID)},
			},
		},
		Round: protocolFieldView{
			Title:         "Round status",
			Available:     snapshot.Round.Available,
			Unimplemented: snapshot.Round.Unimplemented,
			Error:         snapshot.Round.Error,
			Rows: [][2]string{
				{"last_round", fmt.Sprintf("%d", snapshot.Round.Value.LastRound)},
				{"last_error", snapshot.Round.Value.LastError},
				{"current_round_initialized", fmt.Sprintf("%v", snapshot.Round.Value.CurrentRoundInitialized)},
				{"current_round_locked", fmt.Sprintf("%v", snapshot.Round.Value.CurrentRoundLocked)},
				{"last_intent_id", snapshot.Round.Value.LastIntentID},
			},
		},
		Reward: protocolFieldView{
			Title:         "Reward status",
			Available:     snapshot.Reward.Available,
			Unimplemented: snapshot.Reward.Unimplemented,
			Error:         snapshot.Reward.Error,
			Rows: [][2]string{
				{"last_round", fmt.Sprintf("%d", snapshot.Reward.Value.LastRound)},
				{"orch_address", snapshot.Reward.Value.OrchAddress},
				{"eligible", fmt.Sprintf("%v", snapshot.Reward.Value.Eligible)},
				{"eligibility_reason", snapshot.Reward.Value.EligibilityReason},
				{"last_reward_round", fmt.Sprintf("%d", snapshot.Reward.Value.LastRewardRound)},
				{"active", fmt.Sprintf("%v", snapshot.Reward.Value.Active)},
				{"last_earned_wei", snapshot.Reward.Value.LastEarnedWei},
				{"last_error", snapshot.Reward.Value.LastError},
			},
		},
		ServiceRegistry: protocolFieldView{
			Title:     "Service Registry",
			Available: snapshot.ServiceRegistry.Available,
			Error:     snapshot.ServiceRegistry.Error,
			Rows: [][2]string{
				{"registered", fmt.Sprintf("%v", snapshot.ServiceRegistry.Value.Registered)},
				{"url", snapshot.ServiceRegistry.Value.URL},
			},
		},
		AIServiceRegistry: protocolFieldView{
			Title:     "AI Service Registry",
			Available: snapshot.AIServiceRegistry.Available,
			Error:     snapshot.AIServiceRegistry.Error,
			Rows: [][2]string{
				{"registered", fmt.Sprintf("%v", snapshot.AIServiceRegistry.Value.Registered)},
				{"url", snapshot.AIServiceRegistry.Value.URL},
			},
		},
		Wallet: protocolFieldView{
			Title:     "Wallet",
			Available: snapshot.Wallet.Available,
			Error:     snapshot.Wallet.Error,
			Rows: [][2]string{
				{"address", snapshot.Wallet.Value.Address},
				{"balance_wei", snapshot.Wallet.Value.BalanceWei},
			},
		},
		ConfirmAddress: confirmAddress,
	}
}

func protocolActionFeedbackFromRequest(r *http.Request) *protocolActionFeedbackView {
	q := r.URL.Query()
	action := strings.TrimSpace(q.Get("protocol_action"))
	result := strings.TrimSpace(q.Get("protocol_result"))
	message := strings.TrimSpace(q.Get("protocol_message"))
	if action == "" && result == "" && message == "" {
		return nil
	}
	return &protocolActionFeedbackView{
		Action:  action,
		Result:  result,
		Message: message,
	}
}

func buildTxIntentLookupView(ctx context.Context, client *protocol.Client, query string) *txIntentLookupView {
	trimmed := strings.TrimSpace(query)
	if client == nil && trimmed == "" {
		return nil
	}
	view := &txIntentLookupView{Query: trimmed}
	if client == nil || trimmed == "" {
		return view
	}
	intent, err := client.GetTxIntent(ctx, trimmed)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Result = &txIntentResultView{
		Rows: [][2]string{
			{"id", intent.ID},
			{"kind", intent.Kind},
			{"status", intent.Status},
			{"attempt_count", fmt.Sprintf("%d", intent.AttemptCount)},
			{"created_at", intent.CreatedAt},
			{"last_updated_at", intent.LastUpdatedAt},
			{"confirmed_at", intent.ConfirmedAt},
			{"failed_class", intent.FailedClass},
			{"failed_code", intent.FailedCode},
			{"failed_message", intent.FailedMessage},
		},
	}
	return view
}

func buildAuditEventViews(events []audit.Event) []auditEventView {
	views := make([]auditEventView, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		fields := ""
		if len(event.Fields) > 0 {
			b, err := json.MarshalIndent(event.Fields, "", "  ")
			if err != nil {
				fields = err.Error()
			} else {
				fields = string(b)
			}
		}
		seq := ""
		if event.Seq != nil {
			seq = fmt.Sprintf("%d", *event.Seq)
		}
		views = append(views, auditEventView{
			At:             event.At.UTC().Format(time.RFC3339),
			Kind:           string(event.Kind),
			Actor:          event.Actor,
			EthAddress:     event.EthAddress,
			PublicationSeq: seq,
			CanonHash:      event.CanonHash,
			Note:           event.Note,
			Fields:         fields,
		})
	}
	return views
}

func (s *Server) handleCandidate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	if err := r.ParseMultipartForm(s.maxUpload); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse multipart", err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("candidate")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "candidate file", err)
		return
	}
	defer file.Close()
	manifestBytes, err := extractManifestJSON(file, header.Filename)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "extract manifest.json", err)
		return
	}
	if !json.Valid(manifestBytes) {
		s.fail(w, r, http.StatusBadRequest, "manifest.json", errors.New("not valid JSON"))
		return
	}
	canon, err := canonicalManifestBytes(manifestBytes)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "canonicalize manifest", err)
		return
	}
	stash := &stashedCandidate{
		bytes:      manifestBytes,
		loadedAt:   time.Now().UTC(),
		canonHash:  canonical.SHA256Hex(canon),
		sourceName: filepath.Base(header.Filename),
	}
	s.mu.Lock()
	s.candidate = stash
	s.mu.Unlock()
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindLoadCandidate,
		Actor:      actorFromRequest(r),
		EthAddress: s.signer.Address().String(),
		CanonHash:  stash.canonHash,
		Note:       stash.sourceName,
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	http.Redirect(w, r, "/manifests", http.StatusSeeOther)
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	had := s.candidate != nil
	s.candidate = nil
	s.mu.Unlock()
	if had {
		if appendErr := s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			Actor:      actorFromRequest(r),
			EthAddress: s.signer.Address().String(),
			Note:       "operator discarded candidate",
		}); appendErr != nil {
			s.logger.Warn("audit append failed", "err", appendErr)
		}
	}
	http.Redirect(w, r, "/manifests", http.StatusSeeOther)
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("confirm_last4")))
	expected := lastFourHex(s.signer.Address().String())
	if confirm == "" || confirm != expected {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			Actor:      actorFromRequest(r),
			EthAddress: s.signer.Address().String(),
			Note:       "confirm gesture failed",
		})
		s.fail(w, r, http.StatusBadRequest, "confirm gesture", fmt.Errorf("last 4 hex chars do not match signer address"))
		return
	}
	s.mu.Lock()
	cand := s.candidate
	s.mu.Unlock()
	if cand == nil {
		s.fail(w, r, http.StatusBadRequest, "sign", errors.New("no candidate loaded"))
		return
	}
	envelope, err := signCandidate(cand.bytes, s.signer)
	if err != nil {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			Actor:      actorFromRequest(r),
			EthAddress: s.signer.Address().String(),
			Note:       "sign failed: " + err.Error(),
		})
		s.fail(w, r, http.StatusInternalServerError, "sign", err)
		return
	}
	canon, err := canonicalManifestBytes(cand.bytes)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "canonicalize", err)
		return
	}
	canonHash := canonical.SHA256Hex(canon)
	seq := publicationSeq(cand.bytes)

	if err := writeLastSignedAtomic(s.cfg.LastSignedPath, envelope); err != nil {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			Actor:      actorFromRequest(r),
			EthAddress: s.signer.Address().String(),
			Note:       "write last-signed failed: " + err.Error(),
		})
		s.fail(w, r, http.StatusInternalServerError, "write last-signed", err)
		return
	}

	signEvent := audit.Event{
		Kind:       audit.KindSign,
		Actor:      actorFromRequest(r),
		EthAddress: s.signer.Address().String(),
		CanonHash:  canonHash,
	}
	if seq != nil {
		signEvent.Seq = seq
	}
	if appendErr := s.audit.Append(signEvent); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindWriteSigned,
		Actor:      actorFromRequest(r),
		EthAddress: s.signer.Address().String(),
		CanonHash:  canonHash,
		Note:       cand.sourceName,
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}

	s.mu.Lock()
	s.candidate = nil
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="signed.json"`)
	w.Header().Set("X-Canonical-SHA256", canonHash)
	if _, err := w.Write(envelope); err != nil {
		s.logger.Warn("write signed response", "err", err)
	}
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := s.templates.render(w, "login.html", loginView{AuthEnabled: true, AssetVersion: s.cfg.Version}); err != nil {
		s.logger.Warn("render login", "err", err)
	}
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	token := r.PostForm.Get("admin_token")
	actor := r.PostForm.Get("actor")
	sessionID, err := s.auth.login(token, actor)
	if err != nil {
		if renderErr := s.templates.render(w, "login.html", loginView{
			AuthEnabled:  true,
			Error:        err.Error(),
			AssetVersion: s.cfg.Version,
		}); renderErr != nil {
			s.logger.Warn("render login", "err", renderErr)
		}
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
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			s.auth.logout(cookie.Value)
		}
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleProtocolForceInit(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolForceAction(w, r, "force-init", "chain.round.init", func(ctx context.Context) (protocol.ForceActionOutcome, error) {
		return s.protocol.ForceInitializeRound(ctx)
	})
}

func (s *Server) handleProtocolForceReward(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolForceAction(w, r, "force-reward", "chain.reward.claim", func(ctx context.Context) (protocol.ForceActionOutcome, error) {
		return s.protocol.ForceRewardCall(ctx)
	})
}

func (s *Server) handleProtocolForceAction(w http.ResponseWriter, r *http.Request, routeAction, auditAction string, invoke func(context.Context) (protocol.ForceActionOutcome, error)) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("typed_confirmation")))
	expected := strings.ToLower(s.signer.Address().String())
	actor := actorFromRequest(r)
	if confirm != expected {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{
			"expected_eth_address": expected,
			"typed_confirmation":   confirm,
		}, "")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "typed confirmation must match "+expected)
		return
	}
	if s.protocol == nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, "protocol-daemon socket is not configured")
		s.redirectProtocolFeedback(w, r, routeAction, "error", "protocol-daemon socket is not configured")
		return
	}
	outcome, err := invoke(r.Context())
	if err != nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, err.Error())
		s.redirectProtocolFeedback(w, r, routeAction, "error", err.Error())
		return
	}
	if outcome.Submitted {
		s.appendProtocolAudit(actor, auditAction, "success", map[string]any{
			"intent_id": outcome.IntentID,
		}, "")
		s.redirectProtocolFeedback(w, r, routeAction, "success", "submitted intent "+outcome.IntentID)
		return
	}
	s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{
		"skipped": true,
		"reason":  outcome.SkipReason,
		"code":    outcome.SkipCode,
	}, "")
	s.redirectProtocolFeedback(w, r, routeAction, "rejected", outcome.SkipCode+": "+outcome.SkipReason)
}

func (s *Server) handleProtocolSetServiceURI(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolSetURI(w, r, "set-service-uri", "chain.serviceuri.set", func(ctx context.Context, url string) (string, error) {
		return s.protocol.SetServiceURI(ctx, url)
	})
}

func (s *Server) handleProtocolSetAIServiceURI(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolSetURI(w, r, "set-ai-service-uri", "chain.ai.serviceuri.set", func(ctx context.Context, url string) (string, error) {
		return s.protocol.SetAIServiceURI(ctx, url)
	})
}

func (s *Server) handleProtocolSetURI(w http.ResponseWriter, r *http.Request, routeAction, auditAction string, invoke func(context.Context, string) (string, error)) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	rawURL := strings.TrimSpace(r.PostForm.Get("url"))
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("typed_confirmation")))
	expected := strings.ToLower(s.signer.Address().String())
	actor := actorFromRequest(r)
	if rawURL == "" {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{"url": rawURL}, "url is required")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "url is required")
		return
	}
	if confirm != expected {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{
			"url":                  rawURL,
			"expected_eth_address": expected,
			"typed_confirmation":   confirm,
		}, "")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "typed confirmation must match "+expected)
		return
	}
	if s.protocol == nil {
		s.appendProtocolAudit(actor, auditAction, "error", map[string]any{"url": rawURL}, "protocol-daemon socket is not configured")
		s.redirectProtocolFeedback(w, r, routeAction, "error", "protocol-daemon socket is not configured")
		return
	}
	intentID, err := invoke(r.Context(), rawURL)
	if err != nil {
		s.appendProtocolAudit(actor, auditAction, "error", map[string]any{"url": rawURL}, err.Error())
		s.redirectProtocolFeedback(w, r, routeAction, "error", err.Error())
		return
	}
	s.appendProtocolAudit(actor, auditAction, "success", map[string]any{
		"url":       rawURL,
		"intent_id": intentID,
	}, "")
	s.redirectProtocolFeedback(w, r, routeAction, "success", "submitted intent "+intentID)
}

func (s *Server) appendProtocolAudit(actor, action, result string, fields map[string]any, note string) {
	if err := s.audit.Append(audit.Event{
		Kind:       audit.KindProtocolAction,
		Actor:      actor,
		EthAddress: s.signer.Address().String(),
		Note:       note,
		Fields: map[string]any{
			"action": action,
			"result": result,
			"data":   fields,
		},
	}); err != nil {
		s.logger.Warn("audit append failed", "err", err)
	}
}

func (s *Server) redirectProtocolFeedback(w http.ResponseWriter, r *http.Request, action, result, message string) {
	q := make(url.Values)
	q.Set("protocol_action", action)
	q.Set("protocol_result", result)
	q.Set("protocol_message", message)
	http.Redirect(w, r, "/protocol-actions?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, what string, err error) {
	s.logger.Warn("handler failed", "what", what, "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, fmt.Sprintf("%s: %s", what, err), code)
}

// extractManifestJSON pulls manifest.json out of a tarball or returns
// the body unchanged if it already looks like raw JSON. The console
// accepts both shapes during operator transition; the runbook
// recommends the tarball form (manifest.json + metadata.json) per
// plan 0018 Q3.
func extractManifestJSON(r io.Reader, filename string) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil {
		return nil, err
	}
	if looksLikeJSON(buf) {
		return buf, nil
	}
	return readManifestFromTar(buf, filename)
}

func looksLikeJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func readManifestFromTar(buf []byte, filename string) ([]byte, error) {
	var reader io.Reader = bytes.NewReader(buf)
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, err := gzip.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("manifest.json not found in archive")
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) == "manifest.json" {
			return io.ReadAll(io.LimitReader(tr, 16<<20))
		}
	}
}
