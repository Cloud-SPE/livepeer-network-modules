package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Load(raw)
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
	if cfg.PoolController.TimeoutMS == 0 {
		cfg.PoolController.TimeoutMS = 1500
	}
	if cfg.PaymentDaemon.TimeoutMS == 0 {
		cfg.PaymentDaemon.TimeoutMS = 1500
	}
	if cfg.Reconcile.StatePath == "" {
		cfg.Reconcile.StatePath = "/var/lib/livepeer/pool-reconciler-state.db"
	}
	if cfg.Reconcile.BackfillLimit == 0 {
		cfg.Reconcile.BackfillLimit = 32
	}
	if cfg.Reconcile.RetryInterval == 0 {
		cfg.Reconcile.RetryInterval = 5000
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
	if cfg.PoolController.BearerTokenRef != "" && !strings.HasPrefix(cfg.PoolController.BearerTokenRef, "env://") {
		return fmt.Errorf("pool_controller.bearer_token_ref must use env://")
	}
	if cfg.PoolController.TimeoutMS < 0 {
		return fmt.Errorf("pool_controller.timeout_ms must be >= 0")
	}
	if cfg.PaymentDaemon.TimeoutMS < 0 {
		return fmt.Errorf("payment_daemon.timeout_ms must be >= 0")
	}
	if cfg.Pool.CommissionBPS > 10000 {
		return fmt.Errorf("pool.commission_bps must be <= 10000")
	}
	if cfg.Reconcile.BackfillLimit < 0 {
		return fmt.Errorf("reconcile.backfill_limit must be >= 0")
	}
	if cfg.Reconcile.RetryInterval < 0 {
		return fmt.Errorf("reconcile.retry_interval_ms must be >= 0")
	}
	return nil
}
