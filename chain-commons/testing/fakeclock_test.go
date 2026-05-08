package chaintesting

import (
	"context"
	"testing"
	"time"
)

func TestFakeClock_NowAndAdvance(t *testing.T) {
	c := NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := c.Now(); got.Year() != 2026 {
		t.Errorf("Now() = %v", got)
	}
	c.Advance(time.Hour)
	if got := c.Now().Hour(); got != 1 {
		t.Errorf("After +1h, Now().Hour() = %d", got)
	}
}

func TestFakeClock_DefaultStart(t *testing.T) {
	c := NewFakeClock(time.Time{})
	if c.Now().Year() == 0 {
		t.Errorf("zero start should default, got year 0")
	}
}

func TestFakeClock_Sleep_WakesOnAdvance(t *testing.T) {
	c := NewFakeClock(time.Time{})
	done := make(chan error, 1)
	go func() {
		done <- c.Sleep(context.Background(), 100*time.Millisecond)
	}()
	// Sleep on the real clock so Sleep registers itself before Advance.
	time.Sleep(10 * time.Millisecond)
	c.Advance(200 * time.Millisecond)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Sleep returned err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Sleep did not wake after Advance")
	}
}

func TestFakeClock_Sleep_CtxCancel(t *testing.T) {
	c := NewFakeClock(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); err != context.Canceled {
		t.Errorf("Sleep with cancelled ctx = %v", err)
	}
}

func TestFakeClock_Sleep_Zero(t *testing.T) {
	c := NewFakeClock(time.Time{})
	if err := c.Sleep(context.Background(), 0); err != nil {
		t.Errorf("Sleep(0) = %v", err)
	}
}

func TestFakeClock_Ticker(t *testing.T) {
	c := NewFakeClock(time.Time{})
	tk := c.NewTicker(100 * time.Millisecond)
	defer tk.Stop()

	c.Advance(150 * time.Millisecond) // should fire once
	select {
	case <-tk.C():
	default:
		t.Errorf("ticker should have fired after first advance")
	}
}

func TestFakeClock_Ticker_AfterStop(t *testing.T) {
	c := NewFakeClock(time.Time{})
	tk := c.NewTicker(100 * time.Millisecond)
	tk.Stop()
	c.Advance(time.Hour)
	select {
	case <-tk.C():
		t.Errorf("stopped ticker should not fire")
	case <-time.After(50 * time.Millisecond):
		// good
	}
}
