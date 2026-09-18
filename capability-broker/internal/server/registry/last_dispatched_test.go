package registry

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
)

// last_dispatched_at has to reach the wire, or the field the health
// verdict stopped abusing probed_at for is simply gone.
func TestWriteHealthResponse_CarriesLastDispatchedAt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	last := now.Add(-10 * time.Minute)
	rec := httptest.NewRecorder()
	WriteHealthResponse(rec, health.Response{
		BrokerStatus: "ready", GeneratedAt: now,
		Capabilities: []health.Snapshot{{
			ID: "openai:chat-completions", OfferingID: "qwen", BackendID: "ai2-rig|qwen-chat",
			Status: health.StatusReady, Reason: "certified", ProbeType: "attach",
			ProbedAt: now, StaleAfter: now.Add(30 * time.Second), ConsecutiveSuccesses: 1,
			LastDispatchedAt: last,
		}},
	}, nil)
	var out struct {
		Capabilities []struct {
			LastDispatchedAt time.Time `json:"last_dispatched_at"`
			ProbedAt         time.Time `json:"probed_at"`
			Backends         []struct {
				LastDispatchedAt time.Time `json:"last_dispatched_at"`
				SelectionReason  string    `json:"selection_reason"`
			} `json:"backends"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	c := out.Capabilities[0]
	if !c.Backends[0].LastDispatchedAt.Equal(last) || !c.LastDispatchedAt.Equal(last) {
		t.Fatalf("last_dispatched_at not on the wire: %s", rec.Body.String())
	}
	if !c.ProbedAt.Equal(now) || c.Backends[0].SelectionReason != "eligible" {
		t.Fatalf("verdict aged by the dispatch time: %s", rec.Body.String())
	}
}
