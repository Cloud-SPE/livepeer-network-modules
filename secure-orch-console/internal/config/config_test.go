package config

import (
	"strings"
	"testing"
)

func TestValidateLoopbackAddr_Accepts(t *testing.T) {
	cases := []string{
		"127.0.0.1:8080",
		"localhost:8080",
		"[::1]:8080",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if err := ValidateLoopbackAddr(c); err != nil {
				t.Fatalf("expected accept, got %v", err)
			}
		})
	}
}

func TestValidateLoopbackAddr_Rejects(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"", "required"},
		{":8080", "empty host"},
		{"0.0.0.0:8080", "loopback"},
		{"10.0.0.1:8080", "loopback"},
		{"192.168.1.1:8080", "loopback"},
		{"example.com:8080", "not a literal IP"},
		{"127.0.0.1:", "empty port"},
		{"::1", "address"}, // missing port
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			err := ValidateLoopbackAddr(c.addr)
			if err == nil {
				t.Fatalf("expected reject %q", c.addr)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v missing %q", err, c.want)
			}
		})
	}
}

func TestParseKeystore(t *testing.T) {
	k, err := ParseKeystore("v3:/path/to/ks.json", "/etc/pw")
	if err != nil {
		t.Fatal(err)
	}
	if k.Kind != KeystoreV3 || k.Path != "/path/to/ks.json" || k.PasswordFile != "/etc/pw" {
		t.Fatalf("got %+v", k)
	}

	k, err = ParseKeystore("yubihsm:tcp://127.0.0.1:12345", "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Kind != KeystoreYubiHSM || k.ConnectorURL != "tcp://127.0.0.1:12345" {
		t.Fatalf("got %+v", k)
	}

	for _, bad := range []string{"", "v3:", "v3", "unknown:path", "yubihsm:"} {
		t.Run(bad, func(t *testing.T) {
			if _, err := ParseKeystore(bad, ""); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	c := Config{
		Keystore:       Keystore{Kind: KeystoreV3, Path: "x"},
		InboxDir:       "/var/spool/inbox",
		OutboxDir:      "/var/spool/outbox",
		LastSignedPath: "/var/lib/last.json",
		AuditLogPath:   "/var/log/audit.jsonl",
		Listen:         "127.0.0.1:8080",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}

	c.Listen = "0.0.0.0:8080"
	if err := c.Validate(); err == nil {
		t.Fatal("expected loopback rejection")
	}
}
