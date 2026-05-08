// Package logger holds the Logger provider — a thin slog wrapper so
// service code emits structured logs without each handler taking a
// raw *slog.Logger and learning slog conventions. The interface is
// minimal on purpose: 4 levels, key-value pairs.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// Logger is the structured logger surface used by service/ and repo/.
type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)

	// With returns a child logger with kv pairs included on every line.
	With(kv ...any) Logger
}

// Slog wraps slog.Logger.
type Slog struct {
	l *slog.Logger
}

// Config picks how the logger emits.
type Config struct {
	Level  string // debug|info|warn|error
	Format string // text|json
	// W is the io.Writer; nil means os.Stderr.
	W io.Writer
}

// New constructs a Slog logger with the given config.
func New(cfg Config) *Slog {
	w := cfg.W
	if w == nil {
		w = os.Stderr
	}
	level := levelFromString(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if cfg.Format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return &Slog{l: slog.New(h)}
}

// Discard returns a logger that drops everything; used in tests.
func Discard() *Slog {
	return &Slog{l: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))}
}

func (s *Slog) Debug(msg string, kv ...any) {
	s.l.LogAttrs(context.Background(), slog.LevelDebug, msg, attrs(kv)...)
}
func (s *Slog) Info(msg string, kv ...any) {
	s.l.LogAttrs(context.Background(), slog.LevelInfo, msg, attrs(kv)...)
}
func (s *Slog) Warn(msg string, kv ...any) {
	s.l.LogAttrs(context.Background(), slog.LevelWarn, msg, attrs(kv)...)
}
func (s *Slog) Error(msg string, kv ...any) {
	s.l.LogAttrs(context.Background(), slog.LevelError, msg, attrs(kv)...)
}

func (s *Slog) With(kv ...any) Logger {
	if len(kv) == 0 {
		return s
	}
	return &Slog{l: s.l.With(kv...)}
}

func levelFromString(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// attrs converts key-value pairs to slog.Attrs. Odd-length kv lists
// silently drop the trailing key (logger should never panic on bad
// input — it would mask the error the caller was trying to log).
func attrs(kv []any) []slog.Attr {
	if len(kv) == 0 {
		return nil
	}
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		out = append(out, slog.Any(key, kv[i+1]))
	}
	return out
}
