package chaintesting

import (
	"sort"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
)

// FakeMetrics is a Recorder that captures every emission for test assertion.
type FakeMetrics struct {
	mu         sync.Mutex
	counters   map[seriesKey]float64
	gauges     map[seriesKey]float64
	histograms map[seriesKey][]float64
}

// NewFakeMetrics returns an empty FakeMetrics.
func NewFakeMetrics() *FakeMetrics {
	return &FakeMetrics{
		counters:   make(map[seriesKey]float64),
		gauges:     make(map[seriesKey]float64),
		histograms: make(map[seriesKey][]float64),
	}
}

// CounterAdd implements metrics.Recorder.
func (m *FakeMetrics) CounterAdd(name string, labels metrics.Labels, delta float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[makeKey(name, labels)] += delta
}

// GaugeSet implements metrics.Recorder.
func (m *FakeMetrics) GaugeSet(name string, labels metrics.Labels, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[makeKey(name, labels)] = value
}

// HistogramObserve implements metrics.Recorder.
func (m *FakeMetrics) HistogramObserve(name string, labels metrics.Labels, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := makeKey(name, labels)
	m.histograms[k] = append(m.histograms[k], value)
}

// CounterValue returns the cumulative counter value for (name, labels).
// Returns 0 if no observations have been recorded.
func (m *FakeMetrics) CounterValue(name string, labels metrics.Labels) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[makeKey(name, labels)]
}

// GaugeValue returns the latest gauge value for (name, labels).
func (m *FakeMetrics) GaugeValue(name string, labels metrics.Labels) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gauges[makeKey(name, labels)]
}

// HistogramObservations returns a copy of all observed values for (name,
// labels) in observation order.
func (m *FakeMetrics) HistogramObservations(name string, labels metrics.Labels) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.histograms[makeKey(name, labels)]
	out := make([]float64, len(v))
	copy(out, v)
	return out
}

// Reset clears all captured emissions. Useful between sub-tests.
func (m *FakeMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters = make(map[seriesKey]float64)
	m.gauges = make(map[seriesKey]float64)
	m.histograms = make(map[seriesKey][]float64)
}

// seriesKey is name + canonicalised labels.
type seriesKey struct {
	name   string
	labels string // sorted "k1=v1,k2=v2" form for map keying
}

func makeKey(name string, labels metrics.Labels) seriesKey {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b []byte
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, k...)
		b = append(b, '=')
		b = append(b, labels[k]...)
	}
	return seriesKey{name: name, labels: string(b)}
}

// Compile-time: FakeMetrics implements metrics.Recorder.
var _ metrics.Recorder = (*FakeMetrics)(nil)
