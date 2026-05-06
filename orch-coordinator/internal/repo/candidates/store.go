// Package candidates is the filesystem-backed candidate-history
// store. Each Save() writes a timestamped subdirectory under the data
// dir's candidates/ root holding manifest.json and metadata.json
// alongside a packed candidate.tar.gz. Old snapshots are pruned by
// count.
//
// Plan 0018 §9: snapshots live under
// `<data-dir>/candidates/<timestamp>/` with default keep-count 50.
package candidates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultKeep is the number of candidate snapshots retained on disk
// when no override is configured.
const DefaultKeep = 50

// Store wraps the candidates/ directory.
type Store struct {
	dir  string
	keep int
	mu   sync.Mutex
}

// New opens (or creates) the directory. If keep is non-positive,
// DefaultKeep is used.
func New(dir string, keep int) (*Store, error) {
	if dir == "" {
		return nil, errors.New("candidates: dir is required")
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("candidates: mkdir %s: %w", dir, err)
	}
	return &Store{dir: dir, keep: keep}, nil
}

// Snapshot is the byte payload the builder asks the store to persist.
// All members are stored verbatim; the manifest.json bytes are the
// JCS-canonical signed-bytes the cold key will sign over, and the
// tarball is the operator-download artifact.
type Snapshot struct {
	Timestamp     time.Time
	ManifestBytes []byte
	MetadataBytes []byte
	TarballBytes  []byte
}

// Save writes the snapshot to a fresh timestamped subdirectory and
// prunes old entries. Returns the absolute path to the new snapshot
// directory.
func (s *Store) Save(snap Snapshot) (string, error) {
	if len(snap.ManifestBytes) == 0 {
		return "", errors.New("candidates: empty manifest bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	t := snap.Timestamp
	if t.IsZero() {
		t = time.Now().UTC()
	}
	stamp := t.UTC().Format("20060102T150405.000000Z")
	stamp = strings.ReplaceAll(stamp, ".", "_")
	target := filepath.Join(s.dir, stamp)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("candidates: mkdir %s: %w", target, err)
	}
	if err := writeFileAtomic(filepath.Join(target, "manifest.json"), snap.ManifestBytes); err != nil {
		return "", err
	}
	if len(snap.MetadataBytes) > 0 {
		if err := writeFileAtomic(filepath.Join(target, "metadata.json"), snap.MetadataBytes); err != nil {
			return "", err
		}
	}
	if len(snap.TarballBytes) > 0 {
		if err := writeFileAtomic(filepath.Join(target, "candidate.tar.gz"), snap.TarballBytes); err != nil {
			return "", err
		}
	}
	if err := s.prune(); err != nil {
		return target, fmt.Errorf("candidates: prune: %w", err)
	}
	return target, nil
}

// List returns the directory names (no path prefix) sorted oldest
// first.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("candidates: readdir %s: %w", s.dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Latest returns the path to the most-recent snapshot, or empty if
// none exist.
func (s *Store) Latest() (string, error) {
	names, err := s.List()
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", nil
	}
	return filepath.Join(s.dir, names[len(names)-1]), nil
}

// Tarball returns the packed candidate.tar.gz bytes for the named
// snapshot.
func (s *Store) Tarball(name string) ([]byte, error) {
	p := filepath.Join(s.dir, filepath.Clean(name), "candidate.tar.gz")
	return os.ReadFile(p)
}

// LatestTarball returns the packed bytes of the most-recent snapshot.
func (s *Store) LatestTarball() ([]byte, error) {
	names, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, os.ErrNotExist
	}
	return s.Tarball(names[len(names)-1])
}

// LatestManifest returns the JCS-canonical manifest.json bytes from
// the most-recent snapshot.
func (s *Store) LatestManifest() ([]byte, error) {
	names, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(s.dir, names[len(names)-1], "manifest.json"))
}

func (s *Store) prune() error {
	names, err := s.List()
	if err != nil {
		return err
	}
	if len(names) <= s.keep {
		return nil
	}
	excess := len(names) - s.keep
	for i := 0; i < excess; i++ {
		if err := os.RemoveAll(filepath.Join(s.dir, names[i])); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tempfile in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tempfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename tempfile to %s: %w", path, err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// PruneTimestamp returns the UTC time encoded in a snapshot dir name,
// or zero if the name is unparseable.
func PruneTimestamp(name string) time.Time {
	clean := strings.ReplaceAll(name, "_", ".")
	t, err := time.Parse("20060102T150405.000000Z", clean)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
