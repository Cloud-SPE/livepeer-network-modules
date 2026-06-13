// Package lastsigned owns the last-signed.json envelope file — the
// console's single source of truth for "what the cold key last
// signed". Shared by the web sign flow and the plan 0042 agent loop;
// the write is atomic (temp + fsync + rename) so a crash mid-write
// never corrupts the reference point the differ and the agent's
// crash recovery both depend on.
package lastsigned

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Load reads the envelope. A missing file is (nil, nil) — the first
// sign cycle has no reference point, which is a state, not an error.
func Load(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("load last-signed: %w", err)
	}
	return b, nil
}

// WriteAtomic persists the envelope via temp-file + fsync + rename.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".last-signed-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
