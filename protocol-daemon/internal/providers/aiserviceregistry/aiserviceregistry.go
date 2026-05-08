// Package aiserviceregistry binds the separate AI service registry
// contract used by protocol-daemon for AI-specific serviceURI writes and
// reads. The ABI surface matches ServiceRegistry today.
package aiserviceregistry

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	srprovider "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/serviceregistry"
)

// Bindings is the AI registry-facing write/read surface. The AI registry
// currently exposes the same ABI as the standard ServiceRegistry.
type Bindings = srprovider.Bindings

// New validates the configured AI service registry address and returns
// bindings for the shared setServiceURI/getServiceURI ABI surface.
func New(addr chain.Address, rpcs ...rpc.RPC) (*Bindings, error) {
	return srprovider.New(addr, rpcs...)
}
