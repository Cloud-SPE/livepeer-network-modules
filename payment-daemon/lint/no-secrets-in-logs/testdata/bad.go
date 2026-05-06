package testdata

import "log/slog"

// Intentionally bad — the analyzer must flag this. Excluded from the
// build via the testdata/ directory convention so it doesn't leak into
// the production binary.
func bad(l *slog.Logger) {
	l.Info("login", "password", "hunter2")
}
