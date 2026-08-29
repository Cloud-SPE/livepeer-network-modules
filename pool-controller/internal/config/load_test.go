package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Listen.Paid; got != ":8080" {
		t.Fatalf("Listen.Paid = %q, want :8080", got)
	}
	if got := cfg.Listen.Metrics; got != ":9090" {
		t.Fatalf("Listen.Metrics = %q, want :9090", got)
	}
	if got := cfg.Scoring.CooldownDurationMS; got != 300000 {
		t.Fatalf("Scoring.CooldownDurationMS = %d, want 300000", got)
	}
	if got := cfg.Bootstrap.BrokerAdminTimeoutMS; got != 5000 {
		t.Fatalf("Bootstrap.BrokerAdminTimeoutMS = %d, want 5000", got)
	}
}

func TestLoadBrokerAdminValidation(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  broker_admin_url: http://broker-admin.local
  broker_admin_auth:
    method: bearer
    secret_ref: env://BROKER_ADMIN_TOKEN
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Bootstrap.BrokerAdminURL; got != "http://broker-admin.local" {
		t.Fatalf("Bootstrap.BrokerAdminURL = %q, want broker admin url", got)
	}

	_, err = Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  broker_admin_url: ftp://broker-admin.local
`))
	if err == nil || !strings.Contains(err.Error(), "bootstrap.broker_admin_url scheme") {
		t.Fatalf("Load() error = %v, want broker_admin_url scheme validation", err)
	}
}

func TestLoadPublicPoolURLs(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  public_controller_url: https://pool.example.com
  public_broker_url: https://broker.example.com
  public_broker_quic_addr: broker.example.com:8443
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Bootstrap.PublicBrokerQUICAddr; got != "broker.example.com:8443" {
		t.Fatalf("Bootstrap.PublicBrokerQUICAddr = %q, want broker.example.com:8443", got)
	}

	_, err = Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  public_broker_quic_addr: broker.example.com
`))
	if err == nil || !strings.Contains(err.Error(), "bootstrap.public_broker_quic_addr") {
		t.Fatalf("Load() error = %v, want public broker quic addr validation", err)
	}
}

func TestLoadScoringWeightDefaultsFollowPartialOverride(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
scoring:
  window_score_weight: 0.6
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Scoring.WindowScoreWeight; got != 0.6 {
		t.Fatalf("Scoring.WindowScoreWeight = %v, want 0.6", got)
	}
	if got := cfg.Scoring.EMAScoreWeight; got != 0.4 {
		t.Fatalf("Scoring.EMAScoreWeight = %v, want 0.4", got)
	}
}

func TestLoadRejectsConflictingAdminTokenConfig(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
admin_auth:
  bearer_token: abc
  bearer_token_ref: env://TOKEN
`))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("Load() error = %v, want mutually exclusive admin auth validation", err)
	}
}
