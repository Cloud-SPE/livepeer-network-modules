package chainlog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
)

func TestAdapterForwardsLevelsAndFields(t *testing.T) {
	var buf bytes.Buffer
	l := New(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	l = l.With(logger.String("component", "rpc"))
	l.Debug("d", logger.Int("n", 1))
	l.Info("i", logger.Uint64("u", 2))
	l.Warn("w", logger.Any("a", "x"))
	l.Error("e", logger.Err(errTest))
	out := buf.String()
	for _, want := range []string{"level=DEBUG", "level=INFO", "level=WARN", "level=ERROR", "component=rpc", "n=1", "u=2", "a=x", "err=boom"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestNewNilUsesDefault(t *testing.T) {
	if New(nil) == nil {
		t.Fatal("New(nil) returned nil")
	}
}

type testErr struct{}

func (testErr) Error() string { return "boom" }

var errTest error = testErr{}
