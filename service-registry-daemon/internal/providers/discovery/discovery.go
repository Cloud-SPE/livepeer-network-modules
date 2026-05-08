// Package discovery enumerates the on-chain set of active orchestrators
// for the resolver to seed its cache from. The pool composition is
// fixed for the duration of a round, so the resolver re-walks once per
// round event (subscribed via chain-commons.services.roundclock) — far
// fewer chain reads than a fixed TTL, exactly as fresh as the data
// actually is.
//
// Two implementations:
//
//   - Chain: walks BondingManager.GetFirstTranscoderInPool +
//     GetNextTranscoderInPool via chain-commons.providers.bondingmanager.
//     ~N+1 RPC calls per refresh (~101 for 100 orchs, once per ~19 hours
//     on Arbitrum One). Default for production deployments.
//
//   - Disabled: returns an empty slice. Used when --discovery=overlay-only
//     and in --dev mode. Operators who want strict allowlisting via
//     nodes.yaml pick this.
package discovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ccbm "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// Discovery enumerates the on-chain set of active orchestrators.
//
// Implementations must be safe for concurrent use; callers may invoke
// ActiveOrchs from multiple goroutines (the resolver's seeder calls it
// from a round-event watcher; ad-hoc admin tooling may call it directly).
type Discovery interface {
	// ActiveOrchs returns the list of orchestrator ETH addresses that
	// the resolver should seed its cache from. The result is in the
	// order returned by the on-chain pool walk; callers should not
	// assume any particular ordering.
	ActiveOrchs(ctx context.Context) ([]types.EthAddress, error)
}

// Chain walks the BondingManager active-pool linked list. Use with the
// chain-commons RPC client + the resolved BondingManager address.
type Chain struct {
	bm *ccbm.Bindings
}

// NewChain constructs a chain-walk Discovery backed by chain-commons'
// read-only BondingManager bindings.
func NewChain(r rpc.RPC, bondingManager chain.Address) (*Chain, error) {
	bm, err := ccbm.New(r, bondingManager)
	if err != nil {
		return nil, fmt.Errorf("discovery: bondingmanager: %w", err)
	}
	return &Chain{bm: bm}, nil
}

// ActiveOrchs walks BondingManager.getFirstTranscoderInPool +
// getNextTranscoderInPool until the zero-address terminator. Returns
// the addresses in pool order. Cost: N+1 RPC calls (one for first,
// N for the walk). Caller pairs each address with a
// ServiceRegistry.GetServiceURI lookup downstream.
func (c *Chain) ActiveOrchs(ctx context.Context) ([]types.EthAddress, error) {
	first, err := c.bm.GetFirstTranscoderInPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovery: getFirst: %w", err)
	}
	if first == (chain.Address{}) {
		// Empty pool — not an error.
		return nil, nil
	}

	out := make([]types.EthAddress, 0, 64)
	out = append(out, ethToTypes(first))

	cur := first
	// Bound the walk; the protocol's pool size is configured on-chain
	// (typically ≤100). 1024 is a generous upper bound to catch
	// pathological responses without spinning forever.
	const maxPoolSize = 1024
	for i := 0; i < maxPoolSize; i++ {
		next, err := c.bm.GetNextTranscoderInPool(ctx, cur)
		if err != nil {
			return nil, fmt.Errorf("discovery: getNext: %w", err)
		}
		if next == (chain.Address{}) {
			return out, nil
		}
		out = append(out, ethToTypes(next))
		cur = next
	}
	return nil, errors.New("discovery: pool walk exceeded 1024 entries; pool may be malformed")
}

// Disabled is the no-op Discovery used when --discovery=overlay-only or
// when --dev mode is on. ActiveOrchs returns an empty slice.
type Disabled struct{}

// NewDisabled returns the no-op Discovery.
func NewDisabled() Disabled { return Disabled{} }

// ActiveOrchs returns nil, nil — the empty pool case.
func (Disabled) ActiveOrchs(_ context.Context) ([]types.EthAddress, error) { return nil, nil }

// ethToTypes converts a chain-commons Address (go-ethereum common.Address)
// to the resolver's EthAddress (lower-cased 0x-prefixed string).
func ethToTypes(a chain.Address) types.EthAddress {
	// Address.Hex() returns the EIP-55 mixed-case form; the resolver's
	// EthAddress is the lower-cased canonical form. Use the type's
	// constructor to get the consistent transformation.
	parsed, err := types.ParseEthAddress(a.Hex())
	if err != nil {
		// Should be impossible for a valid 20-byte address; fall back
		// to raw lower-case bytes-to-hex.
		return types.EthAddress("0x" + lowerHex(a[:]))
	}
	return parsed
}

func lowerHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
