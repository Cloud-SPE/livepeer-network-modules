package txintent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
)

// countingProcessor records how many times the manager dispatched it.
type countingProcessor struct{ n atomic.Int32 }

func (c *countingProcessor) Process(context.Context, *Manager, IntentID) { c.n.Add(1) }

// Concurrent first submits of one key must produce one intent and one
// processor dispatch. Without the submit mutex two goroutines could both
// miss the existence read, both persist, and both dispatch — spending
// the key at two nonces.
func TestSubmit_ConcurrentSameKey_OneIntentOneDispatch(t *testing.T) {
	proc := &countingProcessor{}
	m, err := New(config.Default().TxIntent, store.Memory(), clock.System(), nil, metrics.NoOp(), proc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const goroutines = 32
	var wg sync.WaitGroup
	var start sync.WaitGroup
	start.Add(1)
	ids := make([]IntentID, goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			ids[i], errs[i] = m.Submit(context.Background(), sampleParams("k", []byte("same")))
		}(i)
	}
	start.Done()
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("submit %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("submit %d returned a different id", i)
		}
	}
	if got := proc.n.Load(); got != 1 {
		t.Fatalf("processor dispatched %d times, want 1", got)
	}
	all, err := m.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d intents persisted, want 1", len(all))
	}
}

// Adopt racing Submit on the same key: exactly one of them creates the
// intent, the other sees it.
func TestAdopt_ConcurrentWithSubmit_OneIntent(t *testing.T) {
	proc := &countingProcessor{}
	m, err := New(config.Default().TxIntent, store.Memory(), clock.System(), nil, metrics.NoOp(), proc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			start.Wait()
			_, _ = m.Submit(context.Background(), sampleParams("k", []byte("race")))
		}()
		go func() {
			defer wg.Done()
			start.Wait()
			_, _ = m.Adopt(context.Background(), sampleParams("k", []byte("race")), chain.TxHash{0xaa}, 7)
		}()
	}
	start.Done()
	wg.Wait()
	all, err := m.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d intents persisted, want 1", len(all))
	}
	if got := proc.n.Load(); got != 1 {
		t.Fatalf("processor dispatched %d times, want 1", got)
	}
}
