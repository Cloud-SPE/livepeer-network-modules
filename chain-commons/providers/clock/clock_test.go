package clock

import (
	"context"
	"testing"
	"time"
)

func TestSystemClock_Now(t *testing.T) {
	c := System()
	a := c.Now()
	b := c.Now()
	if !b.After(a) && !b.Equal(a) {
		t.Errorf("Now() should be monotonic: a=%v b=%v", a, b)
	}
}

func TestSystemClock_Sleep(t *testing.T) {
	c := System()
	start := time.Now()
	if err := c.Sleep(context.Background(), 10*time.Millisecond); err != nil {
		t.Errorf("Sleep returned err: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 5*time.Millisecond {
		t.Errorf("Sleep returned in %v, expected ~10ms", elapsed)
	}
}

func TestSystemClock_Sleep_CtxCancel(t *testing.T) {
	c := System()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Sleep(ctx, 1*time.Hour)
	if err != context.Canceled {
		t.Errorf("Sleep with cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestSystemClock_Ticker(t *testing.T) {
	c := System()
	tk := c.NewTicker(5 * time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
		// got tick
	case <-time.After(100 * time.Millisecond):
		t.Errorf("ticker did not fire within 100ms")
	}
}
