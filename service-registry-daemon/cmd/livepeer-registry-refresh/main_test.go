package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRawManifest_JSONShapeAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw-registry-manifest.json")
	body := []byte(`{
  "schema_version": "3.0.1",
  "eth_address": "0xabcdef0000000000000000000000000000000000",
  "nodes": [
    {
      "id": "n1",
      "url": "https://orch.example.com:8935",
      "capabilities": [{"name": "openai:/v1/chat/completions"}]
    }
  ]
}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := loadRawManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(body) {
		t.Fatalf("raw manifest drifted")
	}
}

func TestLoadRawManifest_RejectsTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw-registry-manifest.json")
	body := []byte(`{"schema_version":"3.0.1"}{"schema_version":"3.0.1"}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRawManifest(path); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestLoadRawManifest_RejectsNonJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw-registry-manifest.json")
	body := []byte("not-json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRawManifest(path); err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
}
