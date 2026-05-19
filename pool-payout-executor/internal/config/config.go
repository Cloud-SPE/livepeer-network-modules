package config

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
	BatchSize              int    `yaml:"batch_size,omitempty"`
	ExecutorID             string `yaml:"executor_id,omitempty"`
	LeaseTTLSeconds        int    `yaml:"lease_ttl_seconds,omitempty"`
	RPCURL                 string `yaml:"rpc_url,omitempty"`
	ChainID                uint64 `yaml:"chain_id,omitempty"`
	PrivateKeyRef          string `yaml:"private_key_ref,omitempty"`
	KeystorePath           string `yaml:"keystore_path,omitempty"`
	KeystorePasswordPath   string `yaml:"keystore_password_path,omitempty"`
	ConfirmationBlocks     uint64 `yaml:"confirmation_blocks,omitempty"`
	StatePath              string `yaml:"state_path,omitempty"`
	RunHistoryLimit        int    `yaml:"run_history_limit,omitempty"`
	BackoffBaseMS          int    `yaml:"backoff_base_ms,omitempty"`
	BackoffMaxMS           int    `yaml:"backoff_max_ms,omitempty"`
	AutoRequeueFailed      bool   `yaml:"auto_requeue_failed,omitempty"`
	MaxRetries             int    `yaml:"max_retries,omitempty"`
	RequeueCooldownSeconds int    `yaml:"requeue_cooldown_seconds,omitempty"`
	// MetricsAddr is the listen address for /metrics scraping during
	// long-running reconcile-loop mode. Empty disables the listener.
	MetricsAddr string `yaml:"metrics_addr,omitempty"`
}
