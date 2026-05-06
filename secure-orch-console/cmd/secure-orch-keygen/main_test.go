package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_RejectsMissingFlags(t *testing.T) {
	cases := [][]string{
		{},
		{"--out=/tmp/x"},
		{"--password-file=/tmp/x"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := run(args, &bytes.Buffer{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRun_RejectsShortPassword(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	os.WriteFile(pw, []byte("short"), 0o600)
	out := filepath.Join(dir, "ks.json")
	err := run([]string{"--out=" + out, "--password-file=" + pw}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "12") {
		t.Fatalf("expected length error, got %v", err)
	}
}

func TestRun_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	os.WriteFile(pw, []byte("longenoughpassword"), 0o600)
	out := filepath.Join(dir, "ks.json")
	os.WriteFile(out, []byte("existing"), 0o600)
	err := run([]string{"--out=" + out, "--password-file=" + pw}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("expected exists error, got %v", err)
	}
}

func TestRun_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("StandardScryptN takes ~1-2s; skipping in -short")
	}
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	os.WriteFile(pw, []byte("longenoughpassword"), 0o600)
	out := filepath.Join(dir, "ks.json")
	buf := &bytes.Buffer{}
	if err := run([]string{"--out=" + out, "--password-file=" + pw}, buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "address: 0x") {
		t.Fatalf("missing address line: %s", buf.String())
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("keystore mode %o", st.Mode().Perm())
	}
}
