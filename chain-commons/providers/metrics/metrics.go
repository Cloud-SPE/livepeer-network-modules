// Package metrics provides the Recorder interface used by chain-commons
// services to emit metrics, plus a no-op default implementation.
//
// Daemons inject their own Prometheus-decorating Recorder in their
// cmd/<daemon>/main.go. chain-commons does NOT import
// github.com/prometheus/client_golang. This is the swap point if the
// project ever migrates to OpenTelemetry — service code never changes.
package metrics

// Recorder is the metrics surface. All chain-commons emissions go through it.
type Recorder interface {
	CounterAdd(name string, labels Labels, delta float64)
	GaugeSet(name string, labels Labels, value float64)
	HistogramObserve(name string, labels Labels, value float64)
}

// Labels is a map of label name → label value.
type Labels map[string]string

// NoOp returns a Recorder that discards all emissions. Useful for tests
// that don't care about metric output and for daemons during preflight
// before the real Recorder is wired in.
func NoOp() Recorder { return noOpRecorder{} }

type noOpRecorder struct{}

func (noOpRecorder) CounterAdd(string, Labels, float64)        {}
func (noOpRecorder) GaugeSet(string, Labels, float64)          {}
func (noOpRecorder) HistogramObserve(string, Labels, float64)  {}
