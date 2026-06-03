package brokerrender

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRenderUsesActiveAssignmentsOnly(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusActive,
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
		}},
		Backends: []types.MemberBackend{{
			ID:        "backend-1",
			MemberID:  "member-1",
			Transport: "http",
			URL:       "http://backend",
			Auth:      config.AuthConfig{Method: "none"},
			Status:    types.BackendStatusActive,
			HealthProbe: config.HealthProbe{
				Type: "http-status",
			},
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
		}},
	}
	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(got.Model.Capabilities) != 1 {
		t.Fatalf("len(Capabilities) = %d, want 1", len(got.Model.Capabilities))
	}
	if !strings.Contains(string(got.ConfigYAML), "member_eth_address") {
		t.Fatalf("rendered YAML missing pool metadata: %s", string(got.ConfigYAML))
	}
}

func TestRenderConnectedTemplateAssignments(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-chat",
			CapabilityID:    "openai:chat-completions",
			OfferingID:      "default",
			InteractionMode: "http-stream@v0",
			WorkUnit:        config.WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "response-header"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusActive,
		}},
		PoolMembers: []types.PoolMember{{
			ID:         "0xmember",
			EthAddress: "0xmember",
			PayoutMode: "eth",
			Status:     types.MemberStatusActive,
		}},
		HostEnrollments: []types.HostEnrollment{{
			ID:                      "host-1",
			MemberEthAddress:        "0xmember",
			BrokerSessionCredential: "worker-secret",
			Status:                  types.HostEnrollmentActive,
		}},
		HardwareUnits: []types.HardwareUnit{{
			ID:               "gpu-1",
			EnrollmentID:     "host-1",
			MemberEthAddress: "0xmember",
			GPUUUID:          "GPU-1",
			State:            types.HardwareUnitActive,
		}},
		Templates: []types.TemplateCatalogEntry{{
			ID:                 "chat-4090",
			CapabilityID:       "openai:chat-completions",
			OfferingID:         "default",
			InteractionMode:    "http-stream@v0",
			MaxInFlightDefault: 2,
			QueueLimitDefault:  4,
			Status:             types.TemplateStatusActive,
		}},
		TemplateAssignments: []types.TemplateAssignment{{
			ID:               "assign-chat-1",
			HardwareUnitID:   "gpu-1",
			HostEnrollmentID: "host-1",
			MemberEthAddress: "0xmember",
			TemplateID:       "chat-4090",
			State:            types.TemplateAssignmentActive,
		}},
	}
	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	raw := string(got.ConfigYAML)
	for _, want := range []string{
		"worker://assign-chat-1",
		"worker_session_credential: worker-secret",
		"host_enrollment_id: host-1",
		"hardware_unit_id: gpu-1",
		"gpu_uuid: GPU-1",
		"template_id: chat-4090",
		"max_in_flight: 2",
		"queue_limit: 4",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("rendered YAML missing %q:\n%s", want, raw)
		}
	}
}

func TestRenderOmitsInactiveEntities(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusDisabled,
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusSuspended,
		}},
		Backends: []types.MemberBackend{{
			ID:        "backend-1",
			MemberID:  "member-1",
			Transport: "http",
			URL:       "http://backend",
			Status:    types.BackendStatusDisabled,
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusDisabled,
		}},
	}
	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(got.Model.Capabilities) != 0 {
		t.Fatalf("len(Capabilities) = %d, want 0", len(got.Model.Capabilities))
	}
}

func TestRenderDeterministicRevision(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusActive,
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
		}},
		Backends: []types.MemberBackend{{
			ID:        "backend-1",
			MemberID:  "member-1",
			Transport: "http",
			URL:       "http://backend",
			Status:    types.BackendStatusActive,
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
		}},
	}
	first, err := Render(input)
	if err != nil {
		t.Fatalf("Render(first) error = %v", err)
	}
	second, err := Render(input)
	if err != nil {
		t.Fatalf("Render(second) error = %v", err)
	}
	if first.Revision != second.Revision || string(first.ConfigYAML) != string(second.ConfigYAML) {
		t.Fatalf("render not deterministic")
	}
}

// TestRenderEmitsEmptyConstraintsBlock locks in the contract that the
// rendered broker config always carries a `constraints:` key for each
// capability, even when the operator left constraints empty. Resolvers
// downstream fingerprint the canonical constraints bytes; an absent
// block used to produce a nil fingerprint and a "no route candidates"
// failure.
func TestRenderEmitsEmptyConstraintsBlock(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "zerank-2-default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusActive,
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
		}},
		Backends: []types.MemberBackend{{
			ID:        "backend-1",
			MemberID:  "member-1",
			Transport: "http",
			URL:       "http://backend",
			Status:    types.BackendStatusActive,
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
		}},
	}

	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(got.Model.Capabilities) != 1 {
		t.Fatalf("len(Capabilities) = %d, want 1", len(got.Model.Capabilities))
	}
	if got.Model.Capabilities[0].Constraints == nil {
		t.Fatalf("Constraints is nil; want empty map")
	}
	if !strings.Contains(string(got.ConfigYAML), "constraints: {}") {
		t.Fatalf("rendered YAML missing empty constraints block: %s", string(got.ConfigYAML))
	}
}

func TestRenderRequestFormulaIncludesEmptyFieldsMap(t *testing.T) {
	input := RenderInput{
		Bootstrap: BootstrapBrokerSettings{
			Identity: config.Identity{OrchEthAddress: "0xorch"},
		},
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: config.WorkUnit{
				Name: "requests",
				Extractor: map[string]any{
					"type":       "request-formula",
					"expression": "1",
				},
			},
			Price:  config.Price{AmountWei: "1", PerUnits: 1},
			Status: types.OfferStatusActive,
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xmember",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
		}},
		Backends: []types.MemberBackend{{
			ID:        "backend-1",
			MemberID:  "member-1",
			Transport: "http",
			URL:       "http://backend",
			Status:    types.BackendStatusActive,
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
		}},
	}

	got, err := Render(input)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(string(got.ConfigYAML), "fields: {}") {
		t.Fatalf("rendered YAML missing empty request-formula fields map: %s", string(got.ConfigYAML))
	}
	fields, ok := got.Model.Capabilities[0].WorkUnit.Extractor["fields"].(map[string]any)
	if !ok || len(fields) != 0 {
		t.Fatalf("rendered model fields = %#v, want empty map", got.Model.Capabilities[0].WorkUnit.Extractor["fields"])
	}
}
