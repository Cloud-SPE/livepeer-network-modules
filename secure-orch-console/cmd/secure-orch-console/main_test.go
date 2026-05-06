package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_RejectsRoutableBind(t *testing.T) {
	dir := t.TempDir()
	keystore := filepath.Join(dir, "ks.json")
	password := filepath.Join(dir, "pw")
	os.WriteFile(keystore, []byte("{}"), 0o600)
	os.WriteFile(password, []byte("pw"), 0o600)
	cases := []string{"0.0.0.0:8080", ":8080", "1.2.3.4:8080"}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			err := run([]string{
				"--keystore=v3:" + keystore,
				"--keystore-password-file=" + password,
				"--listen=" + addr,
				"--last-signed=" + filepath.Join(dir, "last.json"),
				"--audit-log=" + filepath.Join(dir, "audit.jsonl"),
			})
			if err == nil {
				t.Fatalf("expected rejection for %q (hard rule: secure-orch never accepts inbound connections)", addr)
			}
			if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "host") {
				t.Fatalf("error should mention loopback gate: %v", err)
			}
		})
	}
}

func TestRun_RejectsBadKeystoreSelector(t *testing.T) {
	cases := []string{"", "v3", "unknown:foo", "yubihsm:tcp://127.0.0.1:12345"}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			err := run([]string{
				"--keystore=" + k,
				"--listen=127.0.0.1:0",
			})
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
