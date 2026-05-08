package chaintesting

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
)

func TestFakeLogger_ChildAllLevels(t *testing.T) {
	parent := NewFakeLogger()
	child := parent.With(logger.String("module", "test"))
	child.Debug("d")
	child.Info("i")
	child.Warn("w")
	child.Error("e")

	es := parent.Entries()
	if len(es) != 4 {
		t.Fatalf("Entries = %d, want 4", len(es))
	}
	for _, e := range es {
		if len(e.Fields) == 0 || e.Fields[0].Key != "module" {
			t.Errorf("child entry missing module field: %+v", e.Fields)
		}
	}
}

func TestFakeLogger_GrandchildAccumulatesFields(t *testing.T) {
	parent := NewFakeLogger()
	child := parent.With(logger.String("module", "txintent"))
	grandchild := child.With(logger.String("subsystem", "settle"))
	grandchild.Info("evt", logger.String("k", "v"))

	got := parent.Entries()[0]
	if len(got.Fields) != 3 {
		t.Fatalf("expected 3 merged fields, got %d", len(got.Fields))
	}
	keys := []string{got.Fields[0].Key, got.Fields[1].Key, got.Fields[2].Key}
	want := []string{"module", "subsystem", "k"}
	for i, w := range want {
		if keys[i] != w {
			t.Errorf("merged field [%d] key = %q, want %q", i, keys[i], w)
		}
	}
}
