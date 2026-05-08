// Package logger provides a structured-logging abstraction.
//
// Production code uses Slog(io.Writer); tests use FakeLogger for
// capture-and-assert. lint/no-secrets-in-logs (per module) flags Field
// values that look like keys, tickets, or secrets.
package logger

import (
	"io"
	"log/slog"
)

// Logger is the structured-logging interface used by chain-commons services.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	With(fields ...Field) Logger
}

// Field is a structured log field. Slog wraps these as slog.Attrs.
type Field struct {
	Key   string
	Value any
}

// String constructs a string-valued Field.
func String(key, value string) Field { return Field{Key: key, Value: value} }

// Int constructs an int-valued Field.
func Int(key string, value int) Field { return Field{Key: key, Value: value} }

// Uint64 constructs a uint64-valued Field.
func Uint64(key string, value uint64) Field { return Field{Key: key, Value: value} }

// Err constructs an error-valued Field with key "err".
func Err(value error) Field { return Field{Key: "err", Value: value} }

// Any constructs a Field with an arbitrary value.
func Any(key string, value any) Field { return Field{Key: key, Value: value} }

// Slog returns a Logger backed by a stdlib log/slog text handler writing
// to w. Level is set via opts (slog.HandlerOptions).
func Slog(w io.Writer, opts *slog.HandlerOptions) Logger {
	return &slogLogger{l: slog.New(slog.NewTextHandler(w, opts))}
}

// SlogJSON returns a Logger backed by a stdlib log/slog JSON handler.
func SlogJSON(w io.Writer, opts *slog.HandlerOptions) Logger {
	return &slogLogger{l: slog.New(slog.NewJSONHandler(w, opts))}
}

type slogLogger struct{ l *slog.Logger }

func (s *slogLogger) toAttrs(fields []Field) []any {
	a := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		a = append(a, f.Key, f.Value)
	}
	return a
}

func (s *slogLogger) Debug(msg string, fields ...Field) { s.l.Debug(msg, s.toAttrs(fields)...) }
func (s *slogLogger) Info(msg string, fields ...Field)  { s.l.Info(msg, s.toAttrs(fields)...) }
func (s *slogLogger) Warn(msg string, fields ...Field)  { s.l.Warn(msg, s.toAttrs(fields)...) }
func (s *slogLogger) Error(msg string, fields ...Field) { s.l.Error(msg, s.toAttrs(fields)...) }

func (s *slogLogger) With(fields ...Field) Logger {
	return &slogLogger{l: s.l.With(s.toAttrs(fields)...)}
}
