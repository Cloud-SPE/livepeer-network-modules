package admissionreview

import (
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"slices"
)

type JoinRequestBackendPreview struct {
	BackendID          string                    `json:"backend_id"`
	Transport          string                    `json:"transport,omitempty"`
	URL                string                    `json:"url,omitempty"`
	VerificationStatus types.VerificationStatus  `json:"verification_status,omitempty"`
	VerificationError  string                    `json:"verification_error,omitempty"`
	ClaimCount         int                       `json:"claim_count"`
	Approavable        bool                      `json:"approvable"`
	Servable           bool                      `json:"servable"`
	ServableClaimCount int                       `json:"servable_claim_count"`
	ClaimPreviews      []JoinRequestClaimPreview `json:"claim_previews,omitempty"`
	Reasons            []string                  `json:"reasons,omitempty"`
}

type JoinRequestClaimPreview struct {
	CapabilityID      string                       `json:"capability_id"`
	OfferingID        string                       `json:"offering_id,omitempty"`
	InteractionMode   string                       `json:"interaction_mode,omitempty"`
	MatchingOfferIDs  []string                     `json:"matching_offer_ids,omitempty"`
	ActiveOfferIDs    []string                     `json:"active_offer_ids,omitempty"`
	SuggestedOfferIDs []string                     `json:"suggested_offer_ids,omitempty"`
	Suggestions       []JoinRequestOfferSuggestion `json:"suggestions,omitempty"`
	Servable          bool                         `json:"servable"`
	Reasons           []string                     `json:"reasons,omitempty"`
}

type JoinRequestOfferSuggestion struct {
	OfferID string `json:"offer_id"`
	Score   int    `json:"score"`
	Reason  string `json:"reason,omitempty"`
}

type JoinRequestPreviewView struct {
	JoinRequestID   string                      `json:"join_request_id"`
	Status          types.JoinRequestStatus     `json:"status"`
	Approavable     bool                        `json:"approvable"`
	BackendPreviews []JoinRequestBackendPreview `json:"backend_previews"`
	Reasons         []string                    `json:"reasons,omitempty"`
}

type AssignmentCandidateView struct {
	BackendID          string                    `json:"backend_id"`
	MemberID           string                    `json:"member_id"`
	MemberEthAddress   string                    `json:"member_eth_address,omitempty"`
	MemberDisplayName  string                    `json:"member_display_name,omitempty"`
	BackendStatus      types.BackendStatus       `json:"backend_status"`
	VerificationStatus types.VerificationStatus  `json:"verification_status"`
	AssignmentCount    int                       `json:"assignment_count"`
	ActiveAssignments  int                       `json:"active_assignments"`
	SuggestedClaims    []JoinRequestClaimPreview `json:"suggested_claims,omitempty"`
}

func BuildJoinRequestPreview(item types.JoinRequest, offers []types.Offer) JoinRequestPreviewView {
	view := JoinRequestPreviewView{
		JoinRequestID:   item.ID,
		Status:          item.Status,
		Approavable:     true,
		BackendPreviews: make([]JoinRequestBackendPreview, 0, len(item.RequestedBackends)),
	}
	if item.Status != types.JoinRequestPending {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "join request must be pending for approval")
	}
	if strings.TrimSpace(item.MemberEthAddress) == "" {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "member_eth_address is required")
	}
	if len(item.RequestedBackends) == 0 {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "requested_backends must contain at least one backend")
	}
	for _, backend := range item.RequestedBackends {
		backendView := JoinRequestBackendPreview{
			BackendID:          backend.ID,
			Transport:          backend.Transport,
			URL:                backend.URL,
			VerificationStatus: backend.VerificationStatus,
			VerificationError:  backend.VerificationError,
			ClaimCount:         len(backend.ClaimedCapabilities),
			Approavable:        true,
			ClaimPreviews:      make([]JoinRequestClaimPreview, 0, len(backend.ClaimedCapabilities)),
		}
		if strings.TrimSpace(backend.ID) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend id is required")
		}
		if strings.TrimSpace(backend.Transport) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend transport is required")
		}
		if strings.TrimSpace(backend.URL) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend url is required")
		}
		if backend.VerificationStatus != types.VerificationPassing {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend verification must be passing")
		}
		if len(backend.ClaimedCapabilities) == 0 {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend must claim at least one capability")
		}
		for _, claim := range backend.ClaimedCapabilities {
			claimView := BuildJoinClaimPreview(claim, offers)
			if claimView.Servable {
				backendView.Servable = true
				backendView.ServableClaimCount++
			}
			backendView.ClaimPreviews = append(backendView.ClaimPreviews, claimView)
		}
		if !backendView.Approavable {
			view.Approavable = false
			if len(backendView.Reasons) > 0 {
				view.Reasons = append(view.Reasons, backend.ID+": "+strings.Join(backendView.Reasons, "; "))
			}
		}
		view.BackendPreviews = append(view.BackendPreviews, backendView)
	}
	if !view.Approavable && len(view.Reasons) == 0 {
		view.Reasons = append(view.Reasons, "one or more requested backends are not approvable")
	}
	return view
}

func BuildJoinClaimPreview(claim types.ClaimedOffer, offers []types.Offer) JoinRequestClaimPreview {
	view := JoinRequestClaimPreview{
		CapabilityID:    claim.CapabilityID,
		OfferingID:      claim.OfferingID,
		InteractionMode: claim.InteractionMode,
	}
	for _, offer := range offers {
		if offer.CapabilityID != claim.CapabilityID {
			continue
		}
		if claim.InteractionMode != "" && offer.InteractionMode != claim.InteractionMode {
			continue
		}
		if claim.OfferingID != "" && offer.OfferingID != claim.OfferingID {
			continue
		}
		view.MatchingOfferIDs = append(view.MatchingOfferIDs, offer.ID)
		if offer.Status == types.OfferStatusActive {
			view.ActiveOfferIDs = append(view.ActiveOfferIDs, offer.ID)
			score, reason := RankJoinClaimSuggestion(claim, offer)
			view.Suggestions = append(view.Suggestions, JoinRequestOfferSuggestion{
				OfferID: offer.ID,
				Score:   score,
				Reason:  reason,
			})
		}
	}
	slices.SortFunc(view.Suggestions, func(left, right JoinRequestOfferSuggestion) int {
		if left.Score != right.Score {
			return right.Score - left.Score
		}
		return strings.Compare(left.OfferID, right.OfferID)
	})
	for _, suggestion := range view.Suggestions {
		view.SuggestedOfferIDs = append(view.SuggestedOfferIDs, suggestion.OfferID)
	}
	view.Servable = len(view.ActiveOfferIDs) > 0
	if strings.TrimSpace(claim.CapabilityID) == "" {
		view.Reasons = append(view.Reasons, "claimed capability_id is required")
	}
	if len(view.MatchingOfferIDs) == 0 {
		view.Reasons = append(view.Reasons, "no orch offer matches this claim")
	} else if len(view.ActiveOfferIDs) == 0 {
		view.Reasons = append(view.Reasons, "matching orch offers exist but none are active")
	}
	return view
}

func RankJoinClaimSuggestion(claim types.ClaimedOffer, offer types.Offer) (int, string) {
	score := 0
	parts := make([]string, 0, 3)
	if strings.TrimSpace(claim.OfferingID) != "" {
		if claim.OfferingID == offer.OfferingID {
			score += 100
			parts = append(parts, "exact offering_id")
		}
	} else {
		score += 10
		parts = append(parts, "claim allows any offering_id")
	}
	if strings.TrimSpace(claim.InteractionMode) != "" {
		if claim.InteractionMode == offer.InteractionMode {
			score += 50
			parts = append(parts, "exact interaction_mode")
		}
	} else {
		score += 5
		parts = append(parts, "claim allows any interaction_mode")
	}
	score += 1
	parts = append(parts, "capability_id matched")
	return score, strings.Join(parts, "; ")
}

func ListAssignmentCandidates(stateRepo *repo.StateRepo) ([]AssignmentCandidateView, error) {
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return nil, err
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return nil, err
	}
	out := make([]AssignmentCandidateView, 0)
	for _, backend := range backends {
		member, err := stateRepo.GetMember(backend.MemberID)
		if err != nil {
			continue
		}
		assignments, err := stateRepo.ListAssignmentsByBackend(backend.ID)
		if err != nil {
			return nil, err
		}
		activeAssignments := 0
		for _, assignment := range assignments {
			if assignment.Status == types.AssignmentStatusActive {
				activeAssignments++
			}
		}
		if member.Status != types.MemberStatusActive || backend.Status != types.BackendStatusActive || backend.VerificationStatus != types.VerificationPassing || activeAssignments > 0 {
			continue
		}
		candidate := AssignmentCandidateView{
			BackendID:          backend.ID,
			MemberID:           backend.MemberID,
			MemberEthAddress:   member.EthAddress,
			MemberDisplayName:  member.DisplayName,
			BackendStatus:      backend.Status,
			VerificationStatus: backend.VerificationStatus,
			AssignmentCount:    len(assignments),
			ActiveAssignments:  activeAssignments,
			SuggestedClaims:    make([]JoinRequestClaimPreview, 0, len(backend.ClaimedCapabilities)),
		}
		for _, claim := range backend.ClaimedCapabilities {
			claimView := BuildJoinClaimPreview(claim, offers)
			if len(claimView.SuggestedOfferIDs) > 0 {
				candidate.SuggestedClaims = append(candidate.SuggestedClaims, claimView)
			}
		}
		out = append(out, candidate)
	}
	return out, nil
}
