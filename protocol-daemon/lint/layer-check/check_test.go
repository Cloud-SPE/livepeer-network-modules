package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRepoCleanRoot(t *testing.T) {
	dir := t.TempDir()
	// Empty repo → no findings, no error.
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(findings))
	}
}

func TestServiceImportingEthereumIsFlagged(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "service", "x")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package x
import _ "github.com/ethereum/go-ethereum/common"
`
	if err := os.WriteFile(filepath.Join(pkg, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(findings), findings)
	}
	if findings[0].RuleID != "service-no-eth" {
		t.Fatalf("RuleID = %s; want service-no-eth", findings[0].RuleID)
	}
	if !strings.Contains(findings[0].String(), "service-no-eth") {
		t.Fatalf("finding string missing rule: %s", findings[0])
	}
}

func TestServiceImportingBBoltIsFlagged(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "service", "y")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package y
import _ "go.etcd.io/bbolt"
`
	if err := os.WriteFile(filepath.Join(pkg, "y.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
}

func TestRepoImportingBBoltIsFlagged(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "repo", "z")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package z
import _ "go.etcd.io/bbolt"
`
	if err := os.WriteFile(filepath.Join(pkg, "z.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].RuleID != "repo-no-bbolt" {
		t.Fatalf("RuleID = %s", findings[0].RuleID)
	}
}

func TestPrometheusOutsideRuntimeMetricsIsFlagged(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "service", "p")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package p
import _ "github.com/prometheus/client_golang/prometheus"
`
	if err := os.WriteFile(filepath.Join(pkg, "p.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	// service/p imports prometheus AND go-ethereum-related forbidden, so
	// only the prometheus rule fires here. service-no-eth doesn't trigger
	// since the import isn't go-ethereum.
	hasPrometheusRule := false
	for _, f := range findings {
		if f.RuleID == "prometheus-only-in-runtime-metrics-or-cmd" {
			hasPrometheusRule = true
		}
	}
	if !hasPrometheusRule {
		t.Fatalf("expected prometheus rule violation, got: %+v", findings)
	}
}

func TestPrometheusInRuntimeMetricsAllowed(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "runtime", "metrics")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package metrics
import _ "github.com/prometheus/client_golang/prometheus"
`
	if err := os.WriteFile(filepath.Join(pkg, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got: %+v", findings)
	}
}

func TestTypesImportingInternalIsFlagged(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "types")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package types
import _ "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/config"
`
	if err := os.WriteFile(filepath.Join(pkg, "t.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "types-no-internal" {
		t.Fatal("wrong rule id")
	}
}

func TestTestFilesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "service", "z")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package z
import _ "github.com/ethereum/go-ethereum/common"
`
	if err := os.WriteFile(filepath.Join(pkg, "z_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	findings, _ := CheckRepo(dir)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings on test file, got %d", len(findings))
	}
}

func TestRunIntegration(t *testing.T) {
	// Use a temp dir with one violation; Run should return 1.
	dir := t.TempDir()
	pkg := filepath.Join(dir, "internal", "service", "x")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package x
import _ "github.com/ethereum/go-ethereum/common"
`
	if err := os.WriteFile(filepath.Join(pkg, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	code := Run(dir, &sb)
	if code != 1 {
		t.Fatalf("Run returned %d; want 1", code)
	}
}

func TestRunCleanIntegration(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	code := Run(dir, &sb)
	if code != 0 {
		t.Fatalf("Run returned %d on empty repo; want 0", code)
	}
}
