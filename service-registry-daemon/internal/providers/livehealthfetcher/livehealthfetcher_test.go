package livehealthfetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetch_AcceptsCurrentBrokerHealthShape(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/health" {
			t.Fatalf("path = %q, want /registry/health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"broker_status":"ready",
			"generated_at":"2026-05-19T16:13:33.398750396Z",
			"capabilities":[
				{
					"id":"openai:chat-completions",
					"offering_id":"vllm-qwen3.6-27b-default",
					"status":"ready",
					"reason":"probe_ok",
					"probe_type":"http-openai-model-ready",
					"probed_at":"2026-05-19T16:13:32.573470837Z",
					"stale_after":"2026-05-19T16:13:47.573470837Z",
					"backends":[
						{
							"backend_id":"http://vllm_model_runner:8000/v1/chat/completions",
							"status":"ready",
							"reason":"probe_ok",
							"probe_type":"http-openai-model-ready",
							"probed_at":"2026-05-19T16:13:32.573470837Z",
							"stale_after":"2026-05-19T16:13:47.573470837Z",
							"consecutive_successes":331,
							"selection_eligible":true,
							"selection_weight":150,
							"selection_reason":"eligible"
						}
					],
					"metadata":{
						"provider":"vllm",
						"applicable":true,
						"last_result":"enriched"
					}
				}
			]
		}`))
	}))
	defer srv.Close()

	fetcher := New(5 * time.Second)
	snap, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snap == nil {
		t.Fatal("Fetch() returned nil snapshot")
	}
	if snap.BrokerStatus != "ready" {
		t.Fatalf("broker_status = %q, want ready", snap.BrokerStatus)
	}
	if len(snap.Capabilities) != 1 {
		t.Fatalf("capabilities len = %d, want 1", len(snap.Capabilities))
	}
	cap := snap.Capabilities[0]
	if cap.ID != "openai:chat-completions" || cap.OfferingID != "vllm-qwen3.6-27b-default" {
		t.Fatalf("capability tuple = %q/%q", cap.ID, cap.OfferingID)
	}
	if cap.ProbeType != "http-openai-model-ready" {
		t.Fatalf("probe_type = %q, want http-openai-model-ready", cap.ProbeType)
	}
	if len(cap.Backends) != 1 || !cap.Backends[0].SelectionEligible {
		t.Fatalf("backend selection metadata not decoded: %+v", cap.Backends)
	}
}
