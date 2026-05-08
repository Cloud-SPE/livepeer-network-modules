package metrics

import "testing"

func TestNoOp_DoesNotPanic(t *testing.T) {
	r := NoOp()
	r.CounterAdd("name", Labels{"a": "b"}, 1)
	r.GaugeSet("name", Labels{"a": "b"}, 1)
	r.HistogramObserve("name", Labels{"a": "b"}, 1)
}

func TestNoOp_AcceptsNilLabels(t *testing.T) {
	r := NoOp()
	r.CounterAdd("name", nil, 1)
	r.GaugeSet("name", nil, 1)
	r.HistogramObserve("name", nil, 1)
}
