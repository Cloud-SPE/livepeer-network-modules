// Package config holds the validated daemon-level configuration.
//
// Embeds chain-commons/config.Config for the chain-glue knobs and adds the
// protocol-daemon-specific fields on top: mode, orchestrator address, init
// jitter, min-balance gate.
package config

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaincfg "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// Config is the protocol-daemon configuration.
type Config struct {
	// Chain holds the chain-commons config (EthURLs, ChainID, KeystorePath,
	// ControllerAddr, GasLimit, etc.).
	Chain chaincfg.Config

	// Mode is the daemon's operating mode.
	Mode types.Mode

	// OrchAddress is the on-chain orchestrator (cold) address whose
	// transcoder we call rewardWithHint for. Required in reward mode.
	OrchAddress chain.Address

	// AIServiceRegistryAddress is the separate AI registry contract used
	// for AI-specific serviceURI publication and status reads.
	AIServiceRegistryAddress chain.Address

	// InitJitter is a maximum random delay introduced before submitting an
	// initializeRound call. Used to avoid collisions when fleets of
	// orchestrators all run a round-init daemon. Default 0 (no jitter).
	InitJitter time.Duration

	// MinBalanceWei is the minimum wallet balance required at startup.
	// Daemon refuses to start when balance is below. Default 5e15 wei.
	MinBalanceWei chain.Wei

	// SocketPath is the unix socket the gRPC server listens on. Default
	// $XDG_RUNTIME_DIR/livepeer-protocol-daemon.sock.
	SocketPath string

	// MetricsListen is the host:port the Prometheus listener binds to.
	// Empty = listener disabled (default).
	MetricsListen string

	// MetricsMaxSeries caps distinct label tuples per metric. 0 = no cap.
	MetricsMaxSeries int

	// Dev enables development mode: chain-commons.testing fakes are wired
	// in instead of real RPC / keystore. Useful for local dev and CI.
	Dev bool

	// Version is the binary's build version, stamped by ldflags.
	Version string
}

// Default returns a Config with sensible defaults. Callers fill in the
// network-specific values.
func Default() Config {
	return Config{
		Chain: chaincfg.Default(),
		Mode:  types.ModeBoth,
		AIServiceRegistryAddress: chain.Address{
			0x04, 0xC0, 0xB2, 0x49, 0x74, 0x01, 0x75, 0x99,
			0x9E, 0x5B, 0xF5, 0xC9, 0xAC, 0x1D, 0xA9, 0x24,
			0x31, 0xEF, 0x34, 0xC5,
		},
		InitJitter:       0,
		MinBalanceWei:    new(big.Int).SetUint64(5_000_000_000_000_000), // 5e15 wei ~ 0.005 ETH
		MetricsMaxSeries: 10_000,
	}
}

// Validate enforces invariants. Returns the first violation.
func (c *Config) Validate() error {
	if err := c.Mode.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Dev mode: relax requirements that would normally come from --eth-urls,
	// --keystore-path, etc. The dev-mode wire-up uses chain-commons.testing
	// fakes, so the chain-commons config doesn't need to validate here.
	if c.Dev {
		// Reward mode in dev still needs an orchestrator address; we
		// stamp the FakeKeystore-derived address upstream when none is
		// provided. Defer that validation to the wire-up code.
		return nil
	}
	if err := c.Chain.Validate(); err != nil {
		return err
	}
	if c.Mode.HasReward() && c.OrchAddress == (chain.Address{}) {
		return errors.New("config: --orch-address is required in reward mode")
	}
	if c.SocketPath == "" {
		return errors.New("config: --socket is required (unix socket path for the gRPC listener)")
	}
	if c.MinBalanceWei == nil || c.MinBalanceWei.Sign() < 0 {
		return errors.New("config: MinBalanceWei must be non-negative")
	}
	if c.InitJitter < 0 {
		return errors.New("config: InitJitter must be non-negative")
	}
	return nil
}

// String returns a redacted summary safe to log.
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{mode=%s eth_urls=%d chain_id=%d controller=%s orch=%s ai_service_registry=%s init_jitter=%s min_balance=%s metrics_listen=%q dev=%v}",
		c.Mode, len(c.Chain.EthURLs), c.Chain.ChainID, c.Chain.ControllerAddr.Hex(), c.OrchAddress.Hex(),
		c.AIServiceRegistryAddress.Hex(), c.InitJitter, c.MinBalanceWei, c.MetricsListen, c.Dev,
	)
}
