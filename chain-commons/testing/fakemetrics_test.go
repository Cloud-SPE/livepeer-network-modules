package chaintesting

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
)

func TestFakeMetrics_Counter(t *testing.T) {
	m := NewFakeMetrics()
	m.CounterAdd("foo_total", metrics.Labels{"a": "1"}, 1)
	m.CounterAdd("foo_total", metrics.Labels{"a": "1"}, 2)
	if got := m.CounterValue("foo_total", metrics.Labels{"a": "1"}); got != 3 {
		t.Errorf("counter = %v, want 3", got)
	}
}

func TestFakeMetrics_LabelsAreCanonicalized(t *testing.T) {
	m := NewFakeMetrics()
	m.CounterAdd("foo_total", metrics.Labels{"a": "1", "b": "2"}, 1)
	// Different map iteration order should produce same key.
	m.CounterAdd("foo_total", metrics.Labels{"b": "2", "a": "1"}, 1)
	if got := m.CounterValue("foo_total", metrics.Labels{"a": "1", "b": "2"}); got != 2 {
		t.Errorf("counter (canonicalised labels) = %v, want 2", got)
	}
}

func TestFakeMetrics_Gauge(t *testing.T) {
	m := NewFakeMetrics()
	m.GaugeSet("active", nil, 5)
	m.GaugeSet("active", nil, 7)
	if got := m.GaugeValue("active", nil); got != 7 {
		t.Errorf("gauge = %v, want 7", got)
	}
}

func TestFakeMetrics_Histogram(t *testing.T) {
	m := NewFakeMetrics()
	m.HistogramObserve("dur", nil, 0.5)
	m.HistogramObserve("dur", nil, 1.0)
	m.HistogramObserve("dur", nil, 1.5)

	obs := m.HistogramObservations("dur", nil)
	if len(obs) != 3 || obs[0] != 0.5 || obs[2] != 1.5 {
		t.Errorf("histogram observations = %v", obs)
	}
}

func TestFakeMetrics_Reset(t *testing.T) {
	m := NewFakeMetrics()
	m.CounterAdd("c", nil, 1)
	m.GaugeSet("g", nil, 1)
	m.HistogramObserve("h", nil, 1)
	m.Reset()
	if m.CounterValue("c", nil) != 0 {
		t.Errorf("counter not reset")
	}
	if m.GaugeValue("g", nil) != 0 {
		t.Errorf("gauge not reset")
	}
	if len(m.HistogramObservations("h", nil)) != 0 {
		t.Errorf("histogram not reset")
	}
}

func TestFakeMetrics_DistinctSeries(t *testing.T) {
	m := NewFakeMetrics()
	m.CounterAdd("c", metrics.Labels{"k": "a"}, 1)
	m.CounterAdd("c", metrics.Labels{"k": "b"}, 2)
	if got := m.CounterValue("c", metrics.Labels{"k": "a"}); got != 1 {
		t.Errorf("series a = %v", got)
	}
	if got := m.CounterValue("c", metrics.Labels{"k": "b"}); got != 2 {
		t.Errorf("series b = %v", got)
	}
}
