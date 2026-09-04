package chaincommons

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	cmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

func TestLogger_RedactsURLFieldsToHost(t *testing.T) {
	var buf bytes.Buffer
	l := Logger(slog.New(slog.NewTextHandler(&buf, nil)))
	l.Warn("rpc.endpoint_failed",
		logger.String("url", "https://user:pw@rpc.example.com/v2/SECRETKEY"),
		logger.String("class", "transient"))
	l.With(logger.String("url", "https://rpc2.example.com/KEY2")).Info("hi")
	out := buf.String()
	if strings.Contains(out, "SECRETKEY") || strings.Contains(out, "KEY2") || strings.Contains(out, "user:pw") {
		t.Fatalf("secret leaked into log: %s", out)
	}
	if !strings.Contains(out, "url=rpc.example.com") || !strings.Contains(out, "url=rpc2.example.com") {
		t.Fatalf("host not logged: %s", out)
	}
	if !strings.Contains(out, "class=transient") {
		t.Fatalf("other fields must pass through: %s", out)
	}
	l.Debug("d")
	l.Error("e", logger.Err(nil))
}

func TestLogger_RedactsURLsInsideErrorText(t *testing.T) {
	var buf bytes.Buffer
	l := Logger(slog.New(slog.NewTextHandler(&buf, nil)))
	l.Info("rpc.endpoint_failed",
		logger.Err(errors.New(`Post "https://rpc.example.com/v2/SECRETKEY": dial tcp: connection refused`)),
		logger.String("detail", "tried http://other.example.com/KEY2 next"),
		logger.Int("attempt", 3))
	out := buf.String()
	if strings.Contains(out, "SECRETKEY") || strings.Contains(out, "KEY2") {
		t.Fatalf("secret leaked through error text: %s", out)
	}
	if !strings.Contains(out, "rpc.example.com") || !strings.Contains(out, "other.example.com") || !strings.Contains(out, "attempt=3") {
		t.Fatalf("hosts and other fields must survive: %s", out)
	}
	if got := RedactURLs("no urls here"); got != "no urls here" {
		t.Errorf("RedactURLs changed a plain string: %q", got)
	}
}

func TestLogger_NilFallsBackToDefault(t *testing.T) {
	if Logger(nil) == nil {
		t.Fatal("nil logger must yield a usable adapter")
	}
}

func TestRecorder_ForwardsToDynamic(t *testing.T) {
	p := metrics.NewPrometheus()
	r := Recorder(p)
	r.CounterAdd("livepeer_chain_rpc_calls_total", cmetrics.Labels{"method": "ChainID", "result": "ok"}, 1)
	r.GaugeSet("livepeer_chain_g", nil, 3)
	r.HistogramObserve("livepeer_chain_h", nil, 0.5)
	mf, err := p.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range mf {
		names[m.GetName()] = true
	}
	for _, want := range []string{"livepeer_chain_rpc_calls_total", "livepeer_chain_g", "livepeer_chain_h"} {
		if !names[want] {
			t.Errorf("series %s not registered", want)
		}
	}
}

type notDynamic struct{ metrics.Recorder }

func TestRecorder_NonDynamicRecorderIsNoop(t *testing.T) {
	r := Recorder(notDynamic{})
	r.CounterAdd("x", nil, 1) // must not panic
	if Recorder(nil) == nil {
		t.Fatal("nil recorder must yield a no-op")
	}
}

func TestHost(t *testing.T) {
	if got := Host("https://user:pw@rpc.example.com/v2/SECRETKEY"); got != "rpc.example.com" {
		t.Fatalf("Host = %q", got)
	}
	if got := Host("::not a url"); got != "<invalid-url>" {
		t.Fatalf("Host invalid = %q", got)
	}
}
