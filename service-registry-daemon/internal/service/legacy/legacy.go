// Package legacy holds the resolver fallback that synthesizes a
// single ResolvedNode from a plain-URL serviceURI when no manifest is
// available. The path is isolated so it's auditable on its own.
package legacy

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// Synthesize constructs a single legacy ResolvedNode for an address
// whose serviceURI is just a URL.
//
// The synthesized node has:
//   - URL = serviceURI verbatim
//   - Capabilities = nil (unknown)
//   - SignatureStatus = SigLegacy
//   - Source = SourceLegacy
//   - Enabled = true (overlay-applied later)
//   - Weight = 100 (overlay-applied later)
//
// The "legacy" ID is fixed so consumers can recognize the synthesized
// fallback without parsing.
func Synthesize(addr types.EthAddress, serviceURI string) types.ResolvedNode {
	return types.ResolvedNode{
		ID:              "legacy",
		URL:             serviceURI,
		Source:          types.SourceLegacy,
		SignatureStatus: types.SigLegacy,
		OperatorAddr:    addr,
		Enabled:         true,
		Weight:          100,
	}
}
