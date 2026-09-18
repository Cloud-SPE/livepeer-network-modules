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

const settlementKeyA = "0x049d2193d32d9379271df49fcdd6d2b53dad719371ddfb77009494d2c08ceca2bbea717657d9e62d49f11ac13f8ee3ae9dbeea45c1db363ed200edd9618f027f48"
const settlementKeyB = "0x0453180efc760bdd8a7ba16997caa23e1a173e4e45deb8f1b5b379e4eab21cf9eb0e1a2d48eb7d47ae60fdc76b61210f9423d4387eaec5787ed136fefc51c935ef"

func settlementConfig(keys string) []byte {
	return []byte(`identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: http://10.0.0.5:8080
settlement_keys:
` + keys)
}

func TestLoadBytes_SettlementKeys(t *testing.T) {
	cfg, err := LoadBytes(settlementConfig(`  - label: ai2-rig-broker
    public_key: "` + settlementKeyA + `"
    not_before: 2026-09-07T00:00:00Z
    expires_at: 2027-09-07T00:00:00Z
  - label: eu-central-broker
    public_key: "0x` + strings.ToUpper(settlementKeyB[2:]) + `"
    not_before: 2026-09-07T00:00:00Z
    expires_at: 2027-09-07T00:00:00Z
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(cfg.SettlementKeys) != 2 {
		t.Fatalf("settlement_keys: want 2, got %d", len(cfg.SettlementKeys))
	}
	if cfg.SettlementKeys[0].Label != "ai2-rig-broker" || cfg.SettlementKeys[0].PublicKey != settlementKeyA {
		t.Fatalf("key[0]: %+v", cfg.SettlementKeys[0])
	}
	// The schema pattern is lower-case only; an upper-case key is
	// normalized rather than rejected, so the published bytes validate.
	if cfg.SettlementKeys[1].PublicKey != settlementKeyB {
		t.Fatalf("key[1] not normalized to lower case: %s", cfg.SettlementKeys[1].PublicKey)
	}
	want := time.Date(2027, 9, 7, 0, 0, 0, 0, time.UTC)
	if !cfg.SettlementKeys[1].ExpiresAt.Equal(want) {
		t.Fatalf("key[1].expires_at = %s, want %s", cfg.SettlementKeys[1].ExpiresAt, want)
	}
}

func TestLoadBytes_SettlementKeysAbsentIsValid(t *testing.T) {
	cfg, err := LoadBytes(settlementConfig("  []\n"))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(cfg.SettlementKeys) != 0 {
		t.Fatalf("want no keys, got %d", len(cfg.SettlementKeys))
	}
}

func TestLoadBytes_RejectsBadSettlementKeys(t *testing.T) {
	window := `    not_before: 2026-09-07T00:00:00Z
    expires_at: 2027-09-07T00:00:00Z
`
	for _, tc := range []struct{ name, keys, wantErr string }{
		{"too short", `  - public_key: "0x04abcd"
` + window, "settlement_keys[0].public_key: must be 0x + 130 hex"},
		{"not hex", `  - public_key: "` + settlementKeyA[:len(settlementKeyA)-1] + `z"
` + window, "settlement_keys[0].public_key: must be valid hex"},
		{"compressed", `  - public_key: "0x02` + settlementKeyA[4:] + `"
` + window, "uncompressed key"},
		{"missing not_before", `  - public_key: "` + settlementKeyA + `"
    expires_at: 2027-09-07T00:00:00Z
`, "settlement_keys[0].not_before: required"},
		{"reversed window", `  - public_key: "` + settlementKeyA + `"
    not_before: 2027-09-07T00:00:00Z
    expires_at: 2026-09-07T00:00:00Z
`, "expires_at 2026-09-07T00:00:00Z must be after not_before"},
		{"duplicate", `  - public_key: "` + settlementKeyA + `"
` + window + `  - public_key: "0x` + strings.ToUpper(settlementKeyA[2:]) + `"
` + window, "settlement_keys[1].public_key: duplicate of settlement_keys[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBytes(settlementConfig(tc.keys))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadBytes_SettlementKeyValidity(t *testing.T) {
	base := `identity:
  orch_eth_address: "0xabcdef1234567890abcdef1234567890abcdef12"
brokers:
  - name: a
    base_url: http://10.0.0.5:8080
publish:
  settlement_key_validity: `
	cfg, err := LoadBytes([]byte(base + "2160h\n"))
	if err != nil || cfg.Publish.SettlementKeyValidity != 90*24*time.Hour {
		t.Fatalf("90d: cfg=%+v err=%v", cfg, err)
	}
	if _, err := LoadBytes([]byte(base + "1h\n")); err == nil || !strings.Contains(err.Error(), "at least 24h") {
		t.Fatalf("1h accepted: %v", err)
	}
	if _, err := LoadBytes([]byte(base + "-24h\n")); err == nil {
		t.Fatal("negative accepted")
	}
}
