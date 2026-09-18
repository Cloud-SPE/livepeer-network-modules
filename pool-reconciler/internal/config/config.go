package config

import "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"

type Config struct {
	RevenueSources []revenue.Source `yaml:"revenue_sources,omitempty"`
	PoolController PoolController   `yaml:"pool_controller"`
	PaymentDaemon  PaymentDaemon    `yaml:"payment_daemon,omitempty"`
	Pool           Pool             `yaml:"pool,omitempty"`
	Reconcile      Reconcile        `yaml:"reconcile,omitempty"`
	RoundSource    RoundSource      `yaml:"round_source,omitempty"`
}

type PoolController struct {
	PoolID         string `yaml:"pool_id,omitempty"`
	TokenFile      string `yaml:"token_file,omitempty"`
	CAFile         string `yaml:"ca_file,omitempty"`
	URL            string `yaml:"url"`
	BearerTokenRef string `yaml:"bearer_token_ref,omitempty"`
	TimeoutMS      int    `yaml:"timeout_ms,omitempty"`
}

type RoundSource struct {
	ProtocolDaemonSocket string `yaml:"protocol_daemon_socket,omitempty"`
}

type PaymentDaemon struct {
	SocketPath string `yaml:"socket,omitempty"`
	TimeoutMS  int    `yaml:"timeout_ms,omitempty"`
}

type Pool struct {
	CommissionBPS uint32 `yaml:"commission_bps,omitempty"`
}

type Reconcile struct {
	StatePath     string `yaml:"state_path,omitempty"`
	BackfillLimit int    `yaml:"backfill_limit,omitempty"`
	RetryInterval int    `yaml:"retry_interval_ms,omitempty"`
	// MetricsAddr is the listen address for /metrics scraping during
	// long-running watch-rounds mode. Empty disables the listener.
	MetricsAddr string `yaml:"metrics_addr,omitempty"`
}
