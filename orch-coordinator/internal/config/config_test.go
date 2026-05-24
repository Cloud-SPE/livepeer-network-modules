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

func TestNormalizeOptionalBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "empty", in: "", want: ""},
		{name: "trim trailing slash", in: "https://secure.example.com/", want: "https://secure.example.com"},
		{name: "path preserved", in: "https://secure.example.com/console/", want: "https://secure.example.com/console"},
		{name: "reject scheme", in: "ftp://secure.example.com", wantErr: "scheme"},
		{name: "reject query", in: "https://secure.example.com/?x=1", wantErr: "query"},
		{name: "reject fragment", in: "https://secure.example.com/#x", wantErr: "fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeOptionalBaseURL(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func qstr(s string) string {
	return "\"" + s + "\""
}
