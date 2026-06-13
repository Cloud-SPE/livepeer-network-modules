package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status=%d", rec.Code)
	}
	return rec.Body.String()
}

func TestMetrics_ExpositionAfterAgentActivity(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-20*time.Hour), h.now.Add(4*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))

	h.agent.Cycle(context.Background()) // pull + auto-sign renewal
	h.agent.Cycle(context.Background()) // 304 poll

	h.agent.metrics.now = h.agent.now
	body := scrape(t, h.agent.Metrics())
	for _, want := range []string{
		`secure_orch_agent_polls_total{result="pulled"} 1`,
		`secure_orch_agent_polls_total{result="not_modified"} 1`,
		`secure_orch_agent_decisions_total{action="auto_sign"} 1`,
		"secure_orch_agent_held_queue_depth 0",
		"secure_orch_agent_published_manifest_expiry_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "secure_orch_agent_last_publish_confirm_timestamp_seconds 0\n") {
		t.Fatalf("publish confirm timestamp should be set:\n%s", body)
	}
}

func TestMetrics_HeldDepthGauge(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-4*time.Hour), h.now.Add(20*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", true))

	h.agent.Cycle(context.Background())

	if body := scrape(t, h.agent.Metrics()); !strings.Contains(body, "secure_orch_agent_held_queue_depth 1") {
		t.Fatalf("held depth gauge not 1:\n%s", body)
	}
}

func TestAgent_ExpiryWarningFiresOncePerCrossing(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	// Published manifest: 24h TTL. Renewal threshold fraction 0.3333
	// → warn at half the buffer: 4h remaining. Seed at 3h remaining;
	// candidate pull would renew, so park a held-critical candidate
	// instead to keep the loop wedged.
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-21*time.Hour), h.now.Add(3*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", true))

	h.agent.Cycle(context.Background())
	h.agent.Cycle(context.Background())

	if countKind(h.alerts, "manifest_expiry_warning") != 1 {
		t.Fatalf("expiry warning should fire once per crossing: %v", h.alerts)
	}
}

func TestWebhookAlert_PostsSlackCompatibleJSON(t *testing.T) {
	var got map[string]any
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("webhook body: %v", err)
		}
		received <- struct{}{}
	}))
	defer srv.Close()

	alert := NewWebhookAlert(srv.URL, slog.Default())
	alert("held", map[string]any{"etag": "x"})

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook never delivered")
	}
	if got["kind"] != "held" {
		t.Fatalf("kind=%v", got["kind"])
	}
	if _, ok := got["text"].(string); !ok {
		t.Fatalf("missing Slack-compatible text field: %v", got)
	}
	if NewWebhookAlert("", slog.Default()) != nil {
		t.Fatal("empty URL must disable the webhook")
	}
}
