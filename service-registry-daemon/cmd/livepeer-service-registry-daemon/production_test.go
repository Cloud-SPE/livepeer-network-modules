package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProductionChainAndDiagnostics(t *testing.T) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		result := "0x1"
		if request.Method == "eth_chainId" {
			result = "0xa4b1"
		}
		if request.Method == "eth_call" {
			result = "0x" + strings.Repeat("0", 63) + "1"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer rpc.Close()
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--chain-rpc-urls=" + rpc.URL, "--store-path=" + filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	bp, err := build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()
	if bp.roundClock == nil || bp.discovery == nil {
		t.Fatal("chain providers missing")
	}
	if bp.diagnostics.Snapshot().ChainOK {
		t.Fatal("unattempted serviceURI read claimed healthy")
	}
}
func TestRPCListFlagRejectsInvalidLists(t *testing.T) {
	for _, v := range []string{"", "https://rpc.example.com,"} {
		var l csvList
		if err := l.Set(v); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	var l csvList
	if err := l.Set("https://rpc.example.com,https://backup.example.com"); err != nil || len(l) != 2 {
		t.Fatalf("got %v %v", l, err)
	}
}

func TestFailedChainValidationReleasesStore(t *testing.T) {
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--chain-rpc-urls=://", "--store-path=" + filepath.Join(t.TempDir(), "cache.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build(context.Background(), cfg); err == nil {
		t.Fatal("expected chain identity failure")
	}
	cfg.Discovery = "overlay-only"
	bp, err := build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store leaked after failed build: %v", err)
	}
	defer bp.Close()
}
