package metrics

import (
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Dynamic is the name-addressed emission surface chain-commons expects
// from a daemon (its metrics.Recorder: CounterAdd / GaugeSet /
// HistogramObserve). The payment-daemon's own series are typed methods
// on Recorder and keep their names; Dynamic exists so the shared chain
// glue (multi-RPC failover, Controller resolver, gas oracle) can expose
// its livepeer_chain_* series on the same /metrics without the daemon
// having to know the names ahead of time.
//
// Implemented by Prometheus (lazy registration per name) and Noop.
type Dynamic interface {
	CounterAdd(name string, labels map[string]string, delta float64)
	GaugeSet(name string, labels map[string]string, value float64)
	HistogramObserve(name string, labels map[string]string, value float64)
}

// dynamicVecs holds the lazily registered chain-commons series. Label
// keys are fixed by the first emission under a name; an emission whose
// key set differs is dropped rather than panicking the daemon over a
// metric it does not own.
type dynamicVecs struct {
	mu         sync.Mutex
	counters   map[string]*dynamicCounter
	gauges     map[string]*dynamicGauge
	histograms map[string]*dynamicHistogram
}

type dynamicCounter struct {
	keys []string
	vec  *prometheus.CounterVec
}

type dynamicGauge struct {
	keys []string
	vec  *prometheus.GaugeVec
}

type dynamicHistogram struct {
	keys []string
	vec  *prometheus.HistogramVec
}

func labelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func labelValues(keys []string, labels map[string]string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = unset(labels[k])
	}
	return out
}

// externalName keeps a chain-commons name as-is when it already carries
// a namespace prefix (livepeer_chain_*), and otherwise files it under
// the daemon's namespace so an unprefixed name cannot collide with a
// third-party collector on the same registry.
func externalName(name string) string {
	if strings.HasPrefix(name, "livepeer_") {
		return name
	}
	return namespace + "_" + name
}

// CounterAdd implements Dynamic.
func (p *Prometheus) CounterAdd(name string, labels map[string]string, delta float64) {
	if delta < 0 {
		return
	}
	keys := labelKeys(labels)
	p.dyn.mu.Lock()
	c, ok := p.dyn.counters[name]
	if !ok {
		vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: externalName(name), Help: "Emitted by chain-commons."}, keys)
		if err := p.reg.Register(vec); err != nil {
			p.dyn.mu.Unlock()
			return
		}
		c = &dynamicCounter{keys: keys, vec: vec}
		p.dyn.counters[name] = c
	}
	p.dyn.mu.Unlock()
	if !sameKeys(c.keys, keys) {
		return
	}
	c.vec.WithLabelValues(labelValues(c.keys, labels)...).Add(delta)
}

// GaugeSet implements Dynamic.
func (p *Prometheus) GaugeSet(name string, labels map[string]string, value float64) {
	keys := labelKeys(labels)
	p.dyn.mu.Lock()
	g, ok := p.dyn.gauges[name]
	if !ok {
		vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: externalName(name), Help: "Emitted by chain-commons."}, keys)
		if err := p.reg.Register(vec); err != nil {
			p.dyn.mu.Unlock()
			return
		}
		g = &dynamicGauge{keys: keys, vec: vec}
		p.dyn.gauges[name] = g
	}
	p.dyn.mu.Unlock()
	if !sameKeys(g.keys, keys) {
		return
	}
	g.vec.WithLabelValues(labelValues(g.keys, labels)...).Set(value)
}

// HistogramObserve implements Dynamic.
func (p *Prometheus) HistogramObserve(name string, labels map[string]string, value float64) {
	keys := labelKeys(labels)
	p.dyn.mu.Lock()
	h, ok := p.dyn.histograms[name]
	if !ok {
		vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: externalName(name), Help: "Emitted by chain-commons.", Buckets: prometheus.DefBuckets}, keys)
		if err := p.reg.Register(vec); err != nil {
			p.dyn.mu.Unlock()
			return
		}
		h = &dynamicHistogram{keys: keys, vec: vec}
		p.dyn.histograms[name] = h
	}
	p.dyn.mu.Unlock()
	if !sameKeys(h.keys, keys) {
		return
	}
	h.vec.WithLabelValues(labelValues(h.keys, labels)...).Observe(value)
}

func (*Noop) CounterAdd(string, map[string]string, float64)       {}
func (*Noop) GaugeSet(string, map[string]string, float64)         {}
func (*Noop) HistogramObserve(string, map[string]string, float64) {}

// Compile-time: both recorders satisfy Dynamic.
var (
	_ Dynamic = (*Prometheus)(nil)
	_ Dynamic = (*Noop)(nil)
)
