package metrics

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
)

func TestScrapeUpdatesUptime(t *testing.T) {
	recorder := metrics.NewPrometheus(metrics.PrometheusConfig{})
	listener, err := NewListener(Config{Addr: "127.0.0.1:0", Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	listener.srv.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if value, ok := strings.CutPrefix(line, "livepeer_registry_uptime_seconds "); ok {
			seconds, err := strconv.ParseFloat(value, 64)
			if err != nil || seconds <= 0 {
				t.Fatalf("uptime was not updated: %q", line)
			}
			return
		}
	}
	t.Fatal("uptime metric missing")
}
