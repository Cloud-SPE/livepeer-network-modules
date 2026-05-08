package chaincommonsadapter_test

import (
	"testing"
	"time"

	cclock "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock/chaincommonsadapter"
)

func TestNew_RequiresClock(t *testing.T) {
	if _, err := chaincommonsadapter.New(nil); err == nil {
		t.Errorf("New(nil) should fail")
	}
}

func TestNow_DelegatesAndForcesUTC(t *testing.T) {
	// Use chain-commons' system clock; verify Now() comes back in UTC
	// regardless of local zone.
	a, err := chaincommonsadapter.New(cclock.System())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := a.Now()
	if got.Location() != time.UTC {
		t.Errorf("Now().Location() = %v, want UTC", got.Location())
	}
}

func TestNow_DeterministicViaFakeClock(t *testing.T) {
	// FakeClock advances on demand; verify the adapter passes the
	// underlying time through (modulo UTC normalisation).
	fixed := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	fc := chaintest.NewFakeClock(fixed)
	a, _ := chaincommonsadapter.New(fc)

	got := a.Now()
	if !got.Equal(fixed) {
		t.Errorf("Now() = %v, want %v", got, fixed)
	}

	fc.Advance(2 * time.Hour)
	got = a.Now()
	want := fixed.Add(2 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("after Advance, Now() = %v, want %v", got, want)
	}
}
