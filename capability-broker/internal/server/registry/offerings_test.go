package registry

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
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
			ID:              "livepeer:vtuber-session",
			OfferingID:      "default",
			InteractionMode: "session-control-plus-media@v0",
			WorkUnit:        config.WorkUnit{Name: "seconds"},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
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
