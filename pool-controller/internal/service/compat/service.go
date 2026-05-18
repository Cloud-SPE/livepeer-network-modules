package compat

import (
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Result struct {
	Compatible   bool                `json:"compatible"`
	Reasons      []string            `json:"reasons,omitempty"`
	Checks       []CheckResult       `json:"checks,omitempty"`
	MatchedClaim *types.ClaimedOffer `json:"matched_claim,omitempty"`
}

type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

func Check(offer types.Offer, backend types.MemberBackend) Result {
	reasons := make([]string, 0)
	checks := make([]CheckResult, 0, 4)
	if strings.TrimSpace(backend.Transport) == "" {
		reasons = append(reasons, "backend transport is required")
		checks = append(checks, CheckResult{Name: "transport_present", Passed: false, Detail: "backend transport is empty"})
	} else {
		checks = append(checks, CheckResult{Name: "transport_present", Passed: true, Detail: backend.Transport})
	}
	if strings.TrimSpace(backend.URL) == "" {
		reasons = append(reasons, "backend url is required")
		checks = append(checks, CheckResult{Name: "url_present", Passed: false, Detail: "backend url is empty"})
	} else {
		checks = append(checks, CheckResult{Name: "url_present", Passed: true, Detail: backend.URL})
	}
	if len(backend.ClaimedCapabilities) == 0 {
		reasons = append(reasons, "backend has no claimed capabilities")
		checks = append(checks, CheckResult{Name: "claimed_capabilities_present", Passed: false, Detail: "backend has no claimed capabilities"})
		return Result{Compatible: false, Reasons: reasons, Checks: checks}
	}
	checks = append(checks, CheckResult{Name: "claimed_capabilities_present", Passed: true, Detail: fmt.Sprintf("%d claims", len(backend.ClaimedCapabilities))})
	matchedClaim, matched := matchesAnyClaim(offer, backend)
	if !matched {
		reasons = append(reasons, fmt.Sprintf("backend claims do not match offer %s/%s", offer.CapabilityID, offer.OfferingID))
		checks = append(checks, CheckResult{Name: "claim_matches_offer", Passed: false, Detail: fmt.Sprintf("no claim matched %s/%s %s", offer.CapabilityID, offer.OfferingID, offer.InteractionMode)})
		return Result{Compatible: false, Reasons: reasons, Checks: checks}
	}
	checks = append(checks, CheckResult{Name: "claim_matches_offer", Passed: true, Detail: fmt.Sprintf("%s/%s %s", matchedClaim.CapabilityID, matchedClaim.OfferingID, matchedClaim.InteractionMode)})
	return Result{Compatible: len(reasons) == 0, Reasons: reasons, Checks: checks, MatchedClaim: matchedClaim}
}

func matchesAnyClaim(offer types.Offer, backend types.MemberBackend) (*types.ClaimedOffer, bool) {
	for i := range backend.ClaimedCapabilities {
		claim := &backend.ClaimedCapabilities[i]
		if claim.CapabilityID != offer.CapabilityID {
			continue
		}
		if claim.InteractionMode != "" && claim.InteractionMode != offer.InteractionMode {
			continue
		}
		if claim.OfferingID != "" && claim.OfferingID != offer.OfferingID {
			continue
		}
		return claim, true
	}
	return nil, false
}
