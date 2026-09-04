package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	ccmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
)

func TestChainRecorderRegistersLazilyAndAccumulates(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewChainRecorder(reg)
	labels := ccmetrics.Labels{"endpoint": "primary", "outcome": "ok"}
	r.CounterAdd("livepeer_chain_rpc_calls_total", labels, 1)
	r.CounterAdd("livepeer_chain_rpc_calls_total", labels, 2)
	r.GaugeSet("livepeer_chain_rpc_open_circuits", ccmetrics.Labels{"role": "backup"}, 1)
	r.HistogramObserve("livepeer_chain_rpc_call_seconds", ccmetrics.Labels{"method": "eth_call"}, 0.25)

	if got := testutil.ToFloat64(r.counters["livepeer_chain_rpc_calls_total"].WithLabelValues("primary", "ok")); got != 3 {
		t.Fatalf("counter = %v, want 3", got)
	}
	if got := testutil.ToFloat64(r.gauges["livepeer_chain_rpc_open_circuits"].WithLabelValues("backup")); got != 1 {
		t.Fatalf("gauge = %v, want 1", got)
	}
	if n := testutil.CollectAndCount(r.histograms["livepeer_chain_rpc_call_seconds"]); n != 1 {
		t.Fatalf("histogram series = %d, want 1", n)
	}
}

func TestChainRecorderDropsMismatchedLabelSet(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewChainRecorder(reg)
	r.CounterAdd("x_total", ccmetrics.Labels{"a": "1"}, 1)
	r.CounterAdd("x_total", ccmetrics.Labels{"b": "1"}, 1) // dropped, not a panic
	if got := testutil.ToFloat64(r.counters["x_total"].WithLabelValues("1")); got != 1 {
		t.Fatalf("counter = %v, want 1", got)
	}
}

func TestChainRecorderReusesAlreadyRegisteredCollector(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := NewChainRecorder(reg)
	b := NewChainRecorder(reg)
	a.CounterAdd("y_total", ccmetrics.Labels{"k": "v"}, 1)
	b.CounterAdd("y_total", ccmetrics.Labels{"k": "v"}, 1)
	if got := testutil.ToFloat64(b.counters["y_total"].WithLabelValues("v")); got != 2 {
		t.Fatalf("shared counter = %v, want 2", got)
	}
}

func TestChainRecorderNilRegistryUsesDefault(t *testing.T) {
	if NewChainRecorder(nil).reg != prometheus.DefaultRegisterer {
		t.Fatal("nil registry did not fall back to the default registerer")
	}
}
