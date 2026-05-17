package config

import "testing"

func TestLoadDefaultsAndValidation(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    backends:
      - id: b1
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
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Listen.Paid; got != ":8080" {
		t.Fatalf("Listen.Paid = %q, want %q", got, ":8080")
	}
	if got := cfg.Listen.Metrics; got != ":9090" {
		t.Fatalf("Listen.Metrics = %q, want %q", got, ":9090")
	}
	if got := cfg.Members[0].PayoutMode; got != "onchain" {
		t.Fatalf("PayoutMode = %q, want onchain", got)
	}
}

func TestLoadAllowsDuplicatePublishedTuplesAcrossMembers(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    backends:
      - id: b1
        transport: http
        url: http://backend1
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
  - eth_address: 0xdef
    backends:
      - id: b2
        transport: http
        url: http://backend2
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := len(cfg.Members); got != 2 {
		t.Fatalf("len(Members) = %d, want 2", got)
	}
}

func TestLoadRejectsInvalidPayoutMode(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    payout_mode: wire
    backends:
      - id: b1
        transport: http
        url: http://backend
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want payout_mode validation error")
	}
}

func TestLoadRejectsBearerAuthWithoutSecretRef(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    backends:
      - id: b1
        transport: http
        url: http://backend
        auth:
          method: bearer
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want bearer auth validation error")
	}
}

func TestLoadRejectsInvalidAdminAuthRef(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
admin_auth:
  bearer_token_ref: file:///tmp/token
members:
  - eth_address: 0xabc
    backends:
      - id: b1
        transport: http
        url: http://backend
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid admin auth ref error")
	}
}

func TestLoadRejectsMutuallyExclusiveAdminAuthTokenSources(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
admin_auth:
  bearer_token: local-token
  bearer_token_ref: env://POOL_CONTROLLER_ADMIN_TOKEN
members:
  - eth_address: 0xabc
    backends:
      - id: b1
        transport: http
        url: http://backend
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want mutually exclusive admin auth token error")
	}
}

func TestLoadRejectsInvalidReceiptSinkConfig(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
receipt_sink:
  url: ftp://pool-controller:8080
members:
  - eth_address: 0xabc
    backends:
      - id: b1
        transport: http
        url: http://backend
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
            interaction_mode: http-stream@v0
            work_unit:
              name: tokens
              extractor: { type: openai-usage }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want invalid receipt sink url error")
	}
}
