package inbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	dir := t.TempDir()
	must := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("a.json", "{}")
	must("b.json", "{}")
	must("c.txt", "ignore")
	must(".hidden.json", "{}")

	in, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := in.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
	for _, p := range got {
		if !strings.HasSuffix(p, ".json") {
			t.Fatalf("non-json: %s", p)
		}
	}
}

func TestLoadValidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := New(dir)
	c, err := in.Load(filepath.Join(dir, "x.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Bytes) != `{"ok":true}` {
		t.Fatalf("got %s", c.Bytes)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.json"), []byte("not json"), 0o600)
	in, _ := New(dir)
	if _, err := in.Load(filepath.Join(dir, "x.json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	innerDir := filepath.Join(root, "inbox")
	siblingDir := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(innerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(siblingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(siblingDir, "evil.json")
	if err := os.WriteFile(bad, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := New(innerDir)
	if _, err := in.Load(bad); err == nil {
		t.Fatal("expected path-traversal rejection")
	}
}

func TestRemoveIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	os.WriteFile(p, []byte("{}"), 0o600)
	in, _ := New(dir)
	if err := in.Remove(p); err != nil {
		t.Fatal(err)
	}
	// second remove should not error.
	if err := in.Remove(p); err != nil {
		t.Fatal(err)
	}
}
