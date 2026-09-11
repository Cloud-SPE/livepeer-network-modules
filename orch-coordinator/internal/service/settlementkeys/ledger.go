package settlementkeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Windower assigns a validity window to a key the broker announced
// without one. It has to be stable across scrapes — a window that
// moved every cycle would defeat the candidate debounce and the cold
// key would never see the same bytes twice — so it remembers when a
// key was first seen.
type Windower interface {
	Window(publicKey string, now time.Time, validity time.Duration) (notBefore, expiresAt time.Time)
}

// Ledger is the file-backed Windower: public_key → first seen. Lives in
// the coordinator's data dir next to the candidate store.
type Ledger struct {
	mu   sync.Mutex
	path string
	seen map[string]time.Time
}

// OpenLedger loads the ledger at path, creating it on first use.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path, seen: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return l, nil
	case err != nil:
		return nil, fmt.Errorf("settlement key ledger: %w", err)
	}
	var file ledgerFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("settlement key ledger %s: %w", path, err)
	}
	for _, e := range file.Keys {
		l.seen[e.PublicKey] = e.FirstSeen.UTC()
	}
	return l, nil
}

type ledgerFile struct {
	Keys []ledgerEntry `json:"keys"`
}

type ledgerEntry struct {
	PublicKey string    `json:"public_key"`
	FirstSeen time.Time `json:"first_seen"`
}

// Window returns [first_seen, first_seen + validity). When two thirds
// of the window has elapsed the key is re-anchored at now, which
// produces a new window the cold key reviews — a delegation renews by
// an ordinary sign cycle, never silently and never by lapsing.
func (l *Ledger) Window(publicKey string, now time.Time, validity time.Duration) (time.Time, time.Time) {
	now = now.UTC().Truncate(time.Second)
	l.mu.Lock()
	defer l.mu.Unlock()
	first, ok := l.seen[publicKey]
	if !ok || !now.Before(first.Add(validity*2/3)) {
		first = now
		l.seen[publicKey] = first
		if err := l.persistLocked(); err != nil {
			// Losing the file means a window may move on restart; that
			// is a review, not a breach. Nothing better to do than say so.
			fmt.Fprintf(os.Stderr, "settlement key ledger: %v\n", err)
		}
	}
	return first, first.Add(validity)
}

func (l *Ledger) persistLocked() error {
	file := ledgerFile{Keys: make([]ledgerEntry, 0, len(l.seen))}
	for k, t := range l.seen {
		file.Keys = append(file.Keys, ledgerEntry{PublicKey: k, FirstSeen: t})
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// MemoryWindower is a Windower for tests and dev mode: same semantics,
// nothing on disk.
type MemoryWindower struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// Window implements Windower.
func (m *MemoryWindower) Window(publicKey string, now time.Time, validity time.Duration) (time.Time, time.Time) {
	now = now.UTC().Truncate(time.Second)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	first, ok := m.seen[publicKey]
	if !ok || !now.Before(first.Add(validity*2/3)) {
		first = now
		m.seen[publicKey] = first
	}
	return first, first.Add(validity)
}
