package observability

import (
	"log/slog"
	"os"
)

// SetupLogger installs a JSON-handler slog.Logger as the package and process
// default. Logs are emitted to stdout for container-friendly log scraping.
//
// Call once from main(); subsequent slog.Info/Warn/Error calls flow through
// this handler.
func SetupLogger() *slog.Logger {
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(l)
	return l
}
