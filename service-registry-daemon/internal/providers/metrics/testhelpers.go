package metrics

import (
	"net/http"
	"sync/atomic"
	"time"
)

// Counter is a tiny test-only Recorder implementation that counts
// each method invocation and stores last-set values in maps. Other
// packages reuse this in tests so they don't have to redefine a
// Recorder stub for every package.
//
// Goroutine-safe: counters use atomic; map writes use the embedded mu.
//
// Not exported as part of the production API surface — file is
// _testhelpers.go-style but kept buildable so go vet runs against it.
type Counter struct {
	GRPCReqs          atomic.Int64
	GRPCObserves      atomic.Int64
	GRPCInFlight      atomic.Int64
	Resolutions       atomic.Int64
	ResolveDurations  atomic.Int64
	LegacyFallbacks   atomic.Int64
	ManifestFetches   atomic.Int64
	ManifestFetchObs  atomic.Int64
	ManifestVerifies  atomic.Int64
	SignatureVerifies atomic.Int64
	CacheLookups      atomic.Int64
	CacheWrites       atomic.Int64
	CacheEvictions    atomic.Int64
	AuditEvents       atomic.Int64
	OverlayReloads    atomic.Int64
	OverlayDrops      atomic.Int64
	ChainReads        atomic.Int64
	ChainWrites       atomic.Int64
	ChainObserves     atomic.Int64
	PublisherBuilds   atomic.Int64
	PublisherSigns    atomic.Int64
	PublisherProbes   atomic.Int64

	CacheEntriesV   atomic.Int64
	OverlayEntriesV atomic.Int64

	// LastReason fields capture the most recent label value for
	// assertions in package tests.
	LastEvictionReason atomic.Value // string
	LastFetchOutcome   atomic.Value // string
	LastVerifyOutcome  atomic.Value // string
	LastChainOutcome   atomic.Value // string
}

// NewCounter returns a fresh Counter recorder.
func NewCounter() *Counter { return &Counter{} }

func (c *Counter) IncGRPCRequest(_, _, _, _ string)         { c.GRPCReqs.Add(1) }
func (c *Counter) ObserveGRPC(_, _ string, _ time.Duration) { c.GRPCObserves.Add(1) }
func (c *Counter) SetGRPCInFlight(_, _ string, n int)       { c.GRPCInFlight.Store(int64(n)) }

func (c *Counter) IncResolution(_, _ string)                           { c.Resolutions.Add(1) }
func (c *Counter) ObserveResolveDuration(_, _ string, _ time.Duration) { c.ResolveDurations.Add(1) }
func (c *Counter) IncLegacyFallback(_ string)                          { c.LegacyFallbacks.Add(1) }

func (c *Counter) IncManifestFetch(outcome string) {
	c.ManifestFetches.Add(1)
	c.LastFetchOutcome.Store(outcome)
}
func (c *Counter) ObserveManifestFetch(_ string, _ time.Duration, _ int) { c.ManifestFetchObs.Add(1) }
func (c *Counter) IncManifestVerify(outcome string) {
	c.ManifestVerifies.Add(1)
	c.LastVerifyOutcome.Store(outcome)
}
func (c *Counter) ObserveSignatureVerify(_ time.Duration) { c.SignatureVerifies.Add(1) }

func (c *Counter) IncCacheLookup(_ string) { c.CacheLookups.Add(1) }
func (c *Counter) IncCacheWrite()          { c.CacheWrites.Add(1) }
func (c *Counter) IncCacheEviction(reason string) {
	c.CacheEvictions.Add(1)
	c.LastEvictionReason.Store(reason)
}
func (c *Counter) SetCacheEntries(n int) { c.CacheEntriesV.Store(int64(n)) }
func (c *Counter) IncAudit(_ string)     { c.AuditEvents.Add(1) }

func (c *Counter) IncOverlayReload(_ string) { c.OverlayReloads.Add(1) }
func (c *Counter) SetOverlayEntries(n int)   { c.OverlayEntriesV.Store(int64(n)) }
func (c *Counter) IncOverlayDrop(_ string)   { c.OverlayDrops.Add(1) }

func (c *Counter) IncChainRead(outcome string) {
	c.ChainReads.Add(1)
	c.LastChainOutcome.Store(outcome)
}
func (c *Counter) IncChainWrite(_ string)                    { c.ChainWrites.Add(1) }
func (c *Counter) ObserveChainRead(_ time.Duration)          { c.ChainObserves.Add(1) }
func (c *Counter) SetChainLastSuccess(_ time.Time)           {}
func (c *Counter) SetManifestFetcherLastSuccess(_ time.Time) {}

func (c *Counter) IncPublisherBuild()         { c.PublisherBuilds.Add(1) }
func (c *Counter) IncPublisherSign(_ string)  { c.PublisherSigns.Add(1) }
func (c *Counter) IncPublisherProbe(_ string) { c.PublisherProbes.Add(1) }

func (c *Counter) SetUptimeSeconds(_ float64)                {}
func (c *Counter) SetBuildInfo(_ string, _ string, _ string) {}

func (c *Counter) Handler() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
}
