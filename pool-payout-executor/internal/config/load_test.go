package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PoolController.TimeoutMS != 1500 {
		t.Fatalf("TimeoutMS = %d, want 1500", cfg.PoolController.TimeoutMS)
	}
	if cfg.Executor.BatchSize != 25 {
		t.Fatalf("BatchSize = %d, want 25", cfg.Executor.BatchSize)
	}
	if cfg.Executor.ExecutorID == "" || cfg.Executor.LeaseTTLSeconds != 300 {
		t.Fatalf("executor defaults = %#v", cfg.Executor)
	}
	if cfg.Executor.ChainID != 42161 {
		t.Fatalf("ChainID = %d, want 42161", cfg.Executor.ChainID)
	}
	if cfg.Executor.ConfirmationBlocks != 1 {
		t.Fatalf("ConfirmationBlocks = %d, want 1", cfg.Executor.ConfirmationBlocks)
	}
	if cfg.Executor.RunHistoryLimit != 100 {
		t.Fatalf("RunHistoryLimit = %d, want 100", cfg.Executor.RunHistoryLimit)
	}
	if cfg.Executor.BackoffBaseMS != 5000 || cfg.Executor.BackoffMaxMS != 300000 {
		t.Fatalf("backoff defaults = %d/%d, want 5000/300000", cfg.Executor.BackoffBaseMS, cfg.Executor.BackoffMaxMS)
	}
	if cfg.Executor.MaxRetries != 3 || cfg.Executor.RequeueCooldownSeconds != 3600 {
		t.Fatalf("requeue defaults = %d/%d, want 3/3600", cfg.Executor.MaxRetries, cfg.Executor.RequeueCooldownSeconds)
	}
}

func TestLoadRejectsInvalidTokenRef(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
  bearer_token_ref: file:///tmp/token
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid token ref error")
	}
}

func TestLoadRejectsMutuallyExclusiveTokenSources(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
  bearer_token: local-token
  bearer_token_ref: env://POOL_CONTROLLER_ADMIN_TOKEN
`))
	if err == nil {
		t.Fatal("Load() error = nil, want mutually exclusive token source error")
	}
}

func TestLoadRejectsNegativeBatchSize(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  batch_size: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid batch size error")
	}
}

func TestLoadRejectsInvalidPrivateKeyRef(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  private_key_ref: file:///tmp/key
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid private key ref error")
	}
}

func TestLoadRejectsHalfConfiguredKeystore(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  keystore_path: ./keystore.json
`))
	if err == nil {
		t.Fatal("Load() error = nil, want missing keystore password path error")
	}

	_, err = Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  keystore_password_path: ./keystore-password
`))
	if err == nil {
		t.Fatal("Load() error = nil, want missing keystore path error")
	}
}

func TestLoadRejectsNegativeRunHistoryLimit(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  run_history_limit: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid run_history_limit error")
	}
}

func TestLoadRejectsInvalidBackoffRange(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  backoff_base_ms: 6000
  backoff_max_ms: 5000
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid backoff range error")
	}
}

func TestLoadFileResolvesLocalPathsRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "pool-payout-executor-config.local.yaml")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  keystore_path: ./keystore.json
  keystore_password_path: ./keystore-password
  state_path: ./var/state.db
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Executor.KeystorePath != filepath.Join(dir, "keystore.json") {
		t.Fatalf("KeystorePath = %q", cfg.Executor.KeystorePath)
	}
	if cfg.Executor.KeystorePasswordPath != filepath.Join(dir, "keystore-password") {
		t.Fatalf("KeystorePasswordPath = %q", cfg.Executor.KeystorePasswordPath)
	}
	if cfg.Executor.StatePath != filepath.Join(dir, "var/state.db") {
		t.Fatalf("StatePath = %q", cfg.Executor.StatePath)
	}
}

func TestLoadRejectsNegativeRequeuePolicy(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  max_retries: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid max_retries error")
	}

	_, err = Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  requeue_cooldown_seconds: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid requeue_cooldown_seconds error")
	}
}

func TestLoadEnvRPCURLsOverridesFile(t *testing.T) {
	t.Setenv(RPCURLsEnv, " https://env-a , https://env-b ")
	cfg, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  rpc_urls: [https://file-a]
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Executor.RPCURLs) != 2 || cfg.Executor.RPCURLs[0] != "https://env-a" || cfg.Executor.RPCURLs[1] != "https://env-b" {
		t.Fatalf("RPCURLs = %v, want the env list", cfg.Executor.RPCURLs)
	}
}

func TestLoadBlankEnvRPCURLsKeepsFile(t *testing.T) {
	t.Setenv(RPCURLsEnv, "   ")
	cfg, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
executor:
  rpc_urls: [https://file-a]
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Executor.RPCURLs) != 1 || cfg.Executor.RPCURLs[0] != "https://file-a" {
		t.Fatalf("RPCURLs = %v, want the file list", cfg.Executor.RPCURLs)
	}
}

func TestLoadInvalidEnvRPCURLsIsAnError(t *testing.T) {
	t.Setenv(RPCURLsEnv, "https://a,,https://b")
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
`))
	if err == nil || !strings.Contains(err.Error(), "CHAIN_RPC_URLS") || !strings.Contains(err.Error(), "entry 2 is empty") {
		t.Fatalf("Load() error = %v, want CHAIN_RPC_URLS parse error", err)
	}
}
