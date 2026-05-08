package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsage_NonEmpty(t *testing.T) {
	if u := usage(); !strings.Contains(u, "registry") {
		t.Fatalf("usage missing keyword: %s", u)
	}
}

func TestReadPassword_FromFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pw")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readPassword(path); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestReadPassword_FromEnv(t *testing.T) {
	t.Setenv("LIVEPEER_KEYSTORE_PASSWORD", "env-pw")
	if got := readPassword(""); got != "env-pw" {
		t.Fatalf("env: got %q", got)
	}
}

func TestReadPassword_FileMissingFallsBackToEnv(t *testing.T) {
	t.Setenv("LIVEPEER_KEYSTORE_PASSWORD", "env-fallback")
	if got := readPassword("/nonexistent"); got != "env-fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestRun_HelpExits0(t *testing.T) {
	if err := run(context.Background(), []string{"-h"}); err != nil {
		t.Fatalf("run -h: %v", err)
	}
}

func TestRun_BadFlag(t *testing.T) {
	err := run(context.Background(), []string{"--mode=resolver", "--nope"})
	if err == nil {
		t.Fatal("expected error on unknown flag")
	}
}

func TestBuild_ResolverDevSucceeds(t *testing.T) {
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev"})
	if err != nil {
		t.Fatal(err)
	}
	bp, err := build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer bp.Close()
	if bp.chain == nil {
		t.Fatal("chain not set")
	}
	if bp.fetcher == nil {
		t.Fatal("fetcher not set")
	}
	if got := bp.overlayAccessor(); got == nil {
		t.Fatal("overlay accessor returned nil")
	}
}

func TestBuild_ResolverWithOverlayFile(t *testing.T) {
	tmp := t.TempDir()
	yaml := []byte("overlay:\n  - eth_address: \"0xabcdef0000000000000000000000000000000000\"\n    enabled: true\n")
	path := filepath.Join(tmp, "nodes.yaml")
	if err := os.WriteFile(path, yaml, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev", "--static-overlay=" + path})
	if err != nil {
		t.Fatal(err)
	}
	bp, err := build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer bp.Close()
	o := bp.overlayAccessor()
	if e, ok := o.FindByAddress("0xabcdef0000000000000000000000000000000000"); !ok || !e.Enabled {
		t.Fatalf("overlay entry not loaded: %+v", e)
	}
}

func TestBuild_ResolverWithBadOverlayPath(t *testing.T) {
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev", "--static-overlay=/no-such-file.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build(context.Background(), cfg); err == nil {
		t.Fatal("expected error on missing overlay file")
	}
}
