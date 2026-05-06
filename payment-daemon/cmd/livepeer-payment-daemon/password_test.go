package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPasswordFromFile(t *testing.T) {
	t.Setenv(passwordEnvVar, "") // ensure env is empty
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("hunter2"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadPassword(path)
	if err != nil {
		t.Fatalf("loadPassword: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("got %q, want hunter2", got)
	}
}

func TestLoadPasswordFromFileTrimsNewline(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadPassword(path)
	if err != nil {
		t.Fatalf("loadPassword: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("got %q, want hunter2 (newline should be trimmed)", got)
	}
}

func TestLoadPasswordFromFileTrimsCRLF(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("hunter2\r\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadPassword(path)
	if err != nil {
		t.Fatalf("loadPassword: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("got %q, want hunter2 (CRLF should be trimmed)", got)
	}
}

func TestLoadPasswordFromEnv(t *testing.T) {
	t.Setenv(passwordEnvVar, "env-secret")
	got, err := loadPassword("")
	if err != nil {
		t.Fatalf("loadPassword: %v", err)
	}
	if got != "env-secret" {
		t.Errorf("got %q, want env-secret", got)
	}
}

func TestLoadPasswordBothSetRejected(t *testing.T) {
	t.Setenv(passwordEnvVar, "env")
	_, err := loadPassword("/tmp/never-opened")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want mutual-exclusion error, got %v", err)
	}
}

func TestLoadPasswordNeitherSetRejected(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	_, err := loadPassword("")
	if err == nil || !strings.Contains(err.Error(), "password required") {
		t.Fatalf("want required error, got %v", err)
	}
}

func TestLoadPasswordMissingFile(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	_, err := loadPassword(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "read password file") {
		t.Fatalf("want read error, got %v", err)
	}
}

func TestLoadPasswordEmptyFileRejected(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadPassword(path)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-file error, got %v", err)
	}
}

func TestLoadPasswordFileOnlyNewline(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadPassword(path)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-file error for newline-only content, got %v", err)
	}
}

func TestZeroBytesScrubs(t *testing.T) {
	b := []byte("password-bytes")
	zeroBytes(b)
	for i, v := range b {
		if v != 0 {
			t.Errorf("byte %d not zeroed: %x", i, v)
		}
	}
}
