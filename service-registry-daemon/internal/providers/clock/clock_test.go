package clock

import (
	"testing"
	"time"
)

func TestSystem_NowMonotonicIsh(t *testing.T) {
	c := System{}
	a := c.Now()
	time.Sleep(time.Millisecond)
	b := c.Now()
	if !b.After(a) {
		t.Fatalf("expected monotonic-ish forward time, got a=%v b=%v", a, b)
	}
}

func TestFixed_AdvanceAndNow(t *testing.T) {
	f := &Fixed{T: time.Unix(1745000000, 0)}
	if f.Now().Unix() != 1745000000 {
		t.Fatalf("Now() = %v", f.Now())
	}
	f.Advance(time.Hour)
	if f.Now().Unix() != 1745000000+3600 {
		t.Fatalf("Advance: Now() = %v", f.Now())
	}
}
