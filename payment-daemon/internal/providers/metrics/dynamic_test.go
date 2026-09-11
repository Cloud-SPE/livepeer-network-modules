package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func scrape(t *testing.T, p *Prometheus) string {
	t.Helper()
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestDynamic_RegistersChainCommonsSeriesByName(t *testing.T) {
	p := NewPrometheus()
	p.CounterAdd("livepeer_chain_rpc_calls_total", map[string]string{"method": "ChainID", "result": "ok"}, 1)
	p.CounterAdd("livepeer_chain_rpc_calls_total", map[string]string{"method": "ChainID", "result": "ok"}, 2)
	p.GaugeSet("livepeer_chain_something", map[string]string{"role": "primary"}, 7)
	p.HistogramObserve("livepeer_chain_latency_seconds", nil, 0.25)

	body := scrape(t, p)
	for _, want := range []string{
		`livepeer_chain_rpc_calls_total{method="ChainID",result="ok"} 3`,
		`livepeer_chain_something{role="primary"} 7`,
		`livepeer_chain_latency_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

func TestDynamic_UnprefixedNameFiledUnderDaemonNamespace(t *testing.T) {
	p := NewPrometheus()
	p.CounterAdd("orphan_total", nil, 1)
	if body := scrape(t, p); !strings.Contains(body, "livepeer_payment_orphan_total 1") {
		t.Fatalf("unprefixed name not namespaced:\n%s", body)
	}
}

func TestDynamic_LabelSetDriftIsDroppedNotPanicked(t *testing.T) {
	p := NewPrometheus()
	p.CounterAdd("livepeer_chain_drift_total", map[string]string{"a": "1"}, 1)
	p.CounterAdd("livepeer_chain_drift_total", map[string]string{"b": "2"}, 1)  // different keys
	p.CounterAdd("livepeer_chain_drift_total", map[string]string{"a": "1"}, -1) // negative delta
	body := scrape(t, p)
	if !strings.Contains(body, `livepeer_chain_drift_total{a="1"} 1`) {
		t.Fatalf("first label set lost:\n%s", body)
	}
	if strings.Contains(body, `b="2"`) {
		t.Fatalf("drifted label set must be dropped:\n%s", body)
	}
}

func TestDynamic_NoopAcceptsEverything(t *testing.T) {
	n := NewNoop()
	n.CounterAdd("x", nil, 1)
	n.GaugeSet("x", nil, 1)
	n.HistogramObserve("x", nil, 1)
}
