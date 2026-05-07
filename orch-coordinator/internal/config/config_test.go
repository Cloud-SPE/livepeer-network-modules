package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadBytes_HappyPath(t *testing.T) {
	raw := []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: http://10.0.0.5:8080
  - name: b
    base_url: http://10.0.0.6:8080
publish:
  manifest_ttl: 12h
`)
	cfg, err := LoadBytes(raw)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.EthAddress() != "0xabcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("EthAddress: %q", cfg.EthAddress())
	}
	if len(cfg.Brokers) != 2 {
		t.Fatalf("brokers: want 2, got %d", len(cfg.Brokers))
	}
	if cfg.Publish.ManifestTTL != 12*time.Hour {
		t.Fatalf("manifest_ttl: %v", cfg.Publish.ManifestTTL)
	}
}

func TestLoadBytes_RejectsBadEthAddress(t *testing.T) {
	cases := []string{
		"",
		"abcd",
		"0x1234",
		"0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
	}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			raw := []byte("identity:\n  orch_eth_address: " + qstr(addr) + "\nbrokers:\n  - name: a\n    base_url: http://x:1\n")
			if _, err := LoadBytes(raw); err == nil {
				t.Fatalf("expected error for addr %q", addr)
			}
		})
	}
}

func TestLoadBytes_RequiresBrokers(t *testing.T) {
	raw := []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers: []
`)
	if _, err := LoadBytes(raw); err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("expected broker-required error, got %v", err)
	}
}

func TestLoadBytes_RejectsDuplicateBrokerName(t *testing.T) {
	raw := []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: http://x:1
  - name: a
    base_url: http://y:1
`)
	if _, err := LoadBytes(raw); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestLoadBytes_RejectsBadBaseURL(t *testing.T) {
	raw := []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: "ftp://elsewhere/path"
`)
	if _, err := LoadBytes(raw); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme error, got %v", err)
	}
}

func TestLoadBytes_RejectsUnknownField(t *testing.T) {
	raw := []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: http://x:1
random_field: 1
`)
	if _, err := LoadBytes(raw); err == nil {
		t.Fatalf("expected error on unknown field")
	}
}

func qstr(s string) string {
	return "\"" + s + "\""
}
