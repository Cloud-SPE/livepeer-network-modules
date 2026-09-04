package config

import (
	"path/filepath"
	"strings"
)

type Config struct {
	PoolController PoolController `yaml:"pool_controller"`
	Executor       Executor       `yaml:"executor,omitempty"`
}

type PoolController struct {
	URL            string `yaml:"url"`
	BearerToken    string `yaml:"bearer_token,omitempty"`
	BearerTokenRef string `yaml:"bearer_token_ref,omitempty"`
	TimeoutMS      int    `yaml:"timeout_ms,omitempty"`
}

type Executor struct {
	BatchSize       int    `yaml:"batch_size,omitempty"`
	ExecutorID      string `yaml:"executor_id,omitempty"`
	LeaseTTLSeconds int    `yaml:"lease_ttl_seconds,omitempty"`
	// RPCURLs lists JSON-RPC endpoints, primary first; every chain call
	// fails over between them. Precedence: the CHAIN_RPC_URLS environment
	// variable (comma-separated, same shape every other daemon takes),
	// when set and non-blank, replaces this list entirely, so a compose
	// host carries one RPC list for all of its services.
	RPCURLs              []string `yaml:"rpc_urls,omitempty"`
	ChainID              uint64   `yaml:"chain_id,omitempty"`
	PrivateKeyRef        string   `yaml:"private_key_ref,omitempty"`
	KeystorePath         string   `yaml:"keystore_path,omitempty"`
	KeystorePasswordPath string   `yaml:"keystore_password_path,omitempty"`
	ConfirmationBlocks   uint64   `yaml:"confirmation_blocks,omitempty"`
	StatePath            string   `yaml:"state_path,omitempty"`
	// IntentStorePath is the BoltDB file chain-commons's transaction
	// intent machine keeps its durable state in: one record per payout,
	// with every attempt's tx hash and nonce, so a restart resumes
	// tracking instead of re-sending. Default: payout-intents.db next to
	// state_path, or ./payout-intents.db when state_path is unset. It is
	// a separate file from state_path because internal/repo is the only
	// owner of that one.
	IntentStorePath string `yaml:"intent_store_path,omitempty"`
	// ConfirmWaitMS bounds how long a confirm pass waits for an in-flight
	// payout to reach a terminal state before reporting it as still
	// pending. Default 2000. The intent processor keeps tracking in the
	// background regardless; this only shapes the reconcile output.
	ConfirmWaitMS int `yaml:"confirm_wait_ms,omitempty"`
	// ReplaceAfterSeconds is how long a broadcast payout may sit unmined
	// before the processor re-signs it at the same nonce with bumped gas.
	// 0 = chain-commons default (300).
	ReplaceAfterSeconds int `yaml:"replace_after_seconds,omitempty"`
	// MaxReplacements caps gas-bump replacements per payout before it is
	// reported failed (replacement_exhausted). 0 = chain-commons default (3).
	MaxReplacements        int  `yaml:"max_replacements,omitempty"`
	RunHistoryLimit        int  `yaml:"run_history_limit,omitempty"`
	BackoffBaseMS          int  `yaml:"backoff_base_ms,omitempty"`
	BackoffMaxMS           int  `yaml:"backoff_max_ms,omitempty"`
	AutoRequeueFailed      bool `yaml:"auto_requeue_failed,omitempty"`
	MaxRetries             int  `yaml:"max_retries,omitempty"`
	RequeueCooldownSeconds int  `yaml:"requeue_cooldown_seconds,omitempty"`
	// MetricsAddr is the listen address for /metrics scraping during
	// long-running reconcile-loop mode. Empty disables the listener.
	MetricsAddr string `yaml:"metrics_addr,omitempty"`
}

// IntentStoreFile returns the resolved intent store path: the explicit
// intent_store_path, else payout-intents.db beside state_path, else
// ./payout-intents.db.
func (e Executor) IntentStoreFile() string {
	if p := strings.TrimSpace(e.IntentStorePath); p != "" {
		return p
	}
	if sp := strings.TrimSpace(e.StatePath); sp != "" {
		return filepath.Join(filepath.Dir(sp), "payout-intents.db")
	}
	return "payout-intents.db"
}
