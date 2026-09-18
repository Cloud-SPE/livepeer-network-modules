package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentLinksPreserveHistoryButCheckExamples(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"README.md":                             "[current](docs/design-docs/current.md)",
		"docs/design-docs/current.md":           "[reference](../references/archived/old.md)",
		"docs/references/archived/old.md":       "[old location](missing.md)",
		"docs/exec-plans/completed/0001-old.md": "[old location](missing.md)",
		"examples/demo/README.md":               "[broken example](missing.md)",
		"lint/README.md":                        "[missing linter](missing.md)",
	}
	for path, body := range files {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	problems := checkCurrentLinks(root)
	if len(problems) != 2 {
		t.Fatalf("want only current broken links, got %v", problems)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "examples/demo/README.md") || !strings.Contains(joined, "lint/README.md") {
		t.Fatalf("missed current docs: %v", problems)
	}
}
