package configgen

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

func TestBuildIncludesPoolMetadata(t *testing.T) {
	cfg, err := config.Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    display_name: member-a
    backends:
      - id: backend-a
        transport: http
        url: http://backend
        auth: none
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor:
                type: openai-usage
                field: total_tokens
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              provider: vllm
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	model, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(model.Capabilities) != 1 {
		t.Fatalf("len(Capabilities) = %d, want 1", len(model.Capabilities))
	}

	poolBlock, ok := model.Capabilities[0].Extra["pool"].(map[string]any)
	if !ok {
		t.Fatalf("pool metadata missing from extra")
	}
	if got := poolBlock["member_eth_address"]; got != "0xabc" {
		t.Fatalf("member_eth_address = %v, want 0xabc", got)
	}
	if got := model.Capabilities[0].Extra["provider"]; got != "vllm" {
		t.Fatalf("provider = %v, want vllm", got)
	}
}

func TestGenerateYAMLIncludesCapabilityShape(t *testing.T) {
	cfg, err := config.Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    backends:
      - id: backend-a
        transport: http
        url: http://backend
        auth: none
        offerings:
          - capability_id: openai:embeddings
            offering_id: default
            interaction_mode: http-reqresp@v0
            work_unit:
              name: embeddings
              extractor:
                type: request-formula
                expression: "1"
            price:
              amount_wei: "7"
              per_units: 1
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	rendered, err := GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML() error = %v", err)
	}
	text := string(rendered)
	for _, needle := range []string{
		"capabilities:",
		"id: openai:embeddings",
		"offering_id: default",
		"member_backend_id: backend-a",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("rendered YAML missing %q\n%s", needle, text)
		}
	}
}

func TestGenerateYAMLAllowsRepeatedPublishedTupleAcrossMembers(t *testing.T) {
	cfg, err := config.Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xaaa
    backends:
      - id: backend-a
        transport: http
        url: http://backend-a
        offerings:
          - capability_id: openai:chat-completions
            offering_id: shared
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage, field: total_tokens }
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              openai: { model: llama-3-70b }
              provider: vllm
  - eth_address: 0xbbb
    backends:
      - id: backend-b
        transport: http
        url: http://backend-b
        offerings:
          - capability_id: openai:chat-completions
            offering_id: shared
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage, field: total_tokens }
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              openai: { model: llama-3-70b }
              provider: vllm
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	rendered, err := GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML() error = %v", err)
	}
	text := string(rendered)
	if count := strings.Count(text, "id: openai:chat-completions"); count != 2 {
		t.Fatalf("repeated published tuple render count = %d, want 2\n%s", count, text)
	}
	if !strings.Contains(text, "id: backend-a") || !strings.Contains(text, "id: backend-b") {
		t.Fatalf("rendered YAML missing backend ids\n%s", text)
	}
}

func TestBuildSortsCapabilitiesDeterministically(t *testing.T) {
	cfg, err := config.Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xbbb
    backends:
      - id: z-backend
        transport: http
        url: http://backend-b
        offerings:
          - capability_id: openai:embeddings
            offering_id: zeta
            interaction_mode: http-reqresp@v0
            work_unit:
              name: embeddings
              extractor: { type: request-formula, expression: "1" }
            price:
              amount_wei: "1"
              per_units: 1
  - eth_address: 0xaaa
    backends:
      - id: a-backend
        transport: http
        url: http://backend-a
        offerings:
          - capability_id: openai:chat-completions
            offering_id: alpha
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
          - capability_id: openai:embeddings
            offering_id: alpha
            interaction_mode: http-reqresp@v0
            work_unit:
              name: embeddings
              extractor: { type: request-formula, expression: "1" }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	model, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if got, want := len(model.Capabilities), 3; got != want {
		t.Fatalf("len(Capabilities) = %d, want %d", got, want)
	}

	got := []string{
		model.Capabilities[0].ID + "/" + model.Capabilities[0].OfferingID,
		model.Capabilities[1].ID + "/" + model.Capabilities[1].OfferingID,
		model.Capabilities[2].ID + "/" + model.Capabilities[2].OfferingID,
	}
	want := []string{
		"openai:chat-completions/alpha",
		"openai:embeddings/alpha",
		"openai:embeddings/zeta",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("capability[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
