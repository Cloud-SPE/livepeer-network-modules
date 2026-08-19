package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

type stubOverlaySource struct {
	extra map[string]map[string]any
}

func (s stubOverlaySource) ExtraFor(capabilityID, offeringID string) map[string]any {
	return s.extra[capabilityID+"|"+offeringID]
}

func TestBuildOfferings_MergesOverlayWithoutMutatingConfig(t *testing.T) {
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []config.Capability{{
			ID:         "livepeer:vtuber-session",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &config.JobCapability{Transports: []string{"unary"}},
			WorkUnit:   config.WorkUnit{Name: "seconds"},
			Price:      config.Price{AmountWei: "1", PerUnits: 1},
			Extra: map[string]any{
				"provider": "vtuber-runner",
				"vtuber": map[string]any{
					"task": "session",
				},
			},
		}},
	}

	payload := buildOfferings(cfg, stubOverlaySource{
		extra: map[string]map[string]any{
			"livepeer:vtuber-session|default": {
				"vtuber": map[string]any{
					"control_schema": "vtuber-control/v1",
					"media_schema":   "trickle-segment-stream/v1",
				},
			},
		},
	})

	if len(payload.Capabilities) != 1 {
		t.Fatalf("capabilities count = %d; want 1", len(payload.Capabilities))
	}
	vtuber, ok := payload.Capabilities[0].Extra["vtuber"].(map[string]any)
	if !ok {
		t.Fatalf("published extra.vtuber missing: %#v", payload.Capabilities[0].Extra["vtuber"])
	}
	if got := vtuber["control_schema"]; got != "vtuber-control/v1" {
		t.Fatalf("published control_schema = %#v; want vtuber-control/v1", got)
	}
	if _, exists := cfg.Capabilities[0].Extra["control_schema"]; exists {
		t.Fatal("config extra mutated at root")
	}
	baseVTuber, ok := cfg.Capabilities[0].Extra["vtuber"].(map[string]any)
	if !ok {
		t.Fatalf("config extra.vtuber missing: %#v", cfg.Capabilities[0].Extra["vtuber"])
	}
	if _, exists := baseVTuber["control_schema"]; exists {
		t.Fatal("config extra.vtuber should not be mutated by overlay merge")
	}
}

// TestBuildOfferings_EmitsEmptyConstraintsBlock guarantees that the
// public /registry/offerings payload always carries a `constraints`
// field, even when none are configured. Downstream resolvers hash the
// canonical constraints bytes; an absent block previously produced a
// nil constraint_fingerprint that failed request-path filtering.
func TestBuildOfferings_EmitsEmptyConstraintsBlock(t *testing.T) {
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []config.Capability{{
			ID:         "rerank",
			OfferingID: "zerank-2-default",
			Protocol:   "paid-job/v1",
			Job:        &config.JobCapability{Transports: []string{"unary"}},
			WorkUnit:   config.WorkUnit{Name: "requests"},
			Price:      config.Price{AmountWei: "1", PerUnits: 1},
		}},
	}

	payload := buildOfferings(cfg, nil)
	if len(payload.Capabilities) != 1 {
		t.Fatalf("capabilities count = %d; want 1", len(payload.Capabilities))
	}
	if payload.Capabilities[0].Constraints == nil {
		t.Fatal("constraints is nil; want empty map")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"constraints":{}`) {
		t.Fatalf("marshalled payload missing empty constraints block: %s", raw)
	}
}

func TestBuildOfferings_DedupesRepeatedPublishedTuple(t *testing.T) {
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &config.JobCapability{Transports: []string{"unary"}},
				WorkUnit:   config.WorkUnit{Name: "tokens"},
				Price:      config.Price{AmountWei: "1", PerUnits: 1},
				Backend:    config.Backend{ID: "a", Transport: "http", URL: "http://backend-a"},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &config.JobCapability{Transports: []string{"unary"}},
				WorkUnit:   config.WorkUnit{Name: "tokens"},
				Price:      config.Price{AmountWei: "1", PerUnits: 1},
				Backend:    config.Backend{ID: "b", Transport: "http", URL: "http://backend-b"},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
		},
	}

	payload := buildOfferings(cfg, nil)
	if got := len(payload.Capabilities); got != 1 {
		t.Fatalf("capabilities count = %d; want 1", got)
	}
}
