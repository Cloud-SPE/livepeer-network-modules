package compat

import (
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Result struct {
	Compatible bool     `json:"compatible"`
	Reasons    []string `json:"reasons,omitempty"`
}

func Check(offer types.Offer, backend types.MemberBackend) Result {
	reasons := make([]string, 0)
	if strings.TrimSpace(backend.Transport) == "" {
		reasons = append(reasons, "backend transport is required")
	}
	if strings.TrimSpace(backend.URL) == "" {
		reasons = append(reasons, "backend url is required")
	}
	if len(backend.ClaimedCapabilities) == 0 {
		reasons = append(reasons, "backend has no claimed capabilities")
		return Result{Compatible: false, Reasons: reasons}
	}
	if !matchesAnyClaim(offer, backend) {
		reasons = append(reasons, fmt.Sprintf("backend claims do not match offer %s/%s", offer.CapabilityID, offer.OfferingID))
	}
	return Result{Compatible: len(reasons) == 0, Reasons: reasons}
}

func matchesAnyClaim(offer types.Offer, backend types.MemberBackend) bool {
	for _, claim := range backend.ClaimedCapabilities {
		if claim.CapabilityID != offer.CapabilityID {
			continue
		}
		if claim.InteractionMode != "" && claim.InteractionMode != offer.InteractionMode {
			continue
		}
		if claim.OfferingID != "" && claim.OfferingID != offer.OfferingID {
			continue
		}
		return true
	}
	return false
}
