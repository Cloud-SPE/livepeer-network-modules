// Package inbox is the spool-dir reader for incoming candidate
// manifests. The console never opens a network port; it reads from
// here. USB auto-detect (commit 7) populates this directory; today
// the operator drops a candidate.json by hand.
package inbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Candidate is one inbound candidate manifest envelope. The bytes
// field is the raw file content, preserved verbatim so canonical-
// bytes hashing matches what the operator dropped.
type Candidate struct {
	Path  string
	Bytes []byte
}

// Inbox reads candidates from a spool directory.
type Inbox struct {
	dir string
}

// New returns an Inbox rooted at dir. The directory must exist.
func New(dir string) (*Inbox, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("inbox: stat %s: %w", dir, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("inbox: %s is not a directory", dir)
	}
	return &Inbox{dir: dir}, nil
}

// Dir returns the inbox root.
func (i *Inbox) Dir() string { return i.dir }

// List returns the absolute paths of all *.json files in the inbox,
// sorted lexicographically. Hidden files and subdirectories are
// skipped.
func (i *Inbox) List() ([]string, error) {
	entries, err := os.ReadDir(i.dir)
	if err != nil {
		return nil, fmt.Errorf("inbox: read %s: %w", i.dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || name[0] == '.' {
			continue
		}
		if filepath.Ext(name) != ".json" {
			continue
		}
		out = append(out, filepath.Join(i.dir, name))
	}
	sort.Strings(out)
	return out, nil
}

// Load reads a candidate by absolute path, validating that it lives
// inside the inbox directory (no path traversal). The returned
// Candidate.Bytes is the raw file content.
func (i *Inbox) Load(path string) (*Candidate, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("inbox: abs %s: %w", path, err)
	}
	dirAbs, err := filepath.Abs(i.dir)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(dirAbs, abs)
	if err != nil || rel == "" || rel == ".." || filepath.IsAbs(rel) || rel[0] == '.' || filepath.Dir(rel) != "." {
		return nil, fmt.Errorf("inbox: %s is outside inbox dir", path)
	}
	b, err := os.ReadFile(abs) //nolint:gosec // path validated above
	if err != nil {
		return nil, fmt.Errorf("inbox: read %s: %w", abs, err)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("inbox: %s is not valid JSON", abs)
	}
	return &Candidate{Path: abs, Bytes: b}, nil
}

// Remove deletes a loaded candidate after the operator has signed it.
// Returns nil if the file is already gone.
func (i *Inbox) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inbox: remove %s: %w", path, err)
	}
	return nil
}
