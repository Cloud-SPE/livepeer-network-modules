package testdata

import "log/slog"

// Intentionally benign — keys are all fine.
func good(l *slog.Logger) {
	l.Info("event", "sender", "0xabc", "tx_hash", "0xdef", "nonce", 42)
}
