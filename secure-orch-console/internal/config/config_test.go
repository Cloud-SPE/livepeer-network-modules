package config

import (
	"strings"
	"testing"
)

func TestValidateListenAddr_Accepts(t *testing.T) {
	cases := []string{
		"127.0.0.1:8080",
		"localhost:8080",
		"[::1]:8080",
		"0.0.0.0:8080",
		"10.0.0.1:8080",
		"example.com:8080",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if err := ValidateListenAddr(c); err != nil {
				t.Fatalf("expected accept, got %v", err)
			}
		})
	}
}

func TestValidateListenAddr_Rejects(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"", "required"},
		{":8080", "empty host"},
		{"127.0.0.1:", "empty port"},
		{"::1", "address"},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			err := ValidateListenAddr(c.addr)
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
	if k.Path != "/path/to/ks.json" || k.PasswordFile != "/etc/pw" {
		t.Fatalf("got %+v", k)
	}

	for _, bad := range []string{"", "v3:", "v3", "unknown:path", "yubihsm:tcp://127.0.0.1:12345"} {
		t.Run(bad, func(t *testing.T) {
			if _, err := ParseKeystore(bad, ""); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	c := Config{
		Keystore:        Keystore{Path: "x"},
		LastSignedPath:  "/var/lib/last.json",
		AuditLogPath:    "/var/log/audit.jsonl",
		AuditRotateSize: 100 << 20,
		Listen:          "127.0.0.1:8080",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}

	c.Listen = "0.0.0.0:8080"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected routable bind to validate, got %v", err)
	}

	c.Listen = ":8080"
	if err := c.Validate(); err == nil {
		t.Fatal("expected empty-host rejection")
	}

	c.Listen = "127.0.0.1:8080"
	c.AuditRotateSize = -1
	if err := c.Validate(); err == nil {
		t.Fatal("expected negative rotate-size rejection")
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "empty", in: "", want: ""},
		{name: "trim trailing slash", in: "https://coord.example.com/", want: "https://coord.example.com"},
		{name: "path preserved", in: "https://coord.example.com/admin/", want: "https://coord.example.com/admin"},
		{name: "reject scheme", in: "ftp://coord.example.com", wantErr: "scheme"},
		{name: "reject query", in: "https://coord.example.com/?x=1", wantErr: "query"},
		{name: "reject fragment", in: "https://coord.example.com/#x", wantErr: "fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tc.in)
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
