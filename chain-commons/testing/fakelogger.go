package chaintesting

import (
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
)

// LogLevel labels each captured entry.
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// LogEntry captures a single emission.
type LogEntry struct {
	Level  LogLevel
	Msg    string
	Fields []logger.Field
}

// FakeLogger is a Logger that records every emission for test assertion.
type FakeLogger struct {
	mu      sync.Mutex
	entries []LogEntry
	with    []logger.Field
}

// NewFakeLogger returns an empty FakeLogger.
func NewFakeLogger() *FakeLogger { return &FakeLogger{} }

// Debug captures a debug-level entry.
func (l *FakeLogger) Debug(msg string, fields ...logger.Field) {
	l.append(LevelDebug, msg, fields)
}

// Info captures an info-level entry.
func (l *FakeLogger) Info(msg string, fields ...logger.Field) {
	l.append(LevelInfo, msg, fields)
}

// Warn captures a warn-level entry.
func (l *FakeLogger) Warn(msg string, fields ...logger.Field) {
	l.append(LevelWarn, msg, fields)
}

// Error captures an error-level entry.
func (l *FakeLogger) Error(msg string, fields ...logger.Field) {
	l.append(LevelError, msg, fields)
}

// With returns a child FakeLogger that prepends the given fields to every
// entry. Captures share the same underlying buffer.
func (l *FakeLogger) With(fields ...logger.Field) logger.Logger {
	child := &FakeLogger{}
	child.mu.Lock()
	defer child.mu.Unlock()
	// Share buffer with parent.
	child.entries = nil // will write through append helper
	child.with = append(append([]logger.Field(nil), l.with...), fields...)

	// Wire so child's entries land in parent's buffer.
	return &fakeLoggerChild{parent: l, with: child.with}
}

func (l *FakeLogger) append(level LogLevel, msg string, fields []logger.Field) {
	merged := append(append([]logger.Field(nil), l.with...), fields...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, LogEntry{Level: level, Msg: msg, Fields: merged})
}

// Entries returns a copy of the captured entries.
func (l *FakeLogger) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Reset clears captured entries. Does not affect With'd children — they
// continue to write into the parent buffer if it later receives entries.
func (l *FakeLogger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
}

// EntriesByLevel filters captured entries to a specific level.
func (l *FakeLogger) EntriesByLevel(level LogLevel) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LogEntry
	for _, e := range l.entries {
		if e.Level == level {
			out = append(out, e)
		}
	}
	return out
}

// fakeLoggerChild is the result of FakeLogger.With(...). Forwards to the
// parent's buffer with merged fields.
type fakeLoggerChild struct {
	parent *FakeLogger
	with   []logger.Field
}

func (c *fakeLoggerChild) Debug(msg string, fields ...logger.Field) {
	c.parent.append(LevelDebug, msg, append(append([]logger.Field(nil), c.with...), fields...))
}
func (c *fakeLoggerChild) Info(msg string, fields ...logger.Field) {
	c.parent.append(LevelInfo, msg, append(append([]logger.Field(nil), c.with...), fields...))
}
func (c *fakeLoggerChild) Warn(msg string, fields ...logger.Field) {
	c.parent.append(LevelWarn, msg, append(append([]logger.Field(nil), c.with...), fields...))
}
func (c *fakeLoggerChild) Error(msg string, fields ...logger.Field) {
	c.parent.append(LevelError, msg, append(append([]logger.Field(nil), c.with...), fields...))
}
func (c *fakeLoggerChild) With(fields ...logger.Field) logger.Logger {
	return &fakeLoggerChild{
		parent: c.parent,
		with:   append(append([]logger.Field(nil), c.with...), fields...),
	}
}

// Compile-time: FakeLogger satisfies logger.Logger.
var _ logger.Logger = (*FakeLogger)(nil)
var _ logger.Logger = (*fakeLoggerChild)(nil)
