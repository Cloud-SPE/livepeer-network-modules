// Package config holds the validated config used to construct chain-commons
// providers and services.
//
// Daemons embed Config in their own larger config struct; this package owns
// only the chain-glue concerns. Loading from YAML lives in the daemon (each
// daemon has its own YAML schema); this package validates an in-memory
// struct.
package config

import (
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// Config holds every value chain-commons needs to operate.
type Config struct {
	// EthURLs is the ordered list of Ethereum RPC endpoints. Index 0 is the
	// primary; subsequent entries are backups consulted only when earlier
	// circuits are open. Required, non-empty.
	EthURLs []string

	// ChainID is the chain ID we expect at startup. Mismatch with the actual
	// chain rejects the configuration (preflight gate).
	ChainID chain.ChainID

	// KeystorePath is the path to the V3 JSON keystore file. Required when
	// any service signs transactions.
	KeystorePath string

	// KeystorePassword is the keystore unlock password. Read at startup and
	// not retained (the unlocked key lives in process memory; the password
	// string is zeroed where supported).
	KeystorePassword string

	// AccountAddress is the expected address derived from the keystore.
	// Optional: when set, preflight rejects if the keystore decrypts to a
	// different address. Useful for hot-wallet/cold-orch deployments.
	AccountAddress chain.Address

	// ControllerAddr is the on-chain Controller contract that resolves
	// sub-contract addresses. Required (unless SkipController is true and
	// every sub-contract is provided via ContractOverrides).
	ControllerAddr chain.Address

	// SkipController disables Controller resolution at startup. Used for
	// forks and local testing where no Controller is deployed; ContractOverrides
	// must provide every sub-contract address.
	SkipController bool

	// ContractOverrides pins specific sub-contract addresses by name (matching
	// the Controller's getContract name space). Used for staging environments
	// or to test against a specific contract revision. Logged at WARN when
	// non-empty.
	ContractOverrides map[string]chain.Address

	// GasLimit is the gas limit applied to transactions submitted via TxIntent.
	GasLimit uint64

	// GasPriceMin is the floor below which the gas oracle never quotes. Set
	// to nil to disable.
	GasPriceMin chain.GasPrice

	// GasPriceMax is the ceiling above which transactions are rejected
	// (operator protection against runaway costs). Set to nil to disable.
	GasPriceMax chain.GasPrice

	// GasPriceCacheTTL is the TTL of the gas oracle's cached eth_gasPrice
	// response. Default 5s.
	GasPriceCacheTTL time.Duration

	// BlockPollInterval is how often providers/logs and providers/timesource
	// poll the RPC for new blocks. Default 5s.
	BlockPollInterval time.Duration

	// LogChunkSize is the per-request range of providers/logs's eth_getLogs
	// calls. Default 1000 blocks.
	LogChunkSize uint64

	// ReorgConfirmations is the number of blocks deeper than a tx's MinedBlock
	// before TxIntent transitions to confirmed. Default 4 (Arbitrum tuned).
	ReorgConfirmations uint64

	// RPC holds the multi-URL retry/circuit-breaker policy. See RPCPolicy.
	RPC RPCPolicy

	// TxIntent holds the durable transaction state-machine policy. See
	// TxIntentPolicy.
	TxIntent TxIntentPolicy

	// StorePath is the path to the BoltDB file used by all chain-commons
	// services that need persistence. Daemons typically share one file
	// across services. Required.
	StorePath string

	// ControllerRefreshInterval is how often providers/controller re-resolves
	// sub-contract addresses. Default 1h.
	ControllerRefreshInterval time.Duration
}

// RPCPolicy holds the multi-URL retry and circuit-breaker tuning.
//
// Defaults are tuned for Arbitrum production: 6 retries with exponential
// backoff up to 30s; 5 consecutive failures opens the circuit; 60s cooloff
// before half-open; lightweight ChainID() probe every 30s.
type RPCPolicy struct {
	MaxRetries              int
	InitialBackoff          time.Duration
	BackoffFactor           float64
	MaxBackoff              time.Duration
	HealthProbeInterval     time.Duration
	CircuitBreakerThreshold int
	CircuitBreakerCooloff   time.Duration
	CallTimeout             time.Duration
}

// TxIntentPolicy holds the durable transaction state-machine tuning.
//
// Defaults: 5min before replacement, up to 3 replacements, +11% gas bump per
// replacement, RLP encoding of KeyParams.
type TxIntentPolicy struct {
	SubmitTimeout      time.Duration
	MaxReplacements    int
	ReplacementGasBump int    // percent (e.g. 11)
	KeyParamsEncoding  string // "rlp" (default) | "canonical-json"
}

// Default returns a Config with sensible defaults. Callers fill in EthURLs,
// ChainID, KeystorePath, KeystorePassword, ControllerAddr, and StorePath
// from their daemon's config and call Validate().
func Default() Config {
	return Config{
		GasLimit:                  1_000_000,
		GasPriceCacheTTL:          5 * time.Second,
		BlockPollInterval:         5 * time.Second,
		LogChunkSize:              1000,
		ReorgConfirmations:        4,
		ControllerRefreshInterval: 1 * time.Hour,

		RPC: RPCPolicy{
			MaxRetries:              6,
			InitialBackoff:          500 * time.Millisecond,
			BackoffFactor:           2.0,
			MaxBackoff:              30 * time.Second,
			HealthProbeInterval:     30 * time.Second,
			CircuitBreakerThreshold: 5,
			CircuitBreakerCooloff:   60 * time.Second,
			CallTimeout:             2 * time.Minute,
		},
		TxIntent: TxIntentPolicy{
			SubmitTimeout:      5 * time.Minute,
			MaxReplacements:    3,
			ReplacementGasBump: 11,
			KeyParamsEncoding:  "rlp",
		},
	}
}

// Validate enforces config invariants. Returns nil if the config is usable.
func (c *Config) Validate() error {
	if len(c.EthURLs) == 0 {
		return fmt.Errorf("config: EthURLs is required (at least one URL)")
	}
	if c.ChainID == 0 {
		return fmt.Errorf("config: ChainID is required (non-zero)")
	}
	if c.KeystorePath == "" {
		return fmt.Errorf("config: KeystorePath is required")
	}
	if _, err := os.Stat(c.KeystorePath); err != nil {
		return fmt.Errorf("config: KeystorePath %q is not readable: %w", c.KeystorePath, err)
	}
	if !c.SkipController && (c.ControllerAddr == (chain.Address{})) {
		return fmt.Errorf("config: ControllerAddr is required (or set SkipController=true with ContractOverrides for every sub-contract)")
	}
	if c.StorePath == "" {
		return fmt.Errorf("config: StorePath is required")
	}
	if c.GasLimit == 0 {
		return fmt.Errorf("config: GasLimit must be > 0")
	}
	if c.ReorgConfirmations < 1 {
		return fmt.Errorf("config: ReorgConfirmations must be >= 1")
	}
	if c.LogChunkSize == 0 {
		return fmt.Errorf("config: LogChunkSize must be > 0")
	}
	if c.BlockPollInterval <= 0 {
		return fmt.Errorf("config: BlockPollInterval must be > 0")
	}
	if c.GasPriceMin != nil && c.GasPriceMax != nil && c.GasPriceMin.Cmp(c.GasPriceMax) > 0 {
		return fmt.Errorf("config: GasPriceMin must be <= GasPriceMax")
	}
	if c.RPC.MaxRetries < 0 {
		return fmt.Errorf("config: RPC.MaxRetries must be >= 0")
	}
	if c.RPC.CircuitBreakerThreshold < 1 {
		return fmt.Errorf("config: RPC.CircuitBreakerThreshold must be >= 1")
	}
	if c.RPC.BackoffFactor < 1.0 {
		return fmt.Errorf("config: RPC.BackoffFactor must be >= 1.0")
	}
	if c.TxIntent.MaxReplacements < 0 {
		return fmt.Errorf("config: TxIntent.MaxReplacements must be >= 0")
	}
	if c.TxIntent.ReplacementGasBump < 0 || c.TxIntent.ReplacementGasBump > 100 {
		return fmt.Errorf("config: TxIntent.ReplacementGasBump must be in [0, 100]")
	}
	if c.TxIntent.KeyParamsEncoding != "rlp" && c.TxIntent.KeyParamsEncoding != "canonical-json" {
		return fmt.Errorf("config: TxIntent.KeyParamsEncoding must be 'rlp' or 'canonical-json'")
	}
	return nil
}

// Wei is a small helper for constructing chain.Wei from a uint64.
func Wei(n uint64) chain.Wei {
	return new(big.Int).SetUint64(n)
}
