package chaintesting

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
)

func TestFakeLogger_CapturesAtAllLevels(t *testing.T) {
	l := NewFakeLogger()
	l.Debug("d-msg", logger.String("k", "1"))
	l.Info("i-msg")
	l.Warn("w-msg")
	l.Error("e-msg")

	es := l.Entries()
	if len(es) != 4 {
		t.Fatalf("Entries = %d, want 4", len(es))
	}
	wantLevels := []LogLevel{LevelDebug, LevelInfo, LevelWarn, LevelError}
	for i, want := range wantLevels {
		if es[i].Level != want {
			t.Errorf("entry %d level = %v, want %v", i, es[i].Level, want)
		}
	}
	if es[0].Fields[0].Key != "k" {
		t.Errorf("first entry should carry k=1 field")
	}
}

func TestFakeLogger_EntriesByLevel(t *testing.T) {
	l := NewFakeLogger()
	l.Info("a")
	l.Warn("b")
	l.Info("c")
	if got := len(l.EntriesByLevel(LevelInfo)); got != 2 {
		t.Errorf("info entries = %d, want 2", got)
	}
}

func TestFakeLogger_With(t *testing.T) {
	parent := NewFakeLogger()
	child := parent.With(logger.String("module", "txintent"))
	child.Info("evt")
	if len(parent.Entries()) != 1 {
		t.Errorf("With'd child should write to parent buffer")
	}
	got := parent.Entries()[0]
	if got.Fields[0].Key != "module" || got.Fields[0].Value.(string) != "txintent" {
		t.Errorf("child entry missing With field: %+v", got)
	}
}

func TestFakeLogger_Reset(t *testing.T) {
	l := NewFakeLogger()
	l.Info("a")
	l.Reset()
	if len(l.Entries()) != 0 {
		t.Errorf("Reset should clear buffer")
	}
}
