package runnerreport

import (
	"context"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	ext, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := ext.(*Extractor)
	if e.Unit() != "units" {
		t.Fatalf("default unit: got %q, want units", e.Unit())
	}
}

func TestNewWithUnit(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{"unit": "tokens"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := ext.(*Extractor)
	if e.Unit() != "tokens" {
		t.Fatalf("unit: got %q, want tokens", e.Unit())
	}
}

func TestNewRejectsNonStringUnit(t *testing.T) {
	t.Parallel()
	if _, err := New(map[string]any{"unit": 42}); err == nil {
		t.Fatal("expected error on int unit")
	}
}

func TestLiveCounterAccumulates(t *testing.T) {
	t.Parallel()
	ext, _ := New(nil)
	e := ext.(*Extractor)
	lc := e.NewLiveCounter()
	if lc.CurrentUnits() != 0 {
		t.Fatalf("zero state: got %d", lc.CurrentUnits())
	}
	lc.Add(5)
	lc.Add(7)
	if lc.CurrentUnits() != 12 {
		t.Fatalf("after Add: got %d, want 12", lc.CurrentUnits())
	}
}

func TestLiveCounterConcurrent(t *testing.T) {
	t.Parallel()
	ext, _ := New(nil)
	e := ext.(*Extractor)
	lc := e.NewLiveCounter()
	const goroutines = 16
	const each = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				lc.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := lc.CurrentUnits(); got != goroutines*each {
		t.Fatalf("CurrentUnits: got %d, want %d", got, goroutines*each)
	}
}

func TestExtractReturnsZeroWithoutCounter(t *testing.T) {
	t.Parallel()
	ext, _ := New(nil)
	got, err := ext.Extract(context.Background(), &extractors.Request{}, &extractors.Response{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got != 0 {
		t.Fatalf("Extract: got %d, want 0", got)
	}
}

func TestNilLiveCounterSafeReads(t *testing.T) {
	t.Parallel()
	var lc *LiveCounter
	lc.Add(5)
	if lc.CurrentUnits() != 0 {
		t.Fatal("nil receiver should return 0")
	}
}
