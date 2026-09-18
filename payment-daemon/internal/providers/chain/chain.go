// Package chain holds the chain-level constants and the chain-id
// preflight every chain-backed provider (broker, clock, gasprice)
// shares. The RPC transport, Controller address resolution and gas
// pricing come from chain-commons (plan 0048); this package is what is
// left once those are shared.
package chain

import (
	"context"
	"errors"
	"fmt"

	ethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
)

// ArbitrumOneChainID is the canonical chain ID for Arbitrum One — the
// only chain plan 0016 deploys against.
const ArbitrumOneChainID int64 = 42161

// ArbitrumOneController is the deployed Livepeer Controller address on
// Arbitrum One. Source: payment-daemon/docs/operator-runbook.md §10.
var ArbitrumOneController = ethcommon.HexToAddress("0xD8E8328501E9645d16Cf49539efC04f734606ee4")

// CheckChainID confirms the connected RPC reports the expected chain
// ID. Setting expected = 0 disables the check (escape hatch for forks /
// local Anvil; production must keep the default per the runbook).
func CheckChainID(ctx context.Context, client rpc.RPC, expected int64) error {
	if client == nil {
		return errors.New("chain: nil rpc client")
	}
	got, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("eth_chainId: %w", err)
	}
	if expected == 0 {
		return nil
	}
	if expected < 0 || uint64(got) != uint64(expected) {
		return fmt.Errorf("chain id mismatch: rpc=%d, expected=%d", uint64(got), expected)
	}
	return nil
}
