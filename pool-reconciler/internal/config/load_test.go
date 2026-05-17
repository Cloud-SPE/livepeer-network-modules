package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.PoolController.TimeoutMS; got != 1500 {
		t.Fatalf("TimeoutMS = %d, want 1500", got)
	}
	if got := cfg.PaymentDaemon.TimeoutMS; got != 1500 {
		t.Fatalf("PaymentDaemon.TimeoutMS = %d, want 1500", got)
	}
	if got := cfg.Reconcile.BackfillLimit; got != 32 {
		t.Fatalf("Reconcile.BackfillLimit = %d, want 32", got)
	}
	if got := cfg.Reconcile.StatePath; got != "/var/lib/livepeer/pool-reconciler-state.db" {
		t.Fatalf("Reconcile.StatePath = %q", got)
	}
	if got := cfg.Reconcile.RetryInterval; got != 5000 {
		t.Fatalf("Reconcile.RetryInterval = %d, want 5000", got)
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

func TestLoadRejectsInvalidCommissionBPS(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
pool:
  commission_bps: 10001
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid commission_bps error")
	}
}

func TestLoadRejectsNegativeBackfillLimit(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
reconcile:
  backfill_limit: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid backfill_limit error")
	}
}

func TestLoadRejectsNegativeRetryInterval(t *testing.T) {
	_, err := Load([]byte(`
pool_controller:
  url: http://pool-controller:8080
reconcile:
  retry_interval_ms: -1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid retry_interval_ms error")
	}
}
