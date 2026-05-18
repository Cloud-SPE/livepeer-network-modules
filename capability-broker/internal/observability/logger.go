package observability

import (
	"log/slog"
	"os"
	"strings"
)

// SetupLogger installs a JSON-handler slog.Logger as the package and process
// default. Logs are emitted to stdout for container-friendly log scraping.
//
// Call once from main(); subsequent slog.Info/Warn/Error calls flow through
// this handler.
func SetupLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(l)
	return l
}
