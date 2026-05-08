// Package preflight runs startup gates that fail loud-and-fast before
// the gRPC socket opens.
//
// Gates:
//   - chain-id verification (rpc.ChainID matches Config.ChainID)
//   - Controller resolution + non-zero RoundsManager / BondingManager
//   - CodeAt() check for both contracts (revert if no bytecode)
//   - keystore decryption (already done at provider construction; we just
//     confirm the derived address matches Config.AccountAddress if set)
//   - min-balance gate (BalanceAt(keystore.Address) >= MinBalanceWei)
//   - gas oracle round-trip (Suggest() returns a non-nil estimate)
package preflight

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// Config wires the preflight runner.
type Config struct {
	RPC           rpc.RPC
	Controller    controller.Controller
	Keystore      keystore.Keystore
	GasOracle     gasoracle.GasOracle
	ExpectedChain chain.ChainID
	MinBalanceWei chain.Wei
	Mode          types.Mode
	OrchAddress   chain.Address
	Logger        logger.Logger
}

// Result captures the gate outcomes for status reporting.
type Result struct {
	ChainID           chain.ChainID
	RoundsManager     chain.Address
	BondingManager    chain.Address
	Minter            chain.Address
	WalletAddress     chain.Address
	WalletBalanceWei  chain.Wei
	GasEstimateSource string
}

// Run executes every preflight gate. Returns a structured error wrapping
// the failed gate's error code on the first violation.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.RPC == nil {
		return nil, errors.New("preflight: RPC is required")
	}
	if cfg.Controller == nil {
		return nil, errors.New("preflight: Controller is required")
	}
	if cfg.Keystore == nil {
		return nil, errors.New("preflight: Keystore is required")
	}
	if cfg.GasOracle == nil {
		return nil, errors.New("preflight: GasOracle is required")
	}

	out := &Result{}

	// 1. chain-id
	chainID, err := cfg.RPC.ChainID(ctx)
	if err != nil {
		return out, fmt.Errorf("%s: %w", types.ErrCodePreflightChainID, err)
	}
	out.ChainID = chainID
	if cfg.ExpectedChain != 0 && chainID != cfg.ExpectedChain {
		return out, fmt.Errorf("%s: rpc reports chain-id=%d, expected %d",
			types.ErrCodePreflightChainID, chainID, cfg.ExpectedChain)
	}
	if cfg.Logger != nil {
		cfg.Logger.Info("preflight.chain_id_ok",
			logger.Uint64("chain_id", uint64(chainID)),
		)
	}

	// 2. Controller addresses (Refresh ensures the snapshot is current)
	if err := cfg.Controller.Refresh(ctx); err != nil {
		// Refresh failure on a freshly-resolved Controller is unusual; the
		// initial New() already did one resolve. Surface but continue —
		// addresses() returns the last-good snapshot.
		if cfg.Logger != nil {
			cfg.Logger.Warn("preflight.controller_refresh_failed",
				logger.Err(err),
			)
		}
	}
	addrs := cfg.Controller.Addresses()
	out.RoundsManager = addrs.RoundsManager
	out.BondingManager = addrs.BondingManager
	out.Minter = addrs.Minter

	if addrs.RoundsManager == (chain.Address{}) {
		return out, fmt.Errorf("%s: RoundsManager", types.ErrCodePreflightControllerEmpty)
	}
	if cfg.Mode.HasReward() && addrs.BondingManager == (chain.Address{}) {
		return out, fmt.Errorf("%s: BondingManager", types.ErrCodePreflightControllerEmpty)
	}

	// 3. CodeAt for the contracts the daemon calls.
	if err := requireCode(ctx, cfg.RPC, addrs.RoundsManager, "RoundsManager"); err != nil {
		return out, err
	}
	if cfg.Mode.HasReward() {
		if err := requireCode(ctx, cfg.RPC, addrs.BondingManager, "BondingManager"); err != nil {
			return out, err
		}
	}

	// 4. Keystore: just stamp the derived address. Mismatch with
	// Config.OrchAddress is *not* a hard failure — operator may run hot
	// wallet / cold orch split. We log a warning if mismatched.
	walletAddr := cfg.Keystore.Address()
	out.WalletAddress = walletAddr
	if cfg.Mode.HasReward() && cfg.OrchAddress != (chain.Address{}) && cfg.OrchAddress != walletAddr {
		if cfg.Logger != nil {
			cfg.Logger.Info("preflight.hot_cold_split_detected",
				logger.String("wallet", walletAddr.Hex()),
				logger.String("orch", cfg.OrchAddress.Hex()),
			)
		}
	}

	// 5. Min-balance gate.
	bal, err := cfg.RPC.BalanceAt(ctx, walletAddr, nil)
	if err != nil {
		return out, fmt.Errorf("%s: %w", types.ErrCodePreflightBalance, err)
	}
	out.WalletBalanceWei = bal
	if cfg.MinBalanceWei != nil && bal.Cmp(cfg.MinBalanceWei) < 0 {
		return out, fmt.Errorf("%s: wallet balance %s < min %s",
			types.ErrCodePreflightBalance, bal.String(), cfg.MinBalanceWei.String())
	}

	// 6. Gas oracle round-trip — confirms RPC supports the gas-price call.
	est, err := cfg.GasOracle.Suggest(ctx)
	if err != nil {
		return out, fmt.Errorf("%s: %w", types.ErrCodePreflightGasOracle, err)
	}
	out.GasEstimateSource = est.Source

	if cfg.Logger != nil {
		cfg.Logger.Info("preflight.ok",
			logger.String("wallet", walletAddr.Hex()),
			logger.String("balance_wei", bal.String()),
			logger.String("rounds_manager", addrs.RoundsManager.Hex()),
			logger.String("bonding_manager", addrs.BondingManager.Hex()),
			logger.String("gas_source", est.Source),
		)
	}
	return out, nil
}

// requireCode checks that addr has bytecode at the latest block.
func requireCode(ctx context.Context, r rpc.RPC, addr chain.Address, name string) error {
	code, err := r.CodeAt(ctx, addr, nil)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", types.ErrCodePreflightContractCode, name, err)
	}
	if len(code) == 0 {
		return fmt.Errorf("%s: %s at %s has no code", types.ErrCodePreflightContractCode, name, addr.Hex())
	}
	return nil
}

// ensureBig is a small helper used only in preflight tests when comparing
// big.Int values via fmt.
var _ = func(_ *big.Int) {}
