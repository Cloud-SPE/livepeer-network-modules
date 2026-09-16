package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializePoolIdentityOfflineAndNeverReplaceExpectedStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "controller")
	var first bytes.Buffer
	if err := run([]string{"init-identity", "--data-dir", dir}, &first, &first); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		PoolID string `json:"pool_id"`
	}
	if err := json.Unmarshal(first.Bytes(), &identity); err != nil || identity.PoolID == "" {
		t.Fatal(first.String(), err)
	}
	var again bytes.Buffer
	if err := run([]string{"init-identity", "--data-dir", dir, "--expect-pool-id", identity.PoolID}, &again, &again); err != nil || first.String() != again.String() {
		t.Fatal(again.String(), err)
	}
	if err := runInitializeIdentity([]string{"--data-dir", dir, "--expect-pool-id", "other"}, &again); err == nil {
		t.Fatal("wrong identity accepted")
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if err := runInitializeIdentity([]string{"--data-dir", missing, "--expect-pool-id", identity.PoolID}, &again); err == nil {
		t.Fatal("missing expected store initialized")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("replacement data directory created")
	}
}
func TestOfflineConfigRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var output bytes.Buffer
	if err := os.WriteFile(path, []byte("identity:\n  orch_eth_address: 0x1111111111111111111111111111111111111111\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runValidateConfig([]string{"--config", path}, &output); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("identity:\n  orch_eth_address: 0x1111111111111111111111111111111111111111\nmisspelled_safety_option: true\n"), 0600)
	if err := runValidateConfig([]string{"--config", path}, &output); err == nil {
		t.Fatal("unknown config silently ignored")
	}
}

func TestOfflineConfigRejectsUnknownFleetTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("identity:\n  orch_eth_address: 0x1111111111111111111111111111111111111111\nbootstrap:\n  brokers:\n    - admin_url: https://broker.invalid\n      template_ids: [unknown-template]\n"), 0600)
	var output bytes.Buffer
	if err := runValidateConfig([]string{"--config", path}, &output); err == nil {
		t.Fatal("unknown fleet template passed offline preflight")
	}
}
