// Package audit holds the rolling JSONL audit log. Every console
// gesture emits one record. Storage shape mirrors the prior reference
// impl's audit/ package; the file is append-only with size-based
// rotation.
package audit

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Kind enumerates the audit-event categories.
type Kind string

const (
	KindLoadCandidate  Kind = "load_candidate"
	KindViewDiff       Kind = "view_diff"
	KindSign           Kind = "sign"
	KindWriteSigned    Kind = "write_signed"
	KindProtocolAction Kind = "protocol_action"
	KindAbort          Kind = "abort"
	KindBoot           Kind = "boot"
	KindShutdown       Kind = "shutdown"
	KindRotate         Kind = "rotate"
)

// Event is one audit record. Required: At, Kind. Everything else is
// kind-specific.
type Event struct {
	At         time.Time      `json:"at"`
	Kind       Kind           `json:"kind"`
	Actor      string         `json:"actor,omitempty"`
	EthAddress string         `json:"eth_address,omitempty"`
	CanonHash  string         `json:"canonical_sha256,omitempty"`
	Seq        *uint64        `json:"publication_seq,omitempty"`
	Note       string         `json:"note,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
}

// ErrLogClosed is returned by Append after Close.
var ErrLogClosed = errors.New("audit: log closed")

// DefaultRotateSize is 100 MiB.
const DefaultRotateSize int64 = 100 << 20

// Log appends events to a JSONL file. Concurrent Append calls are
// serialized with a mutex. Size-based rotation runs inline before
// each write that would push the file past the configured threshold.
type Log struct {
	mu          sync.Mutex
	w           *os.File
	path        string
	rotateSize  int64
	currentSize int64
	closed      bool
}

type Page struct {
	Events     []Event
	NextCursor string
	HasOlder   bool
}

// Open opens (or creates) the JSONL audit log at path with the given
// rotate threshold. A non-positive rotateSize disables rotation.
func Open(path string, rotateSize int64) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit: mkdir parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("audit: stat: %w", err)
	}
	return &Log{
		w:           f,
		path:        path,
		rotateSize:  rotateSize,
		currentSize: st.Size(),
	}, nil
}

// Append serializes e as one JSON line and flushes to disk. The file
// is rotated first if the next write would push it past the rotate
// threshold.
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
	if l.rotateSize > 0 && l.currentSize+int64(len(b)) > l.rotateSize {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := l.w.Write(b)
	if err != nil {
		return fmt.Errorf("audit: write: %w", err)
	}
	l.currentSize += int64(n)
	if err := l.w.Sync(); err != nil {
		return fmt.Errorf("audit: sync: %w", err)
	}
	return nil
}

// Path returns the file path the log writes to.
func (l *Log) Path() string { return l.path }

// ReadRecent returns up to the last limit events from path in file
// order. A non-positive limit returns all events.
func ReadRecent(path string, limit int) ([]Event, error) {
	page, err := ReadPage(path, limit, "")
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

// ReadPage returns up to limit events in file order using a cursor that
// walks backward through the append-only file. An empty cursor returns the
// newest page. The cursor is opaque to callers.
func ReadPage(path string, limit int, cursor string) (Page, error) {
	f, err := os.Open(path)
	if err != nil {
		return Page{}, fmt.Errorf("audit: open for read: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return Page{}, fmt.Errorf("audit: unmarshal line: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return Page{}, fmt.Errorf("audit: scan: %w", err)
	}
	if limit <= 0 {
		limit = len(events)
	}
	end, err := decodeCursor(cursor, len(events))
	if err != nil {
		return Page{}, err
	}
	start := max(0, end-limit)
	page := Page{
		Events:   events[start:end],
		HasOlder: start > 0,
	}
	if page.HasOlder {
		page.NextCursor = encodeCursor(start)
	}
	return page, nil
}

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

func (l *Log) rotateLocked() error {
	if err := l.w.Sync(); err != nil {
		return fmt.Errorf("audit: sync before rotate: %w", err)
	}
	if err := l.w.Close(); err != nil {
		return fmt.Errorf("audit: close before rotate: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	rotated := l.path + "." + stamp
	if err := os.Rename(l.path, rotated); err != nil {
		return fmt.Errorf("audit: rename %s: %w", l.path, err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: reopen after rotate: %w", err)
	}
	l.w = f
	l.currentSize = 0
	marker, err := json.Marshal(Event{
		At:   time.Now().UTC(),
		Kind: KindRotate,
		Note: "rotated to " + filepath.Base(rotated),
	})
	if err != nil {
		return fmt.Errorf("audit: marshal rotate marker: %w", err)
	}
	marker = append(marker, '\n')
	n, err := l.w.Write(marker)
	if err != nil {
		return fmt.Errorf("audit: write rotate marker: %w", err)
	}
	l.currentSize += int64(n)
	return nil
}

func encodeCursor(end int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
}

func decodeCursor(cursor string, total int) (int, error) {
	if cursor == "" {
		return total, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("audit: decode cursor: %w", err)
	}
	end, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, fmt.Errorf("audit: parse cursor: %w", err)
	}
	if end < 0 || end > total {
		return 0, errors.New("audit: cursor out of range")
	}
	return end, nil
}
