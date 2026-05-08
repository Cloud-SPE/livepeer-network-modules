package testdata

import "log/slog"

// Intentionally bad — the analyzer must flag this.
func bad(l *slog.Logger) {
	l.Info("login", "password", "hunter2")
}
