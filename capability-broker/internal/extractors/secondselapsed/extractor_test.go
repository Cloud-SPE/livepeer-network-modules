package secondselapsed

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// TestLiveCounter_MatchesExtract drives a fake session: a known elapsed
// duration is fed into both the LiveCounter (via injected clock) and
// Extract (via the buffered Response.Duration). The two paths MUST
// agree at end-of-session per plan 0015 §5.4.
func TestLiveCounter_MatchesExtract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		granularity any
		rounding    string
		elapsed     time.Duration
		want        uint64
	}{
		{"granularity=1, ceil, 5.5s", 1, "ceil", 5500 * time.Millisecond, 6},
		{"granularity=1, floor, 5.5s", 1, "floor", 5500 * time.Millisecond, 5},
		{"granularity=1, round, 5.4s", 1, "round", 5400 * time.Millisecond, 5},
		{"granularity=0.001, ceil, 250ms", 0.001, "ceil", 250 * time.Millisecond, 250},
		{"granularity=10, ceil, 35s", 10, "ceil", 35 * time.Second, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext, err := New(map[string]any{
				"granularity": tc.granularity,
				"rounding":    tc.rounding,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			se := ext.(*Extractor)
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			lc := se.NewLiveCounter(start)
			// Inject a fake clock for deterministic CurrentUnits.
			lc.now = func() time.Time { return start.Add(tc.elapsed) }
			if got := lc.CurrentUnits(); got != tc.want {
				t.Fatalf("LiveCounter.CurrentUnits: got %d, want %d", got, tc.want)
			}
			resp := &extractors.Response{
				Body:     []byte{},
				Headers:  http.Header{},
				Duration: tc.elapsed,
			}
			req := &extractors.Request{Method: "GET", Headers: http.Header{}}
			gotExtract, err := se.Extract(context.Background(), req, resp)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if gotExtract != tc.want {
				t.Fatalf("Extract: got %d, want %d", gotExtract, tc.want)
			}
			if gotExtract != lc.CurrentUnits() {
				t.Fatalf("Extract vs LiveCounter mismatch: extract=%d, live=%d", gotExtract, lc.CurrentUnits())
			}
		})
	}
}

// TestLiveCounter_NegativeClampsToZero protects against a bogus injected
// "now" that predates start.
func TestLiveCounter_NegativeClampsToZero(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{"granularity": 1, "rounding": "ceil"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := time.Now()
	lc := ext.(*Extractor).NewLiveCounter(start)
	lc.now = func() time.Time { return start.Add(-5 * time.Second) }
	if got := lc.CurrentUnits(); got != 0 {
		t.Fatalf("CurrentUnits with negative elapsed: got %d, want 0", got)
	}
}

// TestLiveCounter_DefaultClockMonotonic verifies that without an
// injected clock, CurrentUnits reports increasing values over time.
func TestLiveCounter_DefaultClockMonotonic(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{"granularity": 0.001, "rounding": "ceil"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := time.Now()
	lc := ext.(*Extractor).NewLiveCounter(start)
	first := lc.CurrentUnits()
	time.Sleep(20 * time.Millisecond)
	second := lc.CurrentUnits()
	if second < first {
		t.Fatalf("CurrentUnits not monotonic: first=%d, second=%d", first, second)
	}
}

// TestLiveCounter_NilSafe documents nil-safety.
func TestLiveCounter_NilSafe(t *testing.T) {
	t.Parallel()
	var lc *LiveCounter
	if got := lc.CurrentUnits(); got != 0 {
		t.Fatalf("nil LiveCounter.CurrentUnits: got %d, want 0", got)
	}
}

// TestLiveCounter_InterfaceConformance verifies extractors.LiveCounter
// is satisfied.
func TestLiveCounter_InterfaceConformance(t *testing.T) {
	t.Parallel()
	ext, err := New(map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lc := ext.(*Extractor).NewLiveCounter(time.Now())
	var _ extractors.LiveCounter = lc
}
