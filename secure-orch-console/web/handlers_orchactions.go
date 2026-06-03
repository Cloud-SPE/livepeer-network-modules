package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/protocol"
)

// handleProtocolForceTransferBond triggers the round-locked transfer-bond
// handler. Reuses the shared force-action gesture (typed confirmation +
// audit + typed-skip rendering).
func (s *Server) handleProtocolForceTransferBond(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolForceAction(w, r, "force-transfer-bond", "chain.bond.transfer", func(ctx context.Context) (protocol.ForceActionOutcome, error) {
		return s.protocol.ForceTransferBond(ctx)
	})
}

// handleProtocolForceWithdrawFees triggers the round-locked withdraw-fees
// handler.
func (s *Server) handleProtocolForceWithdrawFees(w http.ResponseWriter, r *http.Request) {
	s.handleProtocolForceAction(w, r, "force-withdraw-fees", "chain.fees.withdraw", func(ctx context.Context) (protocol.ForceActionOutcome, error) {
		return s.protocol.ForceWithdrawFees(ctx)
	})
}

// handleProtocolSetTranscoder sets the orchestrator's reward cut and fee
// cut (percentages meaning "what the orchestrator keeps").
func (s *Server) handleProtocolSetTranscoder(w http.ResponseWriter, r *http.Request) {
	const routeAction, auditAction = "set-transcoder", "chain.transcoder.set"
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	rewardCut := strings.TrimSpace(r.PostForm.Get("reward_cut"))
	feeCut := strings.TrimSpace(r.PostForm.Get("fee_cut"))
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("typed_confirmation")))
	expected := strings.ToLower(s.signer.Address().String())
	actor := actorFromRequest(r)

	if rewardCut == "" || feeCut == "" {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{"reward_cut": rewardCut, "fee_cut": feeCut}, "reward_cut and fee_cut are required")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "reward_cut and fee_cut are required")
		return
	}
	if confirm != expected {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{"reward_cut": rewardCut, "fee_cut": feeCut}, "")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "typed confirmation must match "+expected)
		return
	}
	if s.protocol == nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, "protocol-daemon socket is not configured")
		s.redirectProtocolFeedback(w, r, routeAction, "error", "protocol-daemon socket is not configured")
		return
	}
	intentID, err := s.protocol.SetTranscoder(r.Context(), rewardCut, feeCut)
	if err != nil {
		s.appendProtocolAudit(actor, auditAction, "error", map[string]any{"reward_cut": rewardCut, "fee_cut": feeCut}, err.Error())
		s.redirectProtocolFeedback(w, r, routeAction, "error", err.Error())
		return
	}
	s.appendProtocolAudit(actor, auditAction, "success", map[string]any{
		"reward_cut": rewardCut, "fee_cut": feeCut, "intent_id": intentID,
	}, "")
	s.redirectProtocolFeedback(w, r, routeAction, "success", "submitted intent "+intentID)
}

// handleProtocolCastVote casts a treasury proposal vote.
func (s *Server) handleProtocolCastVote(w http.ResponseWriter, r *http.Request) {
	const routeAction, auditAction = "cast-vote", "chain.treasury.vote"
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	proposalID := strings.TrimSpace(r.PostForm.Get("proposal_id"))
	support, supportLabel, supportOK := parseSupport(r.PostForm.Get("support"))
	reason := strings.TrimSpace(r.PostForm.Get("reason"))
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("typed_confirmation")))
	expected := strings.ToLower(s.signer.Address().String())
	actor := actorFromRequest(r)

	if proposalID == "" || !supportOK {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{"proposal_id": proposalID}, "proposal_id and a valid support choice are required")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "proposal_id and a valid support choice are required")
		return
	}
	if confirm != expected {
		s.appendProtocolAudit(actor, auditAction, "rejected", map[string]any{"proposal_id": proposalID}, "")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "typed confirmation must match "+expected)
		return
	}
	if s.protocol == nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, "protocol-daemon socket is not configured")
		s.redirectProtocolFeedback(w, r, routeAction, "error", "protocol-daemon socket is not configured")
		return
	}
	intentID, err := s.protocol.CastVote(r.Context(), proposalID, support, reason)
	if err != nil {
		s.appendProtocolAudit(actor, auditAction, "error", map[string]any{"proposal_id": proposalID, "support": supportLabel}, err.Error())
		s.redirectProtocolFeedback(w, r, routeAction, "error", err.Error())
		return
	}
	s.appendProtocolAudit(actor, auditAction, "success", map[string]any{
		"proposal_id": proposalID, "support": supportLabel, "intent_id": intentID,
	}, "")
	s.redirectProtocolFeedback(w, r, routeAction, "success", "submitted vote "+supportLabel+" intent "+intentID)
}

// handleProtocolSetConfig persists the daemon's operational config.
func (s *Server) handleProtocolSetConfig(w http.ResponseWriter, r *http.Request) {
	const routeAction, auditAction = "set-config", "config.operational.set"
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("typed_confirmation")))
	expected := strings.ToLower(s.signer.Address().String())
	actor := actorFromRequest(r)
	if confirm != expected {
		s.appendProtocolAudit(actor, auditAction, "rejected", nil, "")
		s.redirectProtocolFeedback(w, r, routeAction, "rejected", "typed confirmation must match "+expected)
		return
	}
	if s.protocol == nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, "protocol-daemon socket is not configured")
		s.redirectProtocolFeedback(w, r, routeAction, "error", "protocol-daemon socket is not configured")
		return
	}
	cfg := protocol.OperationalConfig{
		RoundInitEnabled:      r.PostForm.Get("round_init_enabled") == "on",
		RewardEnabled:         r.PostForm.Get("reward_enabled") == "on",
		RewardBeforeTransfer:  r.PostForm.Get("reward_before_transfer") == "on",
		TransferBondEnabled:   r.PostForm.Get("transfer_bond_enabled") == "on",
		TransferBondReceiver:  strings.TrimSpace(r.PostForm.Get("transfer_bond_receiver")),
		TransferBondMinRetain: strings.TrimSpace(r.PostForm.Get("transfer_bond_min_retain")),
		WithdrawFeesEnabled:   r.PostForm.Get("withdraw_fees_enabled") == "on",
		WithdrawFeesReceiver:  strings.TrimSpace(r.PostForm.Get("withdraw_fees_receiver")),
		WithdrawFeesThreshold: strings.TrimSpace(r.PostForm.Get("withdraw_fees_threshold")),
	}
	stored, err := s.protocol.SetConfig(r.Context(), cfg)
	if err != nil {
		s.appendProtocolAudit(actor, auditAction, "error", nil, err.Error())
		s.redirectProtocolFeedback(w, r, routeAction, "error", err.Error())
		return
	}
	s.appendProtocolAudit(actor, auditAction, "success", map[string]any{
		"round_init_enabled":    stored.RoundInitEnabled,
		"reward_enabled":        stored.RewardEnabled,
		"transfer_bond_enabled": stored.TransferBondEnabled,
		"withdraw_fees_enabled": stored.WithdrawFeesEnabled,
	}, "")
	s.redirectProtocolFeedback(w, r, routeAction, "success", "operational config updated")
}

// parseSupport maps the form value to the proto support enum.
func parseSupport(v string) (code uint32, label string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "against":
		return 0, "Against", true
	case "1", "for":
		return 1, "For", true
	case "2", "abstain":
		return 2, "Abstain", true
	default:
		return 0, "", false
	}
}
