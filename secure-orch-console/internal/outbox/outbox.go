// Package outbox writes signed manifest envelopes and atomically
// updates last-signed.json.
package outbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Outbox writes signed manifests to a spool directory and maintains
// the last-signed.json copy used by the diff renderer.
type Outbox struct {
	dir            string
	lastSignedPath string
}

// New returns an Outbox rooted at dir, with last-signed.json kept at
// lastSignedPath. The dir and parent of lastSignedPath are created
// 0700 if missing.
func New(dir, lastSignedPath string) (*Outbox, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("outbox: mkdir %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Dir(lastSignedPath), 0o700); err != nil {
		return nil, fmt.Errorf("outbox: mkdir parent %s: %w", lastSignedPath, err)
	}
	return &Outbox{dir: dir, lastSignedPath: lastSignedPath}, nil
}

// Dir returns the outbox root.
func (o *Outbox) Dir() string { return o.dir }

// LastSignedPath returns the path the last-signed envelope is kept at.
func (o *Outbox) LastSignedPath() string { return o.lastSignedPath }

// LoadLastSigned reads last-signed.json. Returns nil + nil if the file
// does not yet exist (first sign cycle).
func (o *Outbox) LoadLastSigned() ([]byte, error) {
	b, err := os.ReadFile(o.lastSignedPath) //nolint:gosec // path is operator-supplied
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("outbox: read last-signed: %w", err)
	}
	return b, nil
}

// Write writes envelope to <dir>/<name> AND atomically replaces the
// last-signed.json file. Both writes happen 0600. The outbox file is
// written via a tempfile + rename so concurrent readers never see a
// half-written envelope.
func (o *Outbox) Write(name string, envelope []byte) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("outbox: bad filename %q", name)
	}
	dst := filepath.Join(o.dir, name)
	if err := atomicWrite(dst, envelope); err != nil {
		return "", fmt.Errorf("outbox: write %s: %w", dst, err)
	}
	if err := atomicWrite(o.lastSignedPath, envelope); err != nil {
		return dst, fmt.Errorf("outbox: write last-signed: %w", err)
	}
	return dst, nil
}

// atomicWrite creates a temp file in the destination's directory and
// renames it onto dst. This is atomic on the same filesystem.
func atomicWrite(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".outbox-*")
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
	if err := os.Rename(tmpName, dst); err != nil {
		cleanup()
		return err
	}
	return nil
}
