package observability

import (
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	ccmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
)

// ChainRecorder is the chain-commons metrics.Recorder for this process.
// chain-commons emits its transport metrics (RPC calls by endpoint and
// outcome, circuit-breaker transitions) through this interface with no
// Prometheus dependency of its own; the recorder registers one vector
// per metric name on first use so those series land on the same /metrics
// listener as the executor's own counters.
//
// A metric's label key set is fixed by its first emission. chain-commons
// emits each name with a stable key set, so that is not a restriction in
// practice; an emission with a different key set is dropped rather than
// panicking the loop that produced it.
type ChainRecorder struct {
	mu         sync.Mutex
	reg        prometheus.Registerer
	counters   map[string]*prometheus.CounterVec
	gauges     map[string]*prometheus.GaugeVec
	histograms map[string]*prometheus.HistogramVec
	keys       map[string][]string
}

// NewChainRecorder registers into reg (nil = the default registry).
func NewChainRecorder(reg prometheus.Registerer) *ChainRecorder {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	return &ChainRecorder{
		reg:        reg,
		counters:   map[string]*prometheus.CounterVec{},
		gauges:     map[string]*prometheus.GaugeVec{},
		histograms: map[string]*prometheus.HistogramVec{},
		keys:       map[string][]string{},
	}
}

var _ ccmetrics.Recorder = (*ChainRecorder)(nil)

func labelKeys(labels ccmetrics.Labels) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// keysFor returns the registered key set for name, registering the given
// set on first use. ok is false when a later emission disagrees.
func (r *ChainRecorder) keysFor(name string, labels ccmetrics.Labels) ([]string, bool) {
	want := labelKeys(labels)
	have, seen := r.keys[name]
	if !seen {
		r.keys[name] = want
		return want, true
	}
	if strings.Join(have, ",") != strings.Join(want, ",") {
		return nil, false
	}
	return have, true
}

func values(keys []string, labels ccmetrics.Labels) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = labels[k]
	}
	return out
}

// register tries reg.Register and, on AlreadyRegisteredError (a second
// recorder over the same registry), reuses the existing collector.
func register[T prometheus.Collector](reg prometheus.Registerer, c T) T {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(T); ok {
				return existing
			}
		}
	}
	return c
}

func (r *ChainRecorder) CounterAdd(name string, labels ccmetrics.Labels, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys, ok := r.keysFor(name, labels)
	if !ok {
		return
	}
	vec, found := r.counters[name]
	if !found {
		vec = register(r.reg, prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: "chain-commons counter " + name}, keys))
		r.counters[name] = vec
	}
	vec.WithLabelValues(values(keys, labels)...).Add(delta)
}

func (r *ChainRecorder) GaugeSet(name string, labels ccmetrics.Labels, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys, ok := r.keysFor(name, labels)
	if !ok {
		return
	}
	vec, found := r.gauges[name]
	if !found {
		vec = register(r.reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: "chain-commons gauge " + name}, keys))
		r.gauges[name] = vec
	}
	vec.WithLabelValues(values(keys, labels)...).Set(value)
}

func (r *ChainRecorder) HistogramObserve(name string, labels ccmetrics.Labels, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys, ok := r.keysFor(name, labels)
	if !ok {
		return
	}
	vec, found := r.histograms[name]
	if !found {
		vec = register(r.reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: "chain-commons histogram " + name, Buckets: prometheus.DefBuckets}, keys))
		r.histograms[name] = vec
	}
	vec.WithLabelValues(values(keys, labels)...).Observe(value)
}

var (
	chainOnce     sync.Once
	chainRecorder *ChainRecorder
)

// ChainMetrics returns the process-wide recorder over the default
// registry, so every chain client opened during a run shares one set of
// series on the /metrics listener.
func ChainMetrics() *ChainRecorder {
	chainOnce.Do(func() { chainRecorder = NewChainRecorder(nil) })
	return chainRecorder
}
