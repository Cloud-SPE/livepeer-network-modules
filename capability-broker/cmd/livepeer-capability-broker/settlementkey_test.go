package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettlementKey_GenerateThenPubkeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settlement.key")
	var out, errOut bytes.Buffer
	if code := runSettlementKey([]string{"generate", "--out", path}, &out, &errOut); code != 0 {
		t.Fatalf("generate exit %d: %s", code, errOut.String())
	}
	generated := strings.TrimSpace(out.String())
	if !strings.HasPrefix(generated, "0x04") || len(generated) != 132 {
		t.Fatalf("generated public key = %q", generated)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %o, want 0600", info.Mode().Perm())
	}

	out.Reset()
	if code := runSettlementKey([]string{"pubkey", "--file", path}, &out, &errOut); code != 0 {
		t.Fatalf("pubkey exit %d: %s", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != generated {
		t.Fatalf("pubkey %s != generated %s", got, generated)
	}

	// A second generate must not clobber a key a manifest may delegate.
	errOut.Reset()
	if code := runSettlementKey([]string{"generate", "--out", path}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "refusing to overwrite") {
		t.Fatalf("overwrite: exit %d, stderr %q", code, errOut.String())
	}
}

func TestSettlementKey_UsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"generate"}, {"pubkey"}} {
		var out, errOut bytes.Buffer
		if code := runSettlementKey(args, &out, &errOut); code != 2 || errOut.Len() == 0 {
			t.Fatalf("args %v: exit %d, stderr %q", args, code, errOut.String())
		}
	}
}
