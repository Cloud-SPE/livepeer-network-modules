package metrics

import (
	"net/http"
	"time"
)

// Noop is the zero-cost Recorder implementation used when the daemon
// is run without --metrics-listen. Every method returns immediately;
// the call site is one indirect call cheaper than checking for a nil
// Recorder.
//
// Handler returns 404 so an operator scraping a misconfigured port
// gets a clear "no metrics here" signal rather than a silent empty
// success.
type Noop struct{}

// NewNoop returns a Noop recorder. Pointer receiver throughout so
// future stateful additions don't change call sites.
func NewNoop() *Noop { return &Noop{} }

func (*Noop) IncGRPCRequest(_, _, _, _ string)         {}
func (*Noop) ObserveGRPC(_, _ string, _ time.Duration) {}
func (*Noop) SetGRPCInFlight(_, _ string, _ int)       {}

func (*Noop) IncResolution(_, _ string)                           {}
func (*Noop) ObserveResolveDuration(_, _ string, _ time.Duration) {}
func (*Noop) IncLegacyFallback(_ string)                          {}
func (*Noop) IncLiveHealthDecision(_ string)                      {}

func (*Noop) IncManifestFetch(_ string)                             {}
func (*Noop) ObserveManifestFetch(_ string, _ time.Duration, _ int) {}
func (*Noop) IncManifestVerify(_ string)                            {}
func (*Noop) ObserveSignatureVerify(_ time.Duration)                {}

func (*Noop) IncCacheLookup(_ string)   {}
func (*Noop) IncCacheWrite()            {}
func (*Noop) IncCacheEviction(_ string) {}
func (*Noop) SetCacheEntries(_ int)     {}
func (*Noop) IncAudit(_ string)         {}

func (*Noop) IncOverlayReload(_ string) {}
func (*Noop) SetOverlayEntries(_ int)   {}
func (*Noop) IncOverlayDrop(_ string)   {}

func (*Noop) IncChainRead(_ string)                     {}
func (*Noop) IncChainWrite(_ string)                    {}
func (*Noop) ObserveChainRead(_ time.Duration)          {}
func (*Noop) SetChainLastSuccess(_ time.Time)           {}
func (*Noop) SetManifestFetcherLastSuccess(_ time.Time) {}

func (*Noop) IncPublisherBuild()         {}
func (*Noop) IncPublisherSign(_ string)  {}
func (*Noop) IncPublisherProbe(_ string) {}

func (*Noop) SetUptimeSeconds(_ float64)                {}
func (*Noop) SetBuildInfo(_ string, _ string, _ string) {}

// Handler returns a 404-everywhere handler.
func (*Noop) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "metrics listener not enabled (start the daemon with --metrics-listen)", http.StatusNotFound)
	})
}
