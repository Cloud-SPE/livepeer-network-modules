package assignmentpolicy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/compat"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type PreviewView struct {
	Compatible         bool                 `json:"compatible"`
	Reasons            []string             `json:"reasons,omitempty"`
	Checks             []compat.CheckResult `json:"checks,omitempty"`
	MatchedClaim       *types.ClaimedOffer  `json:"matched_claim,omitempty"`
	OfferFound         bool                 `json:"offer_found"`
	BackendFound       bool                 `json:"backend_found"`
	MemberFound        bool                 `json:"member_found"`
	OfferStatus        string               `json:"offer_status,omitempty"`
	BackendStatus      string               `json:"backend_status,omitempty"`
	VerificationStatus string               `json:"verification_status,omitempty"`
	MemberStatus       string               `json:"member_status,omitempty"`
}

func Preview(stateRepo *repo.StateRepo, offerID, backendID string) (PreviewView, error) {
	view := PreviewView{}
	offer, err := stateRepo.GetOffer(strings.TrimSpace(offerID))
	if err == nil {
		view.OfferFound = true
		view.OfferStatus = string(offer.Status)
	}
	backend, err := stateRepo.GetMemberBackend(strings.TrimSpace(backendID))
	if err == nil {
		view.BackendFound = true
		view.BackendStatus = string(backend.Status)
		view.VerificationStatus = string(backend.VerificationStatus)
		member, memberErr := stateRepo.GetMember(backend.MemberID)
		if memberErr == nil {
			view.MemberFound = true
			view.MemberStatus = string(member.Status)
		} else {
			view.Reasons = append(view.Reasons, memberErr.Error())
		}
		if view.OfferFound {
			check := compat.Check(offer, backend)
			view.Compatible = check.Compatible
			view.Reasons = append(view.Reasons, check.Reasons...)
			view.Checks = append(view.Checks, check.Checks...)
			view.MatchedClaim = check.MatchedClaim
		}
	}
	if !view.OfferFound {
		view.Reasons = append(view.Reasons, "offer not found")
	}
	if !view.BackendFound {
		view.Reasons = append(view.Reasons, "backend not found")
	}
	if view.OfferFound && offer.Status != types.OfferStatusActive {
		view.Reasons = append(view.Reasons, "offer must be active")
		view.Compatible = false
	}
	if view.BackendFound && backend.Status != types.BackendStatusActive {
		view.Reasons = append(view.Reasons, "backend must be active")
		view.Compatible = false
	}
	if view.BackendFound && backend.VerificationStatus != types.VerificationPassing {
		view.Reasons = append(view.Reasons, "backend verification must be passing")
		view.Compatible = false
	}
	if view.MemberFound {
		member, _ := stateRepo.GetMember(backend.MemberID)
		if member.Status != types.MemberStatusActive {
			view.Reasons = append(view.Reasons, "member must be active")
			view.Compatible = false
		}
	}
	return view, nil
}

func CreateAssignment(stateRepo *repo.StateRepo, assignment types.Assignment) (PreviewView, error) {
	view, err := Preview(stateRepo, assignment.OfferID, assignment.MemberBackendID)
	if err != nil {
		return view, err
	}
	if !view.Compatible {
		return view, errors.New(strings.Join(view.Reasons, "; "))
	}
	existing, err := stateRepo.ListAssignmentsByBackend(assignment.MemberBackendID)
	if err != nil {
		return view, err
	}
	for _, item := range existing {
		if item.OfferID == assignment.OfferID && item.Status == types.AssignmentStatusActive {
			return view, fmt.Errorf("active assignment already exists for backend %s and offer %s", assignment.MemberBackendID, assignment.OfferID)
		}
	}
	if err := stateRepo.PutAssignment(assignment); err != nil {
		return view, err
	}
	return view, nil
}
