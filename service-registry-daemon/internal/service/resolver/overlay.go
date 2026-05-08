package resolver

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// applyOverlay merges manifest-derived nodes with the static-overlay
// entry for an address. See docs/design-docs/static-overlay.md for the
// precedence rules.
//
//   - Overlay wins on policy fields: enabled, tier_allowed, weight,
//     unsigned_allowed.
//   - Manifest wins on advertised fields: url, capabilities, lat/lon,
//     region.
//   - Pin nodes from the overlay are appended (operator-managed
//     off-chain nodes), with Source=SourceStaticOverlay.
//
// If overlay is nil or has no entry for addr, defaults are applied.
func applyOverlay(addr types.EthAddress, nodes []types.ResolvedNode, overlay *config.Overlay) []types.ResolvedNode {
	entry, ok := overlay.FindByAddress(addr)
	if !ok {
		// No overlay entry: apply defaults (already applied by callers,
		// but be defensive).
		for i := range nodes {
			if nodes[i].Weight == 0 {
				nodes[i].Weight = 100
			}
		}
		return nodes
	}

	// Stamp policy fields from the overlay onto every manifest-derived
	// node. We don't override URL or capabilities — those come from the
	// manifest.
	for i := range nodes {
		nodes[i].Enabled = entry.Enabled
		nodes[i].Weight = entry.Weight
		// Copy tier_allowed defensively so callers can't mutate the overlay.
		if entry.TierAllowed != nil {
			nodes[i].TierAllowed = append([]string(nil), entry.TierAllowed...)
		}
	}

	// Append pin nodes.
	for _, p := range entry.Pin {
		nodes = append(nodes, types.ResolvedNode{
			ID:              p.ID,
			URL:             p.URL,
			Capabilities:    p.Capabilities,
			Source:          types.SourceStaticOverlay,
			SignatureStatus: types.SigUnsigned, // pin nodes are operator-asserted, off-manifest
			OperatorAddr:    addr,
			Enabled:         entry.Enabled,
			TierAllowed:     mergeTier(entry.TierAllowed, p.TierAllowed),
			Weight:          chooseWeight(entry.Weight, p.Weight),
		})
	}

	return nodes
}

// mergeTier returns p (pin-specific) if non-nil, else parent.
func mergeTier(parent, p []string) []string {
	if p != nil {
		return append([]string(nil), p...)
	}
	if parent != nil {
		return append([]string(nil), parent...)
	}
	return nil
}

// chooseWeight returns the pin-specific weight if set (non-zero), else
// the parent overlay weight.
func chooseWeight(parent, p int) int {
	if p > 0 {
		return p
	}
	return parent
}

// signaturePolicyAllows reports whether the resolver should *return*
// nodes whose signature_status is below the operator's policy bar.
//
// Rules:
//   - Verified-signed: always allowed.
//   - Legacy: always allowed (a legacy URL is not a security claim,
//     just an endpoint to dial).
//   - Unsigned (CSV / static-pin): allowed only if operator overlay
//     marks UnsignedAllowed=true OR caller passes allowUnsignedRequest.
//   - Invalid: never allowed (caller decides; we never return invalid).
func signaturePolicyAllows(addr types.EthAddress, overlay *config.Overlay, allowUnsignedRequest bool, status types.SignatureStatus) bool {
	switch status {
	case types.SigVerified, types.SigLegacy:
		return true
	case types.SigUnsigned:
		if allowUnsignedRequest {
			return true
		}
		if entry, ok := overlay.FindByAddress(addr); ok && entry.UnsignedAllowed {
			return true
		}
		return false
	case types.SigInvalid:
		return false
	default:
		return false
	}
}
