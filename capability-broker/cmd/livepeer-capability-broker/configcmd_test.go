package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-config.yaml")
	valid := `
identity:
  orch_eth_address: 0x1234567890abcdef1234567890abcdef12345678
admin_auth: {method: bearer, secret_ref: env://BROKER_ADMIN_TOKEN}
payment_daemon: {mock: true}
offers_source: admin
offers: []
`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runConfig([]string{"validate", "--config", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "offers_source=admin offers=0") {
		t.Fatalf("stdout=%q", got)
	}

	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runConfig([]string{"validate", "--config", path}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "field unknown not found") {
		t.Fatalf("invalid config exit=%d stderr=%q", code, stderr.String())
	}
}

func TestConfigValidateUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"validate"}} {
		var stdout, stderr bytes.Buffer
		if code := runConfig(args, &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
	}
}
