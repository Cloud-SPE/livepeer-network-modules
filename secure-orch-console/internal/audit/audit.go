// Package audit holds the rolling JSONL audit log. Every console
// gesture (load_candidate, view_diff, sign, write_outbox, abort)
// emits one record. Storage shape mirrors the prior reference impl's
// audit/ package; the file is append-only, with size-based rotation
// landing in commit 7.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kind enumerates the audit-event categories. Keep this list small and
// stable; new kinds bump no version, but breaking renames do.
type Kind string

const (
	KindLoadCandidate Kind = "load_candidate"
	KindViewDiff      Kind = "view_diff"
	KindSign          Kind = "sign"
	KindWriteOutbox   Kind = "write_outbox"
	KindAbort         Kind = "abort"
	KindBoot          Kind = "boot"
	KindShutdown      Kind = "shutdown"
)

// Event is one audit record. Required: At, Kind. Everything else is
// kind-specific.
type Event struct {
	At         time.Time      `json:"at"`
	Kind       Kind           `json:"kind"`
	EthAddress string         `json:"eth_address,omitempty"`
	CanonHash  string         `json:"canonical_sha256,omitempty"`
	Seq        *uint64        `json:"publication_seq,omitempty"`
	Note       string         `json:"note,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
}

// ErrLogClosed is returned by Append after Close.
var ErrLogClosed = errors.New("audit: log closed")

// Log appends events to a JSONL file. Concurrent Append calls are
// serialized with a mutex; only one writer per file.
type Log struct {
	mu     sync.Mutex
	w      io.WriteCloser
	path   string
	closed bool
}

// Open opens (or creates) the JSONL audit log at path. The parent
// directory must exist. The file is opened for append in 0600 mode.
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit: mkdir parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open: %w", err)
	}
	return &Log{w: f, path: path}, nil
}

// Append serializes e as one JSON line and flushes to disk.
func (l *Log) Append(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrLogClosed
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if e.Kind == "" {
		return errors.New("audit: missing kind")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	b = append(b, '\n')
	if _, err := l.w.Write(b); err != nil {
		return fmt.Errorf("audit: write: %w", err)
	}
	if f, ok := l.w.(*os.File); ok {
		if err := f.Sync(); err != nil {
			return fmt.Errorf("audit: sync: %w", err)
		}
	}
	return nil
}

// Path returns the file path the log writes to.
func (l *Log) Path() string { return l.path }

// Close flushes and closes the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.w.Close()
}
