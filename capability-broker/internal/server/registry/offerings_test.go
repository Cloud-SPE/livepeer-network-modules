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

// A runner's declared session_params shape reaches gateways verbatim so
// they can validate before opening, instead of learning the requirement
// from a create-time failure after payment was validated. The broker
// relays it and never enforces it.
func TestBuildOfferings_RelaysSessionParamsSchema(t *testing.T) {
	schema := json.RawMessage(`{"required":["room_name"]}`)
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0xabc"},
		Capabilities: []config.Capability{{
			ID: "cap:sess", OfferingID: "default", Protocol: "paid-session/v1",
			Session: &config.SessionCap{
				DescriptorSchema:    "sfu-room/v1",
				SessionParamsSchema: schema,
			},
			WorkUnit: config.WorkUnit{Name: "participant_seconds"},
			Price:    config.Price{AmountWei: "10", PerUnits: 1},
		}},
	}
	got := BuildOfferings(cfg, nil)
	sess := got.Capabilities[0].Session
	if sess == nil || len(sess.SessionParamsSchema) == 0 {
		t.Fatalf("session params schema not advertised: %+v", sess)
	}
	if string(sess.SessionParamsSchema) != string(schema) {
		t.Fatalf("schema altered in transit: %s", sess.SessionParamsSchema)
	}

	// A capability whose runner declared nothing advertises nothing —
	// the field must stay absent rather than becoming empty JSON.
	cfg.Capabilities[0].Session.SessionParamsSchema = nil
	if s := BuildOfferings(cfg, nil).Capabilities[0].Session.SessionParamsSchema; len(s) != 0 {
		t.Fatalf("absent schema became %q", s)
	}
}

// A transcription offering advertises its estimator, because a caller
// funding a multipart upload cannot derive a ceiling from the request
// parameters the way a JSON workload can.
func TestOfferingAdvertisesClientEstimator(t *testing.T) {
	est := estimatorFor(map[string]any{"type": "multipart-audio-duration"})
	if est == nil {
		t.Fatal("no estimator advertised for multipart-audio-duration")
	}
	if est.ID != "multipart-audio-duration/v1" {
		t.Fatalf("id = %q", est.ID)
	}
	if est.Rounding != "ceil-to-whole-seconds" {
		t.Fatalf("rounding = %q; a client must round the same way the seller bills", est.Rounding)
	}
	if est.Exactness != "exact-or-reject" {
		t.Fatalf("exactness = %q; a ceiling built on an estimate underfunds or overcharges",
			est.Exactness)
	}
	// The fixtures are the contract. Two independently-owned
	// implementations agree because they run the same vectors, so a
	// disagreement surfaces as a failing test rather than as a
	// settlement that exceeds the ceiling a caller funded.
	if est.Fixtures == "" {
		t.Fatal("a client told to reproduce an estimator needs the fixtures that pin it")
	}
	// And no package. The field names a canonical client library a
	// caller can install; there is no longer one, and the name this used
	// to carry was never published — so a caller that trusted the field
	// got a 404 for its trouble. Absent is the honest answer, and this
	// asserts it rather than letting a well-meaning re-add slip back in.
	if est.Package != "" {
		t.Fatalf("package = %q; implementations are independently owned, so there is no "+
			"canonical client library to name", est.Package)
	}
}

// Everything else stays unadvertised. How a seller counts is its own
// business and no counterparty gates on it; the exception exists only
// where a client genuinely has to reproduce the number.
func TestOtherExtractorsAdvertiseNoEstimator(t *testing.T) {
	for _, typ := range []string{"openai-usage", "request-formula", "bytes-counted",
		"seconds-elapsed", "response-jsonpath", ""} {
		if est := estimatorFor(map[string]any{"type": typ}); est != nil {
			t.Fatalf("extractor %q advertised estimator %q; extractors are seller-side "+
				"implementation detail unless a client must reproduce them", typ, est.ID)
		}
	}
}
