package config

import (
	"strings"
	"testing"
)

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
	if got := cfg.SyntheticProbes.IntervalMS; got != 30000 {
		t.Fatalf("SyntheticProbes.IntervalMS = %d, want 30000", got)
	}
	if got := cfg.SyntheticProbes.TimeoutMS; got != 3000 {
		t.Fatalf("SyntheticProbes.TimeoutMS = %d, want 3000", got)
	}
	if got := cfg.Scoring.CooldownDurationMS; got != 300000 {
		t.Fatalf("Scoring.CooldownDurationMS = %d, want 300000", got)
	}
	if got := cfg.Scoring.CooldownFailureTrigger; got != 5 {
		t.Fatalf("Scoring.CooldownFailureTrigger = %d, want 5", got)
	}
	if got := cfg.Scoring.WarmupModifier; got != 0.25 {
		t.Fatalf("Scoring.WarmupModifier = %v, want 0.25", got)
	}
	if got := cfg.Scoring.RecentWindowStaleAfterMS; got != 300000 {
		t.Fatalf("Scoring.RecentWindowStaleAfterMS = %d, want 300000", got)
	}
	if got := cfg.Scoring.TopDegradedLimit; got != 10 {
		t.Fatalf("Scoring.TopDegradedLimit = %d, want 10", got)
	}
	if got := cfg.Scoring.PublicWorstOfferingsLimit; got != 5 {
		t.Fatalf("Scoring.PublicWorstOfferingsLimit = %d, want 5", got)
	}
	if got := cfg.Bootstrap.BrokerApplyTimeoutMS; got != 30000 {
		t.Fatalf("Bootstrap.BrokerApplyTimeoutMS = %d, want 30000", got)
	}
	if got := cfg.Members[0].PayoutMode; got != "onchain" {
		t.Fatalf("PayoutMode = %q, want onchain", got)
	}
}

func TestLoadBrokerApplyCommandValidation(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  broker_apply_command:
    - /bin/echo
    - apply
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
              extractor:
                type: openai-usage
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := len(cfg.Bootstrap.BrokerApplyCommand); got != 2 {
		t.Fatalf("len(Bootstrap.BrokerApplyCommand) = %d, want 2", got)
	}

	_, err = Load([]byte(`
identity:
  orch_eth_address: 0x123
bootstrap:
  broker_apply_command:
    - /bin/echo
    - "   "
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
              extractor:
                type: openai-usage
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil || !strings.Contains(err.Error(), "bootstrap.broker_apply_command[1]") {
		t.Fatalf("Load() error = %v, want broker_apply_command validation", err)
	}
}

func TestLoadScoringWeightDefaultsFollowPartialOverride(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
scoring:
  window_score_weight: 0.6
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
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Scoring.WindowScoreWeight; got != 0.6 {
		t.Fatalf("Scoring.WindowScoreWeight = %v, want 0.6", got)
	}
	if got := cfg.Scoring.EMAScoreWeight; got != 0.4 {
		t.Fatalf("Scoring.EMAScoreWeight = %v, want 0.4", got)
	}
}

func TestLoadRecentWindowStaleAfterOverride(t *testing.T) {
	cfg, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
scoring:
  recent_window_stale_after_ms: 120000
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
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Scoring.RecentWindowStaleAfterMS; got != 120000 {
		t.Fatalf("Scoring.RecentWindowStaleAfterMS = %d, want 120000", got)
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

func TestLoadRejectsPoolLiveRTMPOffering(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xabc
    backends:
      - id: live-a
        transport: http
        url: http://backend
        offerings:
          - capability_id: video:live.rtmp
            offering_id: default
            interaction_mode: rtmp-ingress-hls-egress@v0
            work_unit:
              name: out_time_seconds
              extractor: { type: ffmpeg-progress, unit: out_time_seconds }
            price:
              amount_wei: "1"
              per_units: 1
`))
	if err == nil {
		t.Fatal("Load() error = nil, want Pool live RTMP validation error")
	}
	if !strings.Contains(err.Error(), "unsupported Pool live RTMP topology") {
		t.Fatalf("Load() error = %v, want Pool live RTMP validation error", err)
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

func TestLoadRejectsNegativeSyntheticProbeConfig(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
synthetic_probes:
  enabled: true
  interval_ms: -1
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
		t.Fatal("Load() error = nil, want invalid synthetic probe interval error")
	}
}

func TestLoadRejectsInvalidScoringWeights(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
scoring:
  window_score_weight: 0.7
  ema_score_weight: 0.4
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
		t.Fatal("Load() error = nil, want invalid scoring weights error")
	}
}

func TestLoadRejectsNegativeRecentWindowStaleAfter(t *testing.T) {
	_, err := Load([]byte(`
identity:
  orch_eth_address: 0x123
scoring:
  recent_window_stale_after_ms: -1
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
		t.Fatal("Load() error = nil, want invalid recent window stale-after error")
	}
}
