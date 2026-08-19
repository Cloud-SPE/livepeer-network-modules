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

// TestBuildOfferings_EmitsDeclaredAxes pins the contract the coordinator
// depends on: every paid-* offering advertises exactly one axes object,
// in the manifest's published vocabulary. Emitting protocol without axes
// produces manifests that fail schema validation downstream.
func TestBuildOfferings_EmitsDeclaredAxes(t *testing.T) {
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0xabc"},
		Capabilities: []config.Capability{
			{
				ID: "cap:job", OfferingID: "default", Protocol: "paid-job/v1",
				Job:      &config.JobCapability{Transports: []string{"unary", "stream"}},
				WorkUnit: config.WorkUnit{Name: "tokens"},
				Price:    config.Price{AmountWei: "1", PerUnits: 1},
			},
			{
				ID: "cap:sess", OfferingID: "default", Protocol: "paid-session/v1",
				Session: &config.SessionCap{
					DescriptorSchema:     "sfu-room/v1",
					Heartbeat:            config.SessionHeartbeat{IntervalSeconds: 5, MissedThreshold: 4},
					LeaseMaxSeconds:      1800,
					RunwayIncrementUnits: 500,
				},
				WorkUnit: config.WorkUnit{Name: "participant_minutes"},
				Price:    config.Price{AmountWei: "10", PerUnits: 1},
			},
		},
	}
	got := BuildOfferings(cfg, nil)
	if len(got.Capabilities) != 2 {
		t.Fatalf("want 2 capabilities, got %d", len(got.Capabilities))
	}

	job := got.Capabilities[0]
	if job.Session != nil {
		t.Fatal("paid-job offering carries session axes")
	}
	if job.Job == nil || len(job.Job.Transports) != 2 || job.Job.Transports[0] != "unary" {
		t.Fatalf("job axes wrong: %+v", job.Job)
	}

	sess := got.Capabilities[1]
	if sess.Job != nil {
		t.Fatal("paid-session offering carries job axes")
	}
	if sess.Session == nil {
		t.Fatal("paid-session offering has no session axes")
	}
	if sess.Session.DescriptorSchema != "sfu-room/v1" {
		t.Fatalf("descriptor_schema %q", sess.Session.DescriptorSchema)
	}
	// Required-by-schema fields must be present even when the operator
	// left them unset — that is what the Advertised* defaults are for.
	if sess.Session.Metering != "runner-reported" || sess.Session.Attachment != "external" ||
		sess.Session.Refill != "extensible" {
		t.Fatalf("defaulted axes wrong: %+v", sess.Session)
	}
	if sess.Session.Heartbeat == nil || sess.Session.Heartbeat.MissedThreshold != 4 {
		t.Fatalf("heartbeat %+v", sess.Session.Heartbeat)
	}
	// Host config's flat lease cap becomes the manifest's lease object.
	if sess.Session.Lease == nil || sess.Session.Lease.Policy != "funding-tracking" ||
		sess.Session.Lease.MaxSeconds != 1800 {
		t.Fatalf("lease %+v", sess.Session.Lease)
	}
	if sess.Session.RunwayIncrementUnits != 500 {
		t.Fatalf("runway increment %d", sess.Session.RunwayIncrementUnits)
	}
}
