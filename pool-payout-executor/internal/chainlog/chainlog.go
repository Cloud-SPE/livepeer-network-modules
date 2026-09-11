// Package chainlog adapts the executor's slog logger to chain-commons's
// logger.Logger so the multi-RPC transport's failover and circuit
// events land in the same log stream as everything else.
package chainlog

import (
	"log/slog"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
)

type slogAdapter struct{ l *slog.Logger }

// New wraps l. A nil l uses slog.Default().
func New(l *slog.Logger) logger.Logger {
	if l == nil {
		l = slog.Default()
	}
	return &slogAdapter{l: l}
}

func toAttrs(fields []logger.Field) []any {
	a := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		a = append(a, f.Key, f.Value)
	}
	return a
}

func (s *slogAdapter) Debug(msg string, fields ...logger.Field) { s.l.Debug(msg, toAttrs(fields)...) }
func (s *slogAdapter) Info(msg string, fields ...logger.Field)  { s.l.Info(msg, toAttrs(fields)...) }
func (s *slogAdapter) Warn(msg string, fields ...logger.Field)  { s.l.Warn(msg, toAttrs(fields)...) }
func (s *slogAdapter) Error(msg string, fields ...logger.Field) { s.l.Error(msg, toAttrs(fields)...) }
func (s *slogAdapter) With(fields ...logger.Field) logger.Logger {
	return &slogAdapter{l: s.l.With(toAttrs(fields)...)}
}
