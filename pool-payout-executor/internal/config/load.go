package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	ccconfig "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
)

// RPCURLsEnv is the environment variable that overrides executor.rpc_urls.
// It is the same name and comma-separated shape every other daemon reads.
const RPCURLsEnv = "CHAIN_RPC_URLS"

func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Load(raw)
	if err != nil {
		return nil, err
	}
	resolvePath := func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" || filepath.IsAbs(value) {
			return value
		}
		return filepath.Join(filepath.Dir(path), value)
	}
	cfg.Executor.KeystorePath = resolvePath(cfg.Executor.KeystorePath)
	cfg.Executor.KeystorePasswordPath = resolvePath(cfg.Executor.KeystorePasswordPath)
	cfg.Executor.StatePath = resolvePath(cfg.Executor.StatePath)
	return cfg, nil
}

func Load(raw []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
	}
	if cfg.PoolController.TimeoutMS == 0 {
		cfg.PoolController.TimeoutMS = 1500
	}
	if cfg.Executor.BatchSize == 0 {
		cfg.Executor.BatchSize = 25
	}
	if cfg.Executor.ExecutorID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "pool-payout-executor"
		}
		cfg.Executor.ExecutorID = host
	}
	if cfg.Executor.LeaseTTLSeconds == 0 {
		cfg.Executor.LeaseTTLSeconds = 300
	}
	if cfg.Executor.ChainID == 0 {
		cfg.Executor.ChainID = 42161
	}
	if cfg.Executor.ConfirmationBlocks == 0 {
		cfg.Executor.ConfirmationBlocks = 1
	}
	if cfg.Executor.RunHistoryLimit == 0 {
		cfg.Executor.RunHistoryLimit = 100
	}
	if cfg.Executor.BackoffBaseMS == 0 {
		cfg.Executor.BackoffBaseMS = 5000
	}
	if cfg.Executor.BackoffMaxMS == 0 {
		cfg.Executor.BackoffMaxMS = 300000
	}
	if cfg.Executor.MaxRetries == 0 {
		cfg.Executor.MaxRetries = 3
	}
	if cfg.Executor.RequeueCooldownSeconds == 0 {
		cfg.Executor.RequeueCooldownSeconds = 3600
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.PoolController.URL == "" {
		return fmt.Errorf("pool_controller.url is required")
	}
	u, err := url.Parse(cfg.PoolController.URL)
	if err != nil {
		return fmt.Errorf("pool_controller.url is invalid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("pool_controller.url scheme must be http or https (got %q)", u.Scheme)
	}
	if cfg.PoolController.BearerToken != "" && cfg.PoolController.BearerTokenRef != "" {
		return fmt.Errorf("pool_controller.bearer_token and pool_controller.bearer_token_ref are mutually exclusive")
	}
	if cfg.PoolController.BearerTokenRef != "" && !strings.HasPrefix(cfg.PoolController.BearerTokenRef, "env://") {
		return fmt.Errorf("pool_controller.bearer_token_ref must use env://")
	}
	if cfg.PoolController.TimeoutMS < 0 {
		return fmt.Errorf("pool_controller.timeout_ms must be >= 0")
	}
	if cfg.Executor.BatchSize < 0 {
		return fmt.Errorf("executor.batch_size must be >= 0")
	}
	if cfg.Executor.LeaseTTLSeconds < 0 {
		return fmt.Errorf("executor.lease_ttl_seconds must be >= 0")
	}
	if cfg.Executor.RunHistoryLimit < 0 {
		return fmt.Errorf("executor.run_history_limit must be >= 0")
	}
	if cfg.Executor.BackoffBaseMS < 0 {
		return fmt.Errorf("executor.backoff_base_ms must be >= 0")
	}
	if cfg.Executor.BackoffMaxMS < 0 {
		return fmt.Errorf("executor.backoff_max_ms must be >= 0")
	}
	if cfg.Executor.BackoffMaxMS > 0 && cfg.Executor.BackoffBaseMS > cfg.Executor.BackoffMaxMS {
		return fmt.Errorf("executor.backoff_base_ms must be <= executor.backoff_max_ms")
	}
	if cfg.Executor.MaxRetries < 0 {
		return fmt.Errorf("executor.max_retries must be >= 0")
	}
	if cfg.Executor.RequeueCooldownSeconds < 0 {
		return fmt.Errorf("executor.requeue_cooldown_seconds must be >= 0")
	}
	if cfg.Executor.PrivateKeyRef != "" && !strings.HasPrefix(cfg.Executor.PrivateKeyRef, "env://") {
		return fmt.Errorf("executor.private_key_ref must use env://")
	}
	if cfg.Executor.KeystorePath != "" && cfg.Executor.KeystorePasswordPath == "" {
		return fmt.Errorf("executor.keystore_password_path is required when executor.keystore_path is set")
	}
	if cfg.Executor.KeystorePasswordPath != "" && cfg.Executor.KeystorePath == "" {
		return fmt.Errorf("executor.keystore_path is required when executor.keystore_password_path is set")
	}
	return nil
}

// applyEnvOverrides lets the environment win over the file for the
// values a compose host shares between services. Today that is only the
// RPC list: CHAIN_RPC_URLS, when set to a non-blank value, replaces
// executor.rpc_urls; blank or unset leaves the file's list alone; a
// malformed value (a blank entry between commas) is an error rather than
// a silent fallback to the file.
func applyEnvOverrides(cfg *Config) error {
	raw, ok := os.LookupEnv(RPCURLsEnv)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	urls, err := ccconfig.ParseRPCURLs(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", RPCURLsEnv, err)
	}
	cfg.Executor.RPCURLs = urls
	return nil
}
