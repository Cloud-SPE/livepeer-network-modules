package bytescounted

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// TestLiveCounter_MatchesExtract drives a fake session: 1024 bytes are
// streamed across 3 goroutines (333 + 333 + 358), the LiveCounter is
// polled, and at the end Extract over a buffered body of identical size
// produces the same unit total. Plan 0015 §5.4 reconciliation
// requirement.
func TestLiveCounter_MatchesExtract(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{
		"direction":   "response",
		"granularity": 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bc := ext.(*Extractor)
	lc := bc.NewLiveCounter()

	// Three concurrent producers. Plan 0015 §4.4 demands cross-goroutine
	// safety; this exercises atomic.AddUint64 contention.
	const total = 1024
	chunks := []uint64{333, 333, 358}
	var wg sync.WaitGroup
	wg.Add(len(chunks))
	for _, n := range chunks {
		go func(n uint64) {
			defer wg.Done()
			for i := uint64(0); i < n; i++ {
				lc.AddBytes(1)
			}
		}(n)
	}
	wg.Wait()

	if got := lc.CurrentUnits(); got != total {
		t.Fatalf("LiveCounter.CurrentUnits after concurrent adds: got %d, want %d", got, total)
	}

	// Now run Extract over a buffered body of the same size. Plan 0015
	// requires the two paths to agree at end-of-session (§5.4).
	body := make([]byte, total)
	resp := &extractors.Response{
		Status:  200,
		Body:    body,
		Headers: http.Header{},
	}
	req := &extractors.Request{Method: "POST", Headers: http.Header{}}
	got, err := bc.Extract(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got != lc.CurrentUnits() {
		t.Fatalf("Extract vs LiveCounter mismatch: extract=%d, live=%d", got, lc.CurrentUnits())
	}
}

// TestLiveCounter_GranularityDivisor verifies a non-1 granularity divides
// the running byte count at read time, mirroring Extract's behavior.
func TestLiveCounter_GranularityDivisor(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{
		"direction":   "response",
		"granularity": 8, // 1 unit = 8 bytes
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lc := ext.(*Extractor).NewLiveCounter()
	lc.AddBytes(64) // 8 units exactly
	if got := lc.CurrentUnits(); got != 8 {
		t.Fatalf("CurrentUnits with granularity=8: got %d, want 8", got)
	}
	lc.AddBytes(7) // 71 / 8 = 8 (truncated, matches Extract)
	if got := lc.CurrentUnits(); got != 8 {
		t.Fatalf("CurrentUnits at 71 bytes / granularity=8: got %d, want 8", got)
	}
	lc.AddBytes(1) // 72 / 8 = 9
	if got := lc.CurrentUnits(); got != 9 {
		t.Fatalf("CurrentUnits at 72 bytes / granularity=8: got %d, want 9", got)
	}
}

// TestLiveCounter_NilSafe documents that a nil LiveCounter does not
// panic on read or write paths. Drivers may pass a nil counter when
// the configured extractor does not support live polling.
func TestLiveCounter_NilSafe(t *testing.T) {
	t.Parallel()
	var lc *LiveCounter
	if got := lc.CurrentUnits(); got != 0 {
		t.Fatalf("nil LiveCounter.CurrentUnits: got %d, want 0", got)
	}
	lc.AddBytes(1024) // must not panic
}

// TestLiveCounter_Interface verifies the extractors.LiveCounter contract
// is satisfied at compile time and via reflection.
func TestLiveCounter_Interface(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lc := ext.(*Extractor).NewLiveCounter()
	var _ extractors.LiveCounter = lc
	lc.AddBytes(42)
	if got := lc.CurrentUnits(); got != 42 {
		t.Fatalf("CurrentUnits: got %d, want 42", got)
	}
}
