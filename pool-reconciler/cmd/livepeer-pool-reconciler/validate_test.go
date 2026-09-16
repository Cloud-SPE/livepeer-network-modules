package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRegionalConfigurationWithoutNetworkKeysOrStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `pool_controller:
  pool_id: pool_eu
  url: https://never-contact.invalid
  token_file: /not-mounted/service-token
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runValidateConfig([]string{"--config", path}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "configuration valid (offline)\n" {
		t.Fatal(out.String())
	}
	if err := runValidateConfig(nil, &out); err == nil {
		t.Fatal("missing configuration accepted")
	}
	os.WriteFile(path, []byte(raw+"unknown_safety_option: true\n"), 0600)
	if err := runValidateConfig([]string{"--config", path}, &out); err == nil {
		t.Fatal("unknown setting accepted")
	}
}
