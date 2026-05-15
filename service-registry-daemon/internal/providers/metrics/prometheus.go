package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusConfig captures the construction parameters.
type PrometheusConfig struct {
	// MaxSeriesPerMetric is the hard cap on distinct label tuples
	// any single MetricVec may track. New combinations beyond the
	// cap are silently dropped (their values do not propagate to
	// Prometheus); existing combinations continue to update. Set to
	// 0 to disable the cap.
	MaxSeriesPerMetric int

	// OnCapExceeded, if non-nil, is invoked once per exceeded metric
	// (deduped). Operators wire this to their structured logger so
	// the violation is loud in the daemon log.
	OnCapExceeded func(metricName string, observed int, cap int)
}

// Prometheus is the production Recorder. All metrics live in a
// single, dedicated *prometheus.Registry that we own — we do NOT use
// the package-global default registry, so noisy consumer libs don't
// pollute our exposition output.
type Prometheus struct {
	reg *prometheus.Registry
	cfg PrometheusConfig

	// Counters
	grpcRequests     *capVec
	resolutions      *capVec
	legacyFallbacks  *capVec
	liveHealthDecisions *capVec
	manifestFetches  *capVec
	manifestVerifies *capVec
	cacheLookups     *capVec
	cacheWrites      prometheus.Counter
	cacheEvictions   *capVec
	auditEvents      *capVec
	overlayReloads   *capVec
	overlayDrops     *capVec
	chainReads       *capVec
	chainWrites      *capVec
	publisherBuilds  prometheus.Counter
	publisherSigns   *capVec
	publisherProbes  *capVec

	// Histograms
	grpcDuration     *prometheus.HistogramVec
	grpcDurationFast *prometheus.HistogramVec
	resolveDuration  *prometheus.HistogramVec
	manifestFetch    *prometheus.HistogramVec
	manifestBytes    prometheus.Histogram
	signatureVerify  prometheus.Histogram
	chainRead        prometheus.Histogram

	// Gauges
	cacheEntries               prometheus.Gauge
	overlayEntries             prometheus.Gauge
	chainLastSuccess           prometheus.Gauge
	manifestFetcherLastSuccess prometheus.Gauge
	grpcInFlight               *prometheus.GaugeVec
	uptimeSeconds              prometheus.Gauge
	buildInfo                  *prometheus.GaugeVec
}

// NewPrometheus constructs the Prometheus Recorder. It also installs
// the standard process + Go runtime collectors so /metrics surfaces
// `go_*` and `process_*` for free.
func NewPrometheus(cfg PrometheusConfig) *Prometheus {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	p := &Prometheus{reg: reg, cfg: cfg}
	const ns = "livepeer_registry"

	// ----- Counters -----
	p.grpcRequests = newCap(reg, p.onCapHit, "grpc_requests_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "grpc_requests_total",
			Help: "Total gRPC requests served by the daemon."},
		[]string{"service", "method", "code", "registry_code"},
	))
	p.resolutions = newCap(reg, p.onCapHit, "resolutions_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "resolutions_total",
			Help: "Total ResolveByAddress completions, labeled by mode + freshness."},
		[]string{"mode", "freshness"},
	))
	p.legacyFallbacks = newCap(reg, p.onCapHit, "legacy_fallbacks_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "legacy_fallbacks_total",
			Help: "Resolver synthesized a legacy node because no manifest was returnable."},
		[]string{"reason"},
	))
	p.liveHealthDecisions = newCap(reg, p.onCapHit, "live_health_decisions_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "live_health_decisions_total",
			Help: "Layer 2 live-health route decisions made by the resolver before a route reaches a gateway."},
		[]string{"reason"},
	))
	p.manifestFetches = newCap(reg, p.onCapHit, "manifest_fetches_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "manifest_fetches_total",
			Help: "HTTP fetches of resolver manifest URLs."},
		[]string{"outcome"},
	))
	p.manifestVerifies = newCap(reg, p.onCapHit, "manifest_verifications_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "manifest_verifications_total",
			Help: "Manifest signature verifications. signature_mismatch indicates MITM or operator misconfiguration."},
		[]string{"outcome"},
	))
	p.cacheLookups = newCap(reg, p.onCapHit, "cache_lookups_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "cache_lookups_total",
			Help: "Manifest cache lookups, labeled by result."},
		[]string{"result"},
	))
	p.cacheWrites = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "cache_writes_total",
		Help: "Manifest cache writes (after verify, on every successful refresh).",
	})
	reg.MustRegister(p.cacheWrites)
	p.cacheEvictions = newCap(reg, p.onCapHit, "cache_evictions_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "cache_evictions_total",
			Help: "Manifest cache evictions, labeled by reason."},
		[]string{"reason"},
	))
	p.auditEvents = newCap(reg, p.onCapHit, "audit_events_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "audit_events_total",
			Help: "Audit events appended to the per-orchestrator log, labeled by kind."},
		[]string{"kind"},
	))
	p.overlayReloads = newCap(reg, p.onCapHit, "overlay_reloads_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "overlay_reloads_total",
			Help: "Static-overlay (nodes.yaml) reload outcomes."},
		[]string{"outcome"},
	))
	p.overlayDrops = newCap(reg, p.onCapHit, "overlay_dropped_nodes_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "overlay_dropped_nodes_total",
			Help: "Nodes dropped by overlay policy before reaching the consumer."},
		[]string{"reason"},
	))
	p.chainReads = newCap(reg, p.onCapHit, "chain_reads_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "chain_reads_total",
			Help: "ServiceRegistry.getServiceURI eth_call outcomes."},
		[]string{"outcome"},
	))
	p.chainWrites = newCap(reg, p.onCapHit, "chain_writes_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "chain_writes_total",
			Help: "ServiceRegistry.setServiceURI tx outcomes."},
		[]string{"outcome"},
	))
	p.publisherBuilds = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "publisher_builds_total",
		Help: "Publisher.BuildManifest invocations.",
	})
	reg.MustRegister(p.publisherBuilds)
	p.publisherSigns = newCap(reg, p.onCapHit, "publisher_signs_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "publisher_signs_total",
			Help: "Publisher.SignManifest invocations, labeled by outcome."},
		[]string{"outcome"},
	))
	p.publisherProbes = newCap(reg, p.onCapHit, "publisher_probe_workers_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "publisher_probe_workers_total",
			Help: "Publisher.ProbeWorker outcomes."},
		[]string{"outcome"},
	))

	// ----- Histograms -----
	p.grpcDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "grpc_request_duration_seconds",
			Help:    "gRPC unary handler latency (default Prometheus buckets).",
			Buckets: prometheus.DefBuckets},
		[]string{"service", "method"},
	)
	reg.MustRegister(p.grpcDuration)

	// Sub-millisecond variant for the unix-socket fast path
	// (cache hits return in ~50µs–500µs).
	p.grpcDurationFast = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "grpc_request_duration_seconds_fast",
			Help:    "gRPC unary handler latency, sub-ms buckets for the unix-socket fast path.",
			Buckets: []float64{0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01}},
		[]string{"service", "method"},
	)
	reg.MustRegister(p.grpcDurationFast)

	p.resolveDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "resolve_duration_seconds",
			Help:    "End-to-end ResolveByAddress latency (chain + fetch + verify + overlay).",
			Buckets: prometheus.DefBuckets},
		[]string{"mode", "freshness"},
	)
	reg.MustRegister(p.resolveDuration)

	p.manifestFetch = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "manifest_fetch_duration_seconds",
			Help:    "HTTP manifest fetch duration, labeled by outcome.",
			Buckets: prometheus.DefBuckets},
		[]string{"outcome"},
	)
	reg.MustRegister(p.manifestFetch)

	p.manifestBytes = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "manifest_fetch_bytes",
		Help: "Distribution of fetched manifest body sizes.",
		Buckets: []float64{
			512, 1024, 4096, 16384, 65536, 131072, 262144,
		},
	})
	reg.MustRegister(p.manifestBytes)

	p.signatureVerify = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "signature_verify_duration_seconds",
		Help: "secp256k1 recover + comparison latency.",
		Buckets: []float64{
			0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025,
		},
	})
	reg.MustRegister(p.signatureVerify)

	p.chainRead = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "chain_read_duration_seconds",
		Help:    "ServiceRegistry.getServiceURI eth_call latency.",
		Buckets: prometheus.DefBuckets,
	})
	reg.MustRegister(p.chainRead)

	// ----- Gauges -----
	p.cacheEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "cache_entries",
		Help: "Current number of orchestrator addresses in the manifest cache.",
	})
	reg.MustRegister(p.cacheEntries)

	p.overlayEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "overlay_entries",
		Help: "Current number of operator-curated overlay entries.",
	})
	reg.MustRegister(p.overlayEntries)

	p.chainLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "chain_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful chain read.",
	})
	reg.MustRegister(p.chainLastSuccess)

	p.manifestFetcherLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "manifest_fetcher_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful manifest fetch.",
	})
	reg.MustRegister(p.manifestFetcherLastSuccess)

	p.grpcInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: ns, Name: "grpc_in_flight_requests",
			Help: "Current count of in-flight unary gRPC requests by (service, method)."},
		[]string{"service", "method"},
	)
	reg.MustRegister(p.grpcInFlight)

	p.uptimeSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "uptime_seconds",
		Help: "Seconds since daemon start.",
	})
	reg.MustRegister(p.uptimeSeconds)

	p.buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: ns, Name: "build_info",
			Help: "Constant 1 gauge labeled with daemon build metadata."},
		[]string{"version", "mode", "go_version"},
	)
	reg.MustRegister(p.buildInfo)

	p.ApplyCap(cfg.MaxSeriesPerMetric)
	return p
}

// onCapHit dispatches to the user-supplied callback. Wrapped because
// it's accessed from many newCap closures.
func (p *Prometheus) onCapHit(name string, observed, cap int) {
	if p.cfg.OnCapExceeded != nil {
		p.cfg.OnCapExceeded(name, observed, cap)
	}
}

// Registry returns the underlying *prometheus.Registry. Exposed so
// runtime/metrics can serve it via promhttp.HandlerFor.
func (p *Prometheus) Registry() *prometheus.Registry { return p.reg }

// Handler returns a promhttp handler over our private registry.
func (p *Prometheus) Handler() http.Handler {
	return promhttp.HandlerFor(p.reg, promhttp.HandlerOpts{})
}

// ----- Recorder method implementations -----

func (p *Prometheus) IncGRPCRequest(service, method, code, registryCode string) {
	p.grpcRequests.inc(unset(service), unset(method), unset(code), unset(registryCode))
}

func (p *Prometheus) ObserveGRPC(service, method string, d time.Duration) {
	s := unset(service)
	m := unset(method)
	p.grpcDuration.WithLabelValues(s, m).Observe(d.Seconds())
	p.grpcDurationFast.WithLabelValues(s, m).Observe(d.Seconds())
}

func (p *Prometheus) SetGRPCInFlight(service, method string, n int) {
	p.grpcInFlight.WithLabelValues(unset(service), unset(method)).Set(float64(n))
}

func (p *Prometheus) IncResolution(mode, freshness string) {
	p.resolutions.inc(unset(mode), unset(freshness))
}
func (p *Prometheus) ObserveResolveDuration(mode, freshness string, d time.Duration) {
	p.resolveDuration.WithLabelValues(unset(mode), unset(freshness)).Observe(d.Seconds())
}
func (p *Prometheus) IncLegacyFallback(reason string) {
	p.legacyFallbacks.inc(unset(reason))
}
func (p *Prometheus) IncLiveHealthDecision(reason string) {
	p.liveHealthDecisions.inc(unset(reason))
}

func (p *Prometheus) IncManifestFetch(outcome string) {
	p.manifestFetches.inc(unset(outcome))
}
func (p *Prometheus) ObserveManifestFetch(outcome string, d time.Duration, bodyBytes int) {
	p.manifestFetch.WithLabelValues(unset(outcome)).Observe(d.Seconds())
	if bodyBytes > 0 {
		p.manifestBytes.Observe(float64(bodyBytes))
	}
}
func (p *Prometheus) IncManifestVerify(outcome string) {
	p.manifestVerifies.inc(unset(outcome))
}
func (p *Prometheus) ObserveSignatureVerify(d time.Duration) {
	p.signatureVerify.Observe(d.Seconds())
}

func (p *Prometheus) IncCacheLookup(result string) { p.cacheLookups.inc(unset(result)) }
func (p *Prometheus) IncCacheWrite()               { p.cacheWrites.Inc() }
func (p *Prometheus) IncCacheEviction(reason string) {
	p.cacheEvictions.inc(unset(reason))
}
func (p *Prometheus) SetCacheEntries(n int) {
	p.cacheEntries.Set(float64(n))
}
func (p *Prometheus) IncAudit(kind string) { p.auditEvents.inc(unset(kind)) }

func (p *Prometheus) IncOverlayReload(outcome string) {
	p.overlayReloads.inc(unset(outcome))
}
func (p *Prometheus) SetOverlayEntries(n int) { p.overlayEntries.Set(float64(n)) }
func (p *Prometheus) IncOverlayDrop(reason string) {
	p.overlayDrops.inc(unset(reason))
}

func (p *Prometheus) IncChainRead(outcome string)  { p.chainReads.inc(unset(outcome)) }
func (p *Prometheus) IncChainWrite(outcome string) { p.chainWrites.inc(unset(outcome)) }
func (p *Prometheus) ObserveChainRead(d time.Duration) {
	p.chainRead.Observe(d.Seconds())
}
func (p *Prometheus) SetChainLastSuccess(t time.Time) {
	p.chainLastSuccess.Set(float64(t.Unix()))
}
func (p *Prometheus) SetManifestFetcherLastSuccess(t time.Time) {
	p.manifestFetcherLastSuccess.Set(float64(t.Unix()))
}

func (p *Prometheus) IncPublisherBuild() { p.publisherBuilds.Inc() }
func (p *Prometheus) IncPublisherSign(outcome string) {
	p.publisherSigns.inc(unset(outcome))
}
func (p *Prometheus) IncPublisherProbe(outcome string) {
	p.publisherProbes.inc(unset(outcome))
}

func (p *Prometheus) SetUptimeSeconds(s float64) { p.uptimeSeconds.Set(s) }
func (p *Prometheus) SetBuildInfo(version, mode, goVersion string) {
	p.buildInfo.WithLabelValues(version, mode, goVersion).Set(1)
}

// unset returns LabelUnset if v is empty. Prometheus accepts empty
// strings as label values but they read poorly in Grafana.
func unset(v string) string {
	if v == "" {
		return LabelUnset
	}
	return v
}

// ----- cardinality cap -----

// capVec wraps a CounterVec with cardinality enforcement. Tracks
// distinct label tuples in a sync.Map; if the count exceeds the cap,
// new label combinations are silently dropped (existing combinations
// still update).
type capVec struct {
	vec      *prometheus.CounterVec
	name     string
	max      int
	seen     sync.Map // map[string]struct{} — key = "v1\x00v2\x00..."
	count    atomic.Int64
	exceeded atomic.Bool
	onExceed func(name string, observed, cap int)
}

func newCap(reg prometheus.Registerer, onExceed func(string, int, int), name string, v *prometheus.CounterVec) *capVec {
	reg.MustRegister(v)
	return &capVec{vec: v, name: name, onExceed: onExceed}
}

// withCap can be set by NewPrometheus after construction; the
// capVecs are built before cfg is fully wired so we set max here.
func (c *capVec) withCap(max int) *capVec { c.max = max; return c }

// inc increments the counter at (vals...) labels, enforcing the cap.
// If the cap is 0 (disabled) or the label tuple has been seen before,
// fast path. Otherwise check the count.
func (c *capVec) inc(vals ...string) {
	if c.max <= 0 {
		c.vec.WithLabelValues(vals...).Inc()
		return
	}
	key := joinNul(vals)
	if _, ok := c.seen.Load(key); ok {
		c.vec.WithLabelValues(vals...).Inc()
		return
	}
	if c.count.Load() >= int64(c.max) {
		// Cap reached. First-time-only log.
		if c.exceeded.CompareAndSwap(false, true) && c.onExceed != nil {
			c.onExceed(c.name, int(c.count.Load()), c.max)
		}
		return
	}
	c.seen.Store(key, struct{}{})
	c.count.Add(1)
	c.vec.WithLabelValues(vals...).Inc()
}

// joinNul concatenates label values with NUL separators. NUL is not
// permitted in Prometheus label values so this is collision-free.
func joinNul(vs []string) string {
	n := 0
	for _, v := range vs {
		n += len(v) + 1
	}
	out := make([]byte, 0, n)
	for _, v := range vs {
		out = append(out, v...)
		out = append(out, 0)
	}
	return string(out)
}

// ApplyCap sets the max-series-per-metric cap on every wrapped vec.
// Called by NewPrometheus after all vecs are built.
func (p *Prometheus) ApplyCap(max int) {
	for _, v := range []*capVec{
		p.grpcRequests, p.resolutions, p.legacyFallbacks,
		p.manifestFetches, p.manifestVerifies, p.cacheLookups,
		p.cacheEvictions, p.auditEvents, p.overlayReloads,
		p.overlayDrops, p.chainReads, p.chainWrites,
		p.publisherSigns, p.publisherProbes,
	} {
		v.withCap(max)
	}
}
