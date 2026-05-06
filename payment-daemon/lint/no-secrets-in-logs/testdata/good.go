package testdata

import "log/slog"

// Intentionally benign — keys are all fine. Lives in testdata/ so it's
// excluded from production build but available to lint-driven tests.
func good(l *slog.Logger) {
	l.Info("event", "sender", "0xabc", "tx_hash", "0xdef", "nonce", 42)
}
