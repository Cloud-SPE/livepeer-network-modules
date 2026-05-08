package metrics

import (
	"testing"
	"time"
)

// TestCounter exercises every Counter method so the test helper
// itself doesn't bring the metrics package below the 75% coverage
// floor. Counter is consumed by external package tests; those
// coverage hits don't count toward this package, so we self-test.
func TestCounter(t *testing.T) {
	c := NewCounter()

	c.IncGRPCRequest("a", "b", "c", "d")
	c.ObserveGRPC("a", "b", time.Millisecond)
	c.SetGRPCInFlight("a", "b", 3)
	c.IncResolution("well_known", "fresh")
	c.ObserveResolveDuration("well_known", "fresh", time.Millisecond)
	c.IncLegacyFallback("manifest_unavailable")
	c.IncManifestFetch("ok")
	c.ObserveManifestFetch("ok", time.Millisecond, 1024)
	c.IncManifestVerify("verified")
	c.ObserveSignatureVerify(time.Microsecond)
	c.IncCacheLookup("hit_fresh")
	c.IncCacheWrite()
	c.IncCacheEviction("max_stale")
	c.SetCacheEntries(7)
	c.IncAudit("manifest_fetched")
	c.IncOverlayReload("ok")
	c.SetOverlayEntries(2)
	c.IncOverlayDrop("disabled")
	c.IncChainRead("ok")
	c.IncChainWrite("ok")
	c.ObserveChainRead(time.Millisecond)
	c.SetChainLastSuccess(time.Now())
	c.SetManifestFetcherLastSuccess(time.Now())
	c.IncPublisherBuild()
	c.IncPublisherSign("ok")
	c.IncPublisherProbe("ok")
	c.SetUptimeSeconds(10)
	c.SetBuildInfo("v0.0.0", "test", "go1.25")

	// Spot-check the counters that other packages assert on.
	if c.GRPCReqs.Load() != 1 || c.GRPCObserves.Load() != 1 || c.GRPCInFlight.Load() != 3 {
		t.Fatalf("grpc counters: %+v / %+v / %+v", c.GRPCReqs.Load(), c.GRPCObserves.Load(), c.GRPCInFlight.Load())
	}
	if c.Resolutions.Load() != 1 || c.LegacyFallbacks.Load() != 1 {
		t.Fatalf("resolver counters off")
	}
	if got := c.LastFetchOutcome.Load(); got != "ok" {
		t.Fatalf("last fetch outcome = %v", got)
	}
	if got := c.LastVerifyOutcome.Load(); got != "verified" {
		t.Fatalf("last verify outcome = %v", got)
	}
	if got := c.LastChainOutcome.Load(); got != "ok" {
		t.Fatalf("last chain outcome = %v", got)
	}
	if got := c.LastEvictionReason.Load(); got != "max_stale" {
		t.Fatalf("last eviction = %v", got)
	}
	if c.CacheEntriesV.Load() != 7 || c.OverlayEntriesV.Load() != 2 {
		t.Fatalf("gauges off")
	}
	// Handler is a no-op stub — exercising it confirms the interface
	// satisfaction.
	h := c.Handler()
	if h == nil {
		t.Fatal("nil handler")
	}
}
