package manifestcache

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// fakeRecorder is a tiny in-package Recorder stub. We don't want to
// depend on internal/providers/metrics' production impl in repo tests.
type fakeRecorder struct {
	cacheWrites    atomic.Int64
	cacheEntriesV  atomic.Int64
	evictionReason string
}

func (*fakeRecorder) IncGRPCRequest(_, _, _, _ string)                      {}
func (*fakeRecorder) ObserveGRPC(_, _ string, _ time.Duration)              {}
func (*fakeRecorder) SetGRPCInFlight(_, _ string, _ int)                    {}
func (*fakeRecorder) IncResolution(_, _ string)                             {}
func (*fakeRecorder) ObserveResolveDuration(_, _ string, _ time.Duration)   {}
func (*fakeRecorder) IncLegacyFallback(_ string)                            {}
func (*fakeRecorder) IncLiveHealthDecision(_ string)                        {}
func (*fakeRecorder) IncManifestFetch(_ string)                             {}
func (*fakeRecorder) ObserveManifestFetch(_ string, _ time.Duration, _ int) {}
func (*fakeRecorder) IncManifestVerify(_ string)                            {}
func (*fakeRecorder) ObserveSignatureVerify(_ time.Duration)                {}
func (*fakeRecorder) IncCacheLookup(_ string)                               {}
func (f *fakeRecorder) IncCacheWrite()                                      { f.cacheWrites.Add(1) }
func (f *fakeRecorder) IncCacheEviction(reason string)                      { f.evictionReason = reason }
func (f *fakeRecorder) SetCacheEntries(n int)                               { f.cacheEntriesV.Store(int64(n)) }
func (*fakeRecorder) IncAudit(_ string)                                     {}
func (*fakeRecorder) IncOverlayReload(_ string)                             {}
func (*fakeRecorder) SetOverlayEntries(_ int)                               {}
func (*fakeRecorder) IncOverlayDrop(_ string)                               {}
func (*fakeRecorder) IncChainRead(_ string)                                 {}
func (*fakeRecorder) IncChainWrite(_ string)                                {}
func (*fakeRecorder) ObserveChainRead(_ time.Duration)                      {}
func (*fakeRecorder) SetChainLastSuccess(_ time.Time)                       {}
func (*fakeRecorder) SetManifestFetcherLastSuccess(_ time.Time)             {}
func (*fakeRecorder) IncPublisherBuild()                                    {}
func (*fakeRecorder) IncPublisherSign(_ string)                             {}
func (*fakeRecorder) IncPublisherProbe(_ string)                            {}
func (*fakeRecorder) SetUptimeSeconds(_ float64)                            {}
func (*fakeRecorder) SetBuildInfo(_, _, _ string)                           {}
func (*fakeRecorder) Handler() http.Handler                                 { return nil }

func TestMeteredRepo_RoundTrip(t *testing.T) {
	rec := &fakeRecorder{}
	r := WithMetrics(New(store.NewMemory()), rec)

	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")

	if err := r.Put(&Entry{EthAddress: addr}); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(&Entry{EthAddress: other}); err != nil {
		t.Fatal(err)
	}
	if rec.cacheEntriesV.Load() != 2 {
		t.Fatalf("cache_entries gauge = %d, want 2", rec.cacheEntriesV.Load())
	}
	if _, ok, _ := r.Get(addr); !ok {
		t.Fatal("Get failed")
	}
	if err := r.Delete(addr); err != nil {
		t.Fatal(err)
	}
	if rec.evictionReason != "forced" {
		t.Fatalf("eviction reason = %q", rec.evictionReason)
	}
	if rec.cacheEntriesV.Load() != 1 {
		t.Fatalf("after delete cache_entries = %d, want 1", rec.cacheEntriesV.Load())
	}
	if list, _ := r.List(); len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
}

func TestWithMetrics_NilRecorderReturnsInner(t *testing.T) {
	inner := New(store.NewMemory())
	if got := WithMetrics(inner, nil); got != inner {
		t.Fatal("nil recorder should return the inner repo unchanged")
	}
}
