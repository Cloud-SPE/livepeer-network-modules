// Package chain wires the JSON-RPC client used by every chain-backed
// provider (broker, clock, gasprice) and resolves Livepeer's
// Controller-pattern contract addresses.
//
// Per plan 0016 §11.Q1 we build minimal in-tree machinery instead of
// pulling in the deprecated chain-commons module — the v0.2 provider
// interfaces are right-sized and a ~100-line controller resolver is
// cleaner than a sibling extracted module.
package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ArbitrumOneChainID is the canonical chain ID for Arbitrum One — the
// only chain plan 0016 deploys against.
const ArbitrumOneChainID int64 = 42161

// ArbitrumOneController is the deployed Livepeer Controller address on
// Arbitrum One. Source: payment-daemon/docs/operator-runbook.md §10.
var ArbitrumOneController = ethcommon.HexToAddress("0xD8E8328501E9645d16Cf49539efC04f734606ee4")

// Addresses holds the resolved addresses of every Livepeer contract
// the daemon talks to. Populated by Resolver.Resolve.
type Addresses struct {
	TicketBroker   ethcommon.Address
	RoundsManager  ethcommon.Address
	BondingManager ethcommon.Address
}

// Overrides lets operators bypass on-chain Controller resolution for any
// individual contract. Empty / zero address means "resolve via
// Controller".
type Overrides struct {
	TicketBroker   ethcommon.Address
	RoundsManager  ethcommon.Address
	BondingManager ethcommon.Address
}

// CheckChainID confirms the connected RPC reports the expected chain
// ID. Setting expected = 0 disables the check (escape hatch for forks /
// local Anvil; production must keep the default per the runbook).
func CheckChainID(ctx context.Context, client *ethclient.Client, expected int64) error {
	if client == nil {
		return errors.New("chain: nil ethclient")
	}
	got, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("eth_chainId: %w", err)
	}
	if expected == 0 {
		return nil
	}
	if got.Cmp(big.NewInt(expected)) != 0 {
		return fmt.Errorf("chain id mismatch: rpc=%s, expected=%d", got.String(), expected)
	}
	return nil
}

// Resolver loads contract addresses from the Livepeer Controller. The
// Controller exposes `getContract(bytes32 nameHash) returns (address)`
// where nameHash is keccak256(contractName).
type Resolver struct {
	client     *ethclient.Client
	controller ethcommon.Address
}

// NewResolver builds a Resolver targeting the given Controller address.
func NewResolver(client *ethclient.Client, controller ethcommon.Address) *Resolver {
	return &Resolver{client: client, controller: controller}
}

// Resolve returns the resolved Addresses, applying any non-zero
// overrides without an RPC call. Returns an error iff a name without
// override fails to resolve.
func (r *Resolver) Resolve(ctx context.Context, ov Overrides) (Addresses, error) {
	out := Addresses{
		TicketBroker:   ov.TicketBroker,
		RoundsManager:  ov.RoundsManager,
		BondingManager: ov.BondingManager,
	}
	if (out.TicketBroker == ethcommon.Address{}) {
		addr, err := r.callGetContract(ctx, "TicketBroker")
		if err != nil {
			return Addresses{}, fmt.Errorf("resolve TicketBroker: %w", err)
		}
		out.TicketBroker = addr
	}
	if (out.RoundsManager == ethcommon.Address{}) {
		addr, err := r.callGetContract(ctx, "RoundsManager")
		if err != nil {
			return Addresses{}, fmt.Errorf("resolve RoundsManager: %w", err)
		}
		out.RoundsManager = addr
	}
	if (out.BondingManager == ethcommon.Address{}) {
		addr, err := r.callGetContract(ctx, "BondingManager")
		if err != nil {
			return Addresses{}, fmt.Errorf("resolve BondingManager: %w", err)
		}
		out.BondingManager = addr
	}
	if (out.TicketBroker == ethcommon.Address{}) {
		return Addresses{}, errors.New("resolved TicketBroker is zero address")
	}
	if (out.RoundsManager == ethcommon.Address{}) {
		return Addresses{}, errors.New("resolved RoundsManager is zero address")
	}
	if (out.BondingManager == ethcommon.Address{}) {
		return Addresses{}, errors.New("resolved BondingManager is zero address")
	}
	return out, nil
}

// getContractSelector is keccak256("getContract(bytes32)")[:4].
var getContractSelector = crypto.Keccak256([]byte("getContract(bytes32)"))[:4]

func (r *Resolver) callGetContract(ctx context.Context, name string) (ethcommon.Address, error) {
	nameHash := crypto.Keccak256([]byte(name))
	calldata := make([]byte, 0, 4+32)
	calldata = append(calldata, getContractSelector...)
	calldata = append(calldata, nameHash...)
	out, err := r.client.CallContract(ctx, ethereum.CallMsg{
		To:   &r.controller,
		Data: calldata,
	}, nil)
	if err != nil {
		return ethcommon.Address{}, err
	}
	if len(out) < 32 {
		return ethcommon.Address{}, fmt.Errorf("getContract(%s): %d bytes returned, want 32", name, len(out))
	}
	var addr ethcommon.Address
	copy(addr[:], out[12:32])
	return addr, nil
}
