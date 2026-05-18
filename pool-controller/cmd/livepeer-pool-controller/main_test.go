package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/configgen"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRunGenerateBrokerConfigWritesToStdout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
identity:
  orch_eth_address: 0x123
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
              extractor: { type: openai-usage, field: total_tokens }
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              openai: { model: llama-3-70b }
              provider: vllm
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout strings.Builder
	if err := run([]string{"generate-broker-config", "--config", path}, &stdout, io.Discard); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "capabilities:") {
		t.Fatalf("stdout missing broker config:\n%s", stdout.String())
	}
}

func TestServeHandlerExposesAdminEndpoints(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "probe-secret")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "pong"}}},
		})
	}))
	defer backend.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte(`
identity:
  orch_eth_address: 0x123
synthetic_probes:
  enabled: true
members:
  - eth_address: 0xabc
    display_name: member-a
    backends:
      - id: b1
        transport: http
        url: `+backend.URL+`
        auth:
          method: bearer
          secret_ref: env://SECRET_TOKEN
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
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
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML() error = %v", err)
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: path, repo: stateRepo}
	if err := state.Replace(cfg, rendered, "startup", nil); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	if err := stateRepo.SaveWorkReceipt(types.WorkReceipt{
		ID:               "work-1",
		CreatedAt:        time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		RequestID:        "req-1",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		MemberEthAddress: "0xabc",
		BackendID:        "b1",
		ActualUnits:      42,
		Status:           "final",
	}); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	if err := stateRepo.SaveRoundReceipt(types.RoundReceipt{
		ID:               "round-1",
		CreatedAt:        time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC),
		RoundID:          "123",
		PoolRevenueWei:   "10000",
		PoolCutWei:       "1000",
		DistributableWei: "9000",
	}); err != nil {
		t.Fatalf("SaveRoundReceipt() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	for _, tc := range []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{path: "/admin", wantStatus: http.StatusOK, wantBody: "Pool Control Plane"},
		{path: "/healthz", wantStatus: http.StatusOK, wantBody: "ok"},
		{path: "/readyz", wantStatus: http.StatusOK, wantBody: "ready"},
		{path: "/public/v1/summary", wantStatus: http.StatusOK, wantBody: `"latest_closed_round":"123"`},
		{path: "/public/v1/rounds", wantStatus: http.StatusOK, wantBody: `"pool_revenue_wei":"10000"`},
		{path: "/public/v1/offerings", wantStatus: http.StatusOK, wantBody: `"backend_count":1`},
		{path: "/public/v1/member-payouts?member_eth_address=0xabc", wantStatus: http.StatusOK, wantBody: `"member_eth_address":"0xabc"`},
		{path: "/admin/v1/broker-config", wantStatus: http.StatusOK, wantBody: "capabilities:"},
		{path: "/admin/v1/offers", wantStatus: http.StatusOK, wantBody: `"status":"active"`},
		{path: "/admin/v1/audit-events", wantStatus: http.StatusOK, wantBody: `"events":[`},
		{path: "/admin/v1/members", wantStatus: http.StatusOK, wantBody: `"secret_ref_set":true`},
		{path: "/admin/v1/members", wantStatus: http.StatusOK, wantBody: `"status":"active"`},
		{path: "/admin/v1/assignments", wantStatus: http.StatusOK, wantBody: `"status":"active"`},
		{path: "/admin/v1/offerings", wantStatus: http.StatusOK, wantBody: `"backend_count":1`},
		{path: "/admin/v1/state", wantStatus: http.StatusOK, wantBody: `"member_count":1`},
		{path: "/admin/v1/state", wantStatus: http.StatusOK, wantBody: `"cooldown_failure_trigger":5`},
		{path: "/admin/v1/scoring-settings", wantStatus: http.StatusOK, wantBody: `"warmup_modifier":0.25`},
		{path: "/admin/v1/backend-selection-snapshot", wantStatus: http.StatusOK, wantBody: `"entries":[`},
		{path: "/admin/v1/backend-selection-snapshot", wantStatus: http.StatusOK, wantBody: `"real_success_score":0.5`},
		{path: "/admin/v1/backend-selection-summary", wantStatus: http.StatusOK, wantBody: `"total"`},
		{path: "/admin/v1/backend-selection-summary", wantStatus: http.StatusOK, wantBody: `"top_degraded"`},
		{path: "/admin/v1/snapshots", wantStatus: http.StatusOK, wantBody: `"source":"startup"`},
		{path: "/admin/v1/work-receipts", wantStatus: http.StatusOK, wantBody: `"request_id":"req-1"`},
		{path: "/admin/v1/round-receipts", wantStatus: http.StatusOK, wantBody: `"round_id":"123"`},
		{path: "/admin/v1/payout-intents", wantStatus: http.StatusOK, wantBody: `"intents":[]`},
		{path: "/admin/v1/member-payouts", wantStatus: http.StatusOK, wantBody: `"members":[]`},
		{path: "/admin/v1/payout-rounds", wantStatus: http.StatusOK, wantBody: `"rounds":[]`},
		{path: "/admin/v1/payout-alerts", wantStatus: http.StatusOK, wantBody: `"alerts":[]`},
	} {
		resp, err := http.Get(server.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s error = %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.wantStatus {
			t.Fatalf("%s status = %d, want %d", tc.path, resp.StatusCode, tc.wantStatus)
		}
		if !strings.Contains(string(body), tc.wantBody) {
			t.Fatalf("%s body missing %q:\n%s", tc.path, tc.wantBody, string(body))
		}
		if (tc.path == "/admin/v1/members" || tc.path == "/admin/v1/state" || tc.path == "/admin/v1/snapshots") && strings.Contains(string(body), "env://SECRET_TOKEN") {
			t.Fatalf("%s leaked secret_ref:\n%s", tc.path, string(body))
		}
	}

	resp, err := http.Get(server.URL + "/admin/v1/backend-selection-snapshot")
	if err != nil {
		t.Fatalf("GET /admin/v1/backend-selection-snapshot error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backend-selection-snapshot status = %d, want 200", resp.StatusCode)
	}
	var snapshot struct {
		Version int `json:"version"`
		Entries []struct {
			MemberEthAddress        string  `json:"member_eth_address"`
			BackendID               string  `json:"backend_id"`
			CapabilityID            string  `json:"capability_id"`
			OfferingID              string  `json:"offering_id"`
			State                   string  `json:"state"`
			SyntheticConfidence     float64 `json:"synthetic_confidence"`
			RealSuccessScore        float64 `json:"real_success_score"`
			RealLatencyScore        float64 `json:"real_latency_score"`
			WarmupModifier          float64 `json:"warmup_modifier"`
			EffectiveSelectionScore float64 `json:"effective_selection_score"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("json.Unmarshal(snapshot) error = %v\nbody=%s", err, string(body))
	}
	if snapshot.Version != 1 {
		t.Fatalf("snapshot.Version = %d, want 1", snapshot.Version)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("len(snapshot.Entries) = %d, want 1", len(snapshot.Entries))
	}
	entry := snapshot.Entries[0]
	if entry.MemberEthAddress != "0xabc" || entry.BackendID != "b1" || entry.CapabilityID != "openai:chat-completions" || entry.OfferingID != "default" {
		t.Fatalf("unexpected snapshot entry identity: %#v", entry)
	}
	if entry.State != "eligible" {
		t.Fatalf("entry.State = %q, want eligible", entry.State)
	}
	if entry.SyntheticConfidence != 0.5 || entry.RealSuccessScore != 0.5 || entry.RealLatencyScore != 0.5 || entry.WarmupModifier != 1.0 || entry.EffectiveSelectionScore != 0.5 {
		t.Fatalf("unexpected default snapshot scores: %#v", entry)
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/quarantine", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","reason":"manual"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/quarantine error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"state":"quarantined"`) {
		t.Fatalf("quarantine status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/clear-quarantine", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/clear-quarantine error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"state":"eligible"`) {
		t.Fatalf("clear-quarantine status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/warmup", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","warmup_modifier":0.25}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/warmup error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"warmup_modifier":0.25`) || !strings.Contains(string(body), `"warmup_source":"manual_override"`) {
		t.Fatalf("warmup status/body = %d %s", resp.StatusCode, string(body))
	}
	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/clear-warmup", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/clear-warmup error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), `"warmup_override"`) {
		t.Fatalf("clear-warmup status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/max-share-cap", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","max_share_cap":0.5}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/max-share-cap error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"max_share_cap":0.5`) {
		t.Fatalf("max-share-cap status/body = %d %s", resp.StatusCode, string(body))
	}
	resp, err = http.Post(server.URL+"/admin/v1/backend-overrides/clear-max-share-cap", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-overrides/clear-max-share-cap error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"max_share_cap":0`) {
		t.Fatalf("clear-max-share-cap status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-outcomes", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","outcome":"success","latency_metric_ms":700,"occurred_at":"2026-05-17T16:00:00Z"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-outcomes error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backend-outcomes status = %d, want 200: %s", resp.StatusCode, string(body))
	}
	var outcomeResp struct {
		Status string `json:"status"`
		Item   struct {
			RealSuccessScore      float64 `json:"real_success_score"`
			RealLatencyScore      float64 `json:"real_latency_score"`
			LastRealOutcomeAt     string  `json:"last_real_outcome_at"`
			RecentOutcomeCount    int     `json:"recent_outcome_count"`
			RecentWindowStartedAt string  `json:"recent_window_started_at"`
			RecentWindowEndedAt   string  `json:"recent_window_ended_at"`
		} `json:"item"`
	}
	if err := json.Unmarshal(body, &outcomeResp); err != nil {
		t.Fatalf("json.Unmarshal(backend-outcomes) error = %v\nbody=%s", err, string(body))
	}
	if outcomeResp.Status != "ingested" {
		t.Fatalf("backend-outcomes status payload = %q, want ingested", outcomeResp.Status)
	}
	if outcomeResp.Item.LastRealOutcomeAt != "2026-05-17T16:00:00Z" {
		t.Fatalf("LastRealOutcomeAt = %q, want 2026-05-17T16:00:00Z", outcomeResp.Item.LastRealOutcomeAt)
	}
	if outcomeResp.Item.RealSuccessScore <= 0 {
		t.Fatalf("RealSuccessScore = %v, want > 0", outcomeResp.Item.RealSuccessScore)
	}
	if outcomeResp.Item.RealLatencyScore <= 0 {
		t.Fatalf("RealLatencyScore = %v, want > 0", outcomeResp.Item.RealLatencyScore)
	}
	if outcomeResp.Item.RecentOutcomeCount != 1 || outcomeResp.Item.RecentWindowStartedAt != "2026-05-17T16:00:00Z" || outcomeResp.Item.RecentWindowEndedAt != "2026-05-17T16:00:00Z" {
		t.Fatalf("backend-outcomes recent window fields = %+v", outcomeResp.Item)
	}

	resp, err = http.Get(server.URL + "/admin/v1/backend-selection-summary")
	if err != nil {
		t.Fatalf("GET /admin/v1/backend-selection-summary error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"average_recent_outcome_count"`) || !strings.Contains(string(body), `"average_recent_window_age_seconds"`) || !strings.Contains(string(body), `"score_distribution"`) || !strings.Contains(string(body), `"traffic_share"`) {
		t.Fatalf("backend-selection-summary initial status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/backend-outcomes", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","outcome":"bad-value"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/backend-outcomes invalid error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "outcome must be one of") {
		t.Fatalf("backend-outcomes invalid status/body = %d %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/synthetic-probes/run", "application/json", bytes.NewBuffer(nil))
	if err != nil {
		t.Fatalf("POST /admin/v1/synthetic-probes/run error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("synthetic-probes/run status = %d, want 200: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"completed"`) || !strings.Contains(string(body), `"succeeded":1`) {
		t.Fatalf("synthetic-probes/run body unexpected: %s", string(body))
	}

	for i := 0; i < 5; i++ {
		at := fmt.Sprintf("2026-05-17T16:%02d:00Z", 10+i)
		resp, err = http.Post(server.URL+"/admin/v1/backend-outcomes", "application/json", bytes.NewBufferString(`{"member_eth_address":"0xabc","backend_id":"b1","capability_id":"openai:chat-completions","offering_id":"default","outcome":"backend_failure","occurred_at":"`+at+`"}`))
		if err != nil {
			t.Fatalf("POST /admin/v1/backend-outcomes backend_failure %d error = %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("backend-outcomes backend_failure %d status = %d", i, resp.StatusCode)
		}
	}
	resp, err = http.Get(server.URL + "/admin/v1/backend-selection-summary")
	if err != nil {
		t.Fatalf("GET /admin/v1/backend-selection-summary after failures error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"top_excluded"`) || !strings.Contains(string(body), `"worst_offerings":[{"key":"openai:chat-completions/default"`) || !strings.Contains(string(body), `"recent_backend_failure_count":5`) || !strings.Contains(string(body), `"recent_window_started_at":"2026-05-17T16:10:00Z"`) || !strings.Contains(string(body), `"top_routing_reasons":{"manual":1}`) || !strings.Contains(string(body), `"top_exclusion_reasons":{"manual":1}`) || !strings.Contains(string(body), `"score_distribution":{"0_10_to_0_29":1}`) || !strings.Contains(string(body), `"recent_routable_traffic_share":1`) {
		t.Fatalf("backend-selection-summary after failures status/body = %d %s", resp.StatusCode, string(body))
	}
	resp, err = http.Get(server.URL + "/public/v1/summary")
	if err != nil {
		t.Fatalf("GET public summary after failures error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public summary after failures status = %d, want 200", resp.StatusCode)
	}
	var publicSummary struct {
		WorstOfferings []struct {
			Key                 string         `json:"key"`
			TopRoutingReasons   map[string]int `json:"top_routing_reasons"`
			TopExclusionReasons map[string]int `json:"top_exclusion_reasons"`
		} `json:"worst_offerings"`
	}
	if err := json.Unmarshal(body, &publicSummary); err != nil {
		t.Fatalf("json.Unmarshal(public summary after failures) error = %v\nbody=%s", err, string(body))
	}
	if len(publicSummary.WorstOfferings) == 0 {
		t.Fatalf("public summary worst_offerings empty after failures: %s", string(body))
	}
	if publicSummary.WorstOfferings[0].Key != "openai:chat-completions/default" {
		t.Fatalf("public summary worst_offerings[0].key = %q; want openai:chat-completions/default", publicSummary.WorstOfferings[0].Key)
	}
	if publicSummary.WorstOfferings[0].TopRoutingReasons["manual"] != 1 {
		t.Fatalf("public summary top_routing_reasons = %+v; want manual=1", publicSummary.WorstOfferings[0].TopRoutingReasons)
	}
	if publicSummary.WorstOfferings[0].TopExclusionReasons["manual"] != 1 {
		t.Fatalf("public summary top_exclusion_reasons = %+v; want manual=1", publicSummary.WorstOfferings[0].TopExclusionReasons)
	}

	if err := os.WriteFile(path, []byte(`
identity:
  orch_eth_address: 0x123
members:
  - eth_address: 0xdef
    display_name: member-b
    backends:
      - id: b2
        transport: http
        url: http://backend-b
        offerings:
          - capability_id: openai:chat-completions
            offering_id: default
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
`), 0o644); err != nil {
		t.Fatalf("WriteFile(reload) error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/admin/v1/reload", nil)
	if err != nil {
		t.Fatalf("NewRequest(reload) error = %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/v1/reload error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload status = %d, want 200: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"reloaded"`) {
		t.Fatalf("reload body missing reloaded status: %s", string(body))
	}
	if !strings.Contains(string(body), `"snapshot_id":"`) {
		t.Fatalf("reload body missing snapshot id: %s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/members")
	if err != nil {
		t.Fatalf("GET /admin/v1/members after reload error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "member-b") {
		t.Fatalf("reloaded members body missing member-b:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/snapshots")
	if err != nil {
		t.Fatalf("GET /admin/v1/snapshots after reload error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"source":"reload"`) {
		t.Fatalf("snapshot list missing reload entry:\n%s", string(body))
	}
	if strings.Contains(string(body), "env://SECRET_TOKEN") {
		t.Fatalf("snapshot list leaked secret_ref:\n%s", string(body))
	}

	workUpsert := `{
	  "id":"work-1",
	  "round_id":"124",
	  "request_id":"req-1",
	  "capability_id":"openai:chat-completions",
	  "offering_id":"default",
	  "member_eth_address":"0xabc",
	  "backend_id":"b1",
	  "actual_units":84,
	  "gateway_revenue_wei":"2000",
	  "status":"final"
	}`
	resp, err = http.Post(server.URL+"/admin/v1/work-receipts", "application/json", bytes.NewBufferString(workUpsert))
	if err != nil {
		t.Fatalf("POST /admin/v1/work-receipts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("work receipt upsert status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"upserted"`) || !strings.Contains(string(body), `"actual_units":84`) {
		t.Fatalf("work receipt upsert body unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/work-receipts")
	if err != nil {
		t.Fatalf("GET /admin/v1/work-receipts after upsert error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"actual_units":84`) {
		t.Fatalf("work receipt list missing updated units:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/work-receipts?round_id=124&status=final")
	if err != nil {
		t.Fatalf("GET filtered /admin/v1/work-receipts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"round_id":"124"`) || !strings.Contains(string(body), `"actual_units":84`) {
		t.Fatalf("filtered work receipt list unexpected:\n%s", string(body))
	}

	roundUpsert := `{
	  "id":"round-1",
	  "round_id":"123",
	  "pool_revenue_wei":"11000",
	  "pool_cut_wei":"1000",
	  "distributable_wei":"10000",
	  "member_payouts":[{"member_eth_address":"0xabc","contribution_wei":"9000","share_ppm":900000,"payout_wei":"9000"}]
	}`
	resp, err = http.Post(server.URL+"/admin/v1/round-receipts", "application/json", bytes.NewBufferString(roundUpsert))
	if err != nil {
		t.Fatalf("POST /admin/v1/round-receipts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("round receipt upsert status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"upserted"`) || !strings.Contains(string(body), `"pool_revenue_wei":"11000"`) {
		t.Fatalf("round receipt upsert body unexpected:\n%s", string(body))
	}

	closeRound := `{
	  "id":"round-close-1",
	  "round_id":"124",
	  "pool_revenue_wei":"2000",
	  "pool_cut_wei":"200",
	  "included_work_receipt_ids":["work-1"]
	}`
	resp, err = http.Post(server.URL+"/admin/v1/round-close", "application/json", bytes.NewBufferString(closeRound))
	if err != nil {
		t.Fatalf("POST /admin/v1/round-close error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("round close status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"closed"`) || !strings.Contains(string(body), `"distributable_wei":"1800"`) {
		t.Fatalf("round close body unexpected:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"member_eth_address":"0xabc"`) || !strings.Contains(string(body), `"payout_wei":"1800"`) {
		t.Fatalf("round close payout body unexpected:\n%s", string(body))
	}

	derivePayouts := `{"round_receipt_id":"round-close-1"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/derive", "application/json", bytes.NewBufferString(derivePayouts))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/derive error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout derive status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"derived"`) || !strings.Contains(string(body), `"amount_wei":"1800"`) {
		t.Fatalf("payout derive body unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/payout-intents?round_id=124&status=pending")
	if err != nil {
		t.Fatalf("GET /admin/v1/payout-intents error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"round_receipt_id":"round-close-1"`) || !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("payout intent list unexpected:\n%s", string(body))
	}

	exportPayouts := `{"round_id":"124","status":"pending","format":"csv"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/export", "application/json", bytes.NewBufferString(exportPayouts))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/export error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout export status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "member_eth_address") || !strings.Contains(string(body), "payout-124-0xabc") {
		t.Fatalf("payout export CSV unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/payout-intents?round_id=124&status=exported&format=json")
	if err != nil {
		t.Fatalf("GET exported payout intents error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"status":"exported"`) || !strings.Contains(string(body), `"exported_at":"`) {
		t.Fatalf("exported payout intent list unexpected:\n%s", string(body))
	}

	claimPayouts := `{"executor_id":"executor-a","lease_ttl_seconds":300,"round_id":"124","limit":1}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/claim", "application/json", bytes.NewBufferString(claimPayouts))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/claim error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout claim status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"claimed"`) || !strings.Contains(string(body), `"status":"leased"`) {
		t.Fatalf("payout claim body unexpected:\n%s", string(body))
	}
	var claimResp struct {
		LeaseID string `json:"lease_id"`
	}
	if err := json.Unmarshal(body, &claimResp); err != nil {
		t.Fatalf("unmarshal claim response error = %v", err)
	}
	if claimResp.LeaseID == "" {
		t.Fatalf("claim response missing lease_id: %s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/renew", "application/json", bytes.NewBufferString(`{"executor_id":"executor-a","lease_id":"`+claimResp.LeaseID+`","lease_ttl_seconds":600}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/renew error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout renew status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"renewed"`) || !strings.Contains(string(body), `"lease_id":"`+claimResp.LeaseID+`"`) {
		t.Fatalf("payout renew body unexpected:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/release", "application/json", bytes.NewBufferString(`{"executor_id":"executor-a","lease_id":"`+claimResp.LeaseID+`"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/release error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout release status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"released"`) || !strings.Contains(string(body), `"status":"exported"`) {
		t.Fatalf("payout release body unexpected:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/claim", "application/json", bytes.NewBufferString(claimPayouts))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/claim second error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second payout claim status = %d: %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &claimResp); err != nil {
		t.Fatalf("unmarshal second claim response error = %v", err)
	}
	if claimResp.LeaseID == "" {
		t.Fatalf("second claim response missing lease_id: %s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/member-payouts?round_id=124")
	if err != nil {
		t.Fatalf("GET /admin/v1/member-payouts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"member_eth_address":"0xabc"`) || !strings.Contains(string(body), `"leased_wei":"1800"`) {
		t.Fatalf("member payout summary unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/public/v1/member-payouts?member_eth_address=0xabc&round_id=124")
	if err != nil {
		t.Fatalf("GET /public/v1/member-payouts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"summary":{"member_eth_address":"0xabc"`) || !strings.Contains(string(body), `"leased_wei":"1800"`) {
		t.Fatalf("public member payout view unexpected:\n%s", string(body))
	}

	updateSubmitted := `{"ids":["payout-124-0xabc"],"status":"submitted","lease_id":"` + claimResp.LeaseID + `","external_ref":"batch-17","tx_hash":"0xabc123"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(`{"ids":["payout-124-0xabc"],"status":"submitted","lease_id":"wrong-lease","external_ref":"batch-17","tx_hash":"0xabc123"}`))
	if err != nil {
		t.Fatalf("POST wrong-lease submitted error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-lease submitted status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(updateSubmitted))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/status submitted error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout submitted status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"submitted"`) || !strings.Contains(string(body), `"submitted_at":"`) {
		t.Fatalf("payout submitted body unexpected:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"external_ref":"batch-17"`) || !strings.Contains(string(body), `"tx_hash":"0xabc123"`) {
		t.Fatalf("payout submitted metadata unexpected:\n%s", string(body))
	}

	updatePaid := `{"ids":["payout-124-0xabc"],"status":"paid","tx_hash":"0xdef456"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(updatePaid))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/status paid error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout paid status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"paid"`) || !strings.Contains(string(body), `"paid_at":"`) {
		t.Fatalf("payout paid body unexpected:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"tx_hash":"0xdef456"`) {
		t.Fatalf("payout paid metadata unexpected:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(updateSubmitted))
	if err != nil {
		t.Fatalf("POST invalid submitted transition error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid payout transition status = %d: %s", resp.StatusCode, string(body))
	}

	derivePayouts = `{"round_id":"124"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/derive", "application/json", bytes.NewBufferString(derivePayouts))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/derive second error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second payout derive status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/export", "application/json", bytes.NewBufferString(exportPayouts))
	if err != nil {
		t.Fatalf("POST second /admin/v1/payout-intents/export error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second payout export status = %d: %s", resp.StatusCode, string(body))
	}

	updateFailed := `{"ids":["payout-124-0xabc"],"status":"failed","external_ref":"batch-18","tx_hash":"0xfail999","failure_reason":"rpc timeout"}`
	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(updateFailed))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/status failed error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout failed status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"failed"`) || !strings.Contains(string(body), `"failure_reason":"rpc timeout"`) || !strings.Contains(string(body), `"failed_at":"`) {
		t.Fatalf("payout failed body unexpected:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"external_ref":"batch-18"`) || !strings.Contains(string(body), `"tx_hash":"0xfail999"`) {
		t.Fatalf("payout failed metadata unexpected:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/requeue", "application/json", bytes.NewBufferString(`{"ids":["payout-124-0xabc"]}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/requeue error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout requeue status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"requeued"`) || !strings.Contains(string(body), `"status":"exported"`) {
		t.Fatalf("payout requeue body unexpected:\n%s", string(body))
	}
	if strings.Contains(string(body), `"external_ref":"batch-18"`) || strings.Contains(string(body), `"tx_hash":"0xfail999"`) || strings.Contains(string(body), `"failure_reason":"rpc timeout"`) {
		t.Fatalf("payout requeue should clear retry metadata:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"failed_at":"0001-01-01T00:00:00Z"`) {
		t.Fatalf("payout requeue should clear failed_at:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"retry_count":1`) || !strings.Contains(string(body), `"last_requeued_at":"`) {
		t.Fatalf("payout requeue should record retry history:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(updateSubmitted))
	if err != nil {
		t.Fatalf("POST /admin/v1/payout-intents/status retry-submitted error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout retry submitted status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"submitted"`) {
		t.Fatalf("payout retry submitted body unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/member-payouts?member_eth_address=0xabc")
	if err != nil {
		t.Fatalf("GET filtered /admin/v1/member-payouts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"member_eth_address":"0xabc"`) || !strings.Contains(string(body), `"submitted_wei":"1800"`) || !strings.Contains(string(body), `"retried_count":1`) || !strings.Contains(string(body), `"total_retry_count":1`) || !strings.Contains(string(body), `"last_requeued_at":"`) {
		t.Fatalf("filtered member payout summary unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/public/v1/member-payouts?member_eth_address=0xabc&round_id=124")
	if err != nil {
		t.Fatalf("GET public retried member payout view error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"summary":{"member_eth_address":"0xabc"`) || !strings.Contains(string(body), `"retried_count":1`) || !strings.Contains(string(body), `"total_retry_count":1`) || !strings.Contains(string(body), `"last_requeued_at":"`) {
		t.Fatalf("public retried member payout view unexpected:\n%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/payout-rounds?round_id=124")
	if err != nil {
		t.Fatalf("GET /admin/v1/payout-rounds error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout rounds status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"round_id":"124"`) || !strings.Contains(string(body), `"submitted_count":1`) || !strings.Contains(string(body), `"submitted_wei":"1800"`) || !strings.Contains(string(body), `"retried_count":1`) || !strings.Contains(string(body), `"total_retry_count":1`) || !strings.Contains(string(body), `"last_requeued_at":"`) {
		t.Fatalf("payout rounds summary unexpected:\n%s", string(body))
	}

	staleSubmittedAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := stateRepo.SavePayoutIntent(types.PayoutIntent{
		ID:                 "alert-submitted",
		CreatedAt:          staleSubmittedAt.Add(-15 * time.Minute),
		RoundReceiptID:     "round-close-1",
		RoundID:            "124",
		MemberEthAddress:   "0xalert1",
		DestinationAddress: "0xalert1",
		ChainID:            42161,
		Asset:              "native_eth",
		AmountWei:          "25",
		Status:             "submitted",
		SubmittedAt:        staleSubmittedAt,
		ExternalRef:        "batch-stale",
		TxHash:             "0xstale1",
	}); err != nil {
		t.Fatalf("SavePayoutIntent(submitted alert) error = %v", err)
	}
	staleFailedAt := time.Now().UTC().Add(-3 * time.Hour)
	if err := stateRepo.SavePayoutIntent(types.PayoutIntent{
		ID:                 "alert-failed",
		CreatedAt:          staleFailedAt.Add(-30 * time.Minute),
		RoundReceiptID:     "round-close-1",
		RoundID:            "124",
		MemberEthAddress:   "0xalert2",
		DestinationAddress: "0xalert2",
		ChainID:            42161,
		Asset:              "native_eth",
		AmountWei:          "50",
		Status:             "failed",
		ExportedAt:         staleFailedAt.Add(-15 * time.Minute),
		SubmittedAt:        staleFailedAt,
		FailedAt:           staleFailedAt,
		RetryCount:         3,
		LastRequeuedAt:     staleFailedAt.Add(-30 * time.Minute),
		FailureReason:      "rpc timeout",
	}); err != nil {
		t.Fatalf("SavePayoutIntent(failed alert) error = %v", err)
	}
	recentRequeueNow := time.Now().UTC()
	if err := stateRepo.SavePayoutIntent(types.PayoutIntent{
		ID:                 "alert-recent-requeue",
		CreatedAt:          recentRequeueNow.Add(-2 * time.Hour),
		RoundReceiptID:     "round-close-1",
		RoundID:            "124",
		MemberEthAddress:   "0xalert4",
		DestinationAddress: "0xalert4",
		ChainID:            42161,
		Asset:              "native_eth",
		AmountWei:          "60",
		Status:             "failed",
		ExportedAt:         recentRequeueNow.Add(-90 * time.Minute),
		SubmittedAt:        recentRequeueNow.Add(-45 * time.Minute),
		FailedAt:           recentRequeueNow.Add(-5 * time.Minute),
		RetryCount:         1,
		LastRequeuedAt:     recentRequeueNow.Add(-2 * time.Minute),
		FailureReason:      "still failing",
	}); err != nil {
		t.Fatalf("SavePayoutIntent(recent requeue alert) error = %v", err)
	}
	leaseNow := time.Now().UTC()
	if err := stateRepo.SavePayoutIntent(types.PayoutIntent{
		ID:                 "alert-leased",
		CreatedAt:          leaseNow.Add(-10 * time.Minute),
		RoundReceiptID:     "round-close-1",
		RoundID:            "124",
		MemberEthAddress:   "0xalert3",
		DestinationAddress: "0xalert3",
		ChainID:            42161,
		Asset:              "native_eth",
		AmountWei:          "75",
		Status:             "leased",
		ExportedAt:         leaseNow.Add(-5 * time.Minute),
		LeasedAt:           leaseNow.Add(-2 * time.Minute),
		LeaseID:            "lease-alert",
		LeaseOwner:         "executor-a",
		LeaseExpiresAt:     leaseNow.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("SavePayoutIntent(leased alert) error = %v", err)
	}

	resp, err = http.Get(server.URL + "/admin/v1/payout-alerts?submitted_older_than_seconds=60&failed_older_than_seconds=60&lease_expires_within_seconds=30&retry_count_at_least=3&recent_requeue_within_seconds=300")
	if err != nil {
		t.Fatalf("GET /admin/v1/payout-alerts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payout alerts status = %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"type":"submitted_stale"`) || !strings.Contains(string(body), `"type":"failed_stale"`) || !strings.Contains(string(body), `"type":"lease_expiring_soon"`) || !strings.Contains(string(body), `"type":"retry_limit_reached"`) || !strings.Contains(string(body), `"type":"failed_after_recent_requeue"`) {
		t.Fatalf("payout alerts missing expected types:\n%s", string(body))
	}
	if !strings.Contains(string(body), `"submitted_stale_count":1`) || !strings.Contains(string(body), `"failed_stale_count":2`) || !strings.Contains(string(body), `"lease_expiring_soon_count":1`) || !strings.Contains(string(body), `"retry_limit_count":1`) || !strings.Contains(string(body), `"recent_requeue_count":1`) {
		t.Fatalf("payout alert summary unexpected:\n%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/work-receipts", "application/json", bytes.NewBufferString(`{"id":"bad","status":"stub"}`))
	if err != nil {
		t.Fatalf("POST invalid /admin/v1/work-receipts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid work receipt status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/round-close", "application/json", bytes.NewBufferString(`{"id":"bad-close","round_id":"125","pool_revenue_wei":"100","pool_cut_wei":"10","included_work_receipt_ids":["missing"]}`))
	if err != nil {
		t.Fatalf("POST invalid /admin/v1/round-close error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid round close status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/derive", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST invalid /admin/v1/payout-intents/derive error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid payout derive status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/payout-intents/status", "application/json", bytes.NewBufferString(`{"ids":["payout-124-0xabc"],"status":"failed"}`))
	if err != nil {
		t.Fatalf("POST invalid /admin/v1/payout-intents/status error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid payout failed-without-reason status = %d: %s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/public/v1/member-payouts")
	if err != nil {
		t.Fatalf("GET invalid /public/v1/member-payouts error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid public member payout status = %d: %s", resp.StatusCode, string(body))
	}
}

func TestAdminOfferAndAssignmentMutationEndpoints(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	if err := stateRepo.PutMember(types.MemberRecord{
		ID:         "member-1",
		EthAddress: "0xabc",
		PayoutMode: "onchain",
		Status:     types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutMember() error = %v", err)
	}
	if err := stateRepo.PutMemberBackend(types.MemberBackend{
		ID:        "backend-1",
		MemberID:  "member-1",
		Transport: "http",
		URL:       "http://backend",
		Status:    types.BackendStatusActive,
		ClaimedCapabilities: []types.ClaimedOffer{{
			CapabilityID:    "rerank",
			OfferingID:      "zerank-2-default",
			InteractionMode: "http-reqresp@v0",
		}},
	}); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	offerBody := `{"id":"offer-1","capability_id":"rerank","offering_id":"zerank-2-default","interaction_mode":"http-reqresp@v0","work_unit":{"name":"requests","extractor":{"type":"request-formula","expression":"1"}},"price":{"amount_wei":"1","per_units":1}}`
	resp, err := http.Post(server.URL+"/admin/v1/offers", "application/json", bytes.NewBufferString(offerBody))
	if err != nil {
		t.Fatalf("POST /admin/v1/offers error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"offer-1"`) {
		t.Fatalf("POST /admin/v1/offers status=%d body=%s", resp.StatusCode, string(body))
	}

	assignBody := `{"id":"assignment-1","offer_id":"offer-1","member_backend_id":"backend-1"}`
	resp, err = http.Post(server.URL+"/admin/v1/assignments", "application/json", bytes.NewBufferString(assignBody))
	if err != nil {
		t.Fatalf("POST /admin/v1/assignments error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"id":"assignment-1"`) {
		t.Fatalf("POST /admin/v1/assignments status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/offerings")
	if err != nil {
		t.Fatalf("GET /admin/v1/offerings error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"backend_count":1`) {
		t.Fatalf("GET /admin/v1/offerings body=%s", string(body))
	}
}

func TestAdminAssignmentRejectsIncompatibleBackend(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	if err := stateRepo.PutOffer(types.Offer{
		ID:              "offer-1",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
		Status:          types.OfferStatusActive,
	}); err != nil {
		t.Fatalf("PutOffer() error = %v", err)
	}
	if err := stateRepo.PutMember(types.MemberRecord{
		ID:         "member-1",
		EthAddress: "0xabc",
		PayoutMode: "onchain",
		Status:     types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutMember() error = %v", err)
	}
	if err := stateRepo.PutMemberBackend(types.MemberBackend{
		ID:        "backend-1",
		MemberID:  "member-1",
		Transport: "http",
		URL:       "http://backend",
		Status:    types.BackendStatusActive,
		ClaimedCapabilities: []types.ClaimedOffer{{
			CapabilityID:    "openai:chat-completions",
			InteractionMode: "http-stream@v0",
		}},
	}); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	assignBody := `{"id":"assignment-1","offer_id":"offer-1","member_backend_id":"backend-1"}`
	resp, err := http.Post(server.URL+"/admin/v1/assignments", "application/json", bytes.NewBufferString(assignBody))
	if err != nil {
		t.Fatalf("POST /admin/v1/assignments error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "do not match offer") {
		t.Fatalf("POST /admin/v1/assignments status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestJoinRequestApprovalAndStatusMutations(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	joinBody := `{
	  "id":"join-1",
	  "member_eth_address":"0xmember",
	  "display_name":"member-a",
	  "payout_mode":"onchain",
	  "requested_backends":[
	    {
	      "id":"backend-join-1",
	      "transport":"http",
	      "url":"http://backend",
	      "auth":{"method":"none"},
	      "health_probe":{"type":"http-status","config":{"url":"http://backend/healthz"}},
	      "claimed_capabilities":[
	        {"capability_id":"rerank","offering_id":"zerank-2-default","interaction_mode":"http-reqresp@v0"}
	      ]
	    }
	  ]
	}`
	resp, err := http.Post(server.URL+"/member/v1/join-requests", "application/json", bytes.NewBufferString(joinBody))
	if err != nil {
		t.Fatalf("POST /member/v1/join-requests error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("POST /member/v1/join-requests status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/join-requests")
	if err != nil {
		t.Fatalf("GET /admin/v1/join-requests error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"id":"join-1"`) {
		t.Fatalf("GET /admin/v1/join-requests body=%s", string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/join-requests/join-1/approve", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/join-requests/join-1/approve error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"approved"`) {
		t.Fatalf("approve status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/members")
	if err != nil {
		t.Fatalf("GET /admin/v1/members error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"eth_address":"0xmember"`) {
		t.Fatalf("GET /admin/v1/members body=%s", string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/member-backends")
	if err != nil {
		t.Fatalf("GET /admin/v1/member-backends error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"id":"backend-join-1"`) {
		t.Fatalf("GET /admin/v1/member-backends body=%s", string(body))
	}

	members, err := stateRepo.ListMembers()
	if err != nil || len(members) != 1 {
		t.Fatalf("ListMembers() members=%#v err=%v", members, err)
	}
	memberID := members[0].ID

	req, err := http.NewRequest(http.MethodPatch, server.URL+"/admin/v1/members/"+memberID, bytes.NewBufferString(`{"status":"suspended"}`))
	if err != nil {
		t.Fatalf("NewRequest(member patch) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /admin/v1/members error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"suspended"`) {
		t.Fatalf("PATCH /admin/v1/members status=%d body=%s", resp.StatusCode, string(body))
	}

	req, err = http.NewRequest(http.MethodPatch, server.URL+"/admin/v1/member-backends/backend-join-1", bytes.NewBufferString(`{"status":"disabled"}`))
	if err != nil {
		t.Fatalf("NewRequest(backend patch) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /admin/v1/member-backends error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"disabled"`) {
		t.Fatalf("PATCH /admin/v1/member-backends status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestBrokerRuntimeEndpoints(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/v1/broker-runtime")
	if err != nil {
		t.Fatalf("GET /admin/v1/broker-runtime error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"dirty":true`) {
		t.Fatalf("GET /admin/v1/broker-runtime status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/broker-runtime/diff")
	if err != nil {
		t.Fatalf("GET /admin/v1/broker-runtime/diff error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"dirty":true`) {
		t.Fatalf("GET /admin/v1/broker-runtime/diff status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/broker-runtime/mark-applied", "application/json", bytes.NewBufferString(`{"actor":"tester"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/broker-runtime/mark-applied error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"dirty":false`) || !strings.Contains(string(body), `"last_apply_status":"applied"`) {
		t.Fatalf("POST /admin/v1/broker-runtime/mark-applied status=%d body=%s", resp.StatusCode, string(body))
	}

	applied, err := stateRepo.GetAppliedBrokerRuntime()
	if err != nil {
		t.Fatalf("GetAppliedBrokerRuntime() error = %v", err)
	}
	if applied.AppliedRevision == "" || applied.LastApplyStatus != "applied" {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestJoinRequestVerificationAndBackendVerificationFlow(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer probe.Close()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	joinBody := `{
	  "id":"join-verify",
	  "member_eth_address":"0xmember",
	  "display_name":"member-a",
	  "payout_mode":"onchain",
	  "requested_backends":[
	    {
	      "id":"backend-verify",
	      "transport":"http",
	      "url":"` + probe.URL + `/v1/rerank",
	      "auth":{"method":"none"},
	      "health_probe":{"type":"http-status","config":{"url":"` + probe.URL + `/healthz"}},
	      "claimed_capabilities":[
	        {"capability_id":"rerank","offering_id":"zerank-2-default","interaction_mode":"http-reqresp@v0"}
	      ]
	    }
	  ]
	}`
	resp, err := http.Post(server.URL+"/member/v1/join-requests", "application/json", bytes.NewBufferString(joinBody))
	if err != nil {
		t.Fatalf("POST /member/v1/join-requests error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /member/v1/join-requests status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/member/v1/join-requests/join-verify/refresh", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /member/v1/join-requests/join-verify/refresh error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"verification_status":"passing"`) {
		t.Fatalf("refresh join-request status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/join-requests/join-verify/approve", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/join-requests/join-verify/approve error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve join-request status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/member-backends/backend-verify/verify", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/member-backends/backend-verify/verify error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"verification_status":"passing"`) {
		t.Fatalf("verify backend status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestOperatorFlowEndToEnd(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer probe.Close()

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x123"},
		Listen:   config.Listen{Paid: ":8080", Metrics: ":9090"},
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: configPath, repo: stateRepo, cfg: cfg}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		t.Fatalf("renderBrokerState() error = %v", err)
	}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	offerBody := `{
	  "id":"rerank-zerank2",
	  "capability_id":"rerank",
	  "offering_id":"zerank-2-default",
	  "interaction_mode":"http-reqresp@v0",
	  "work_unit":{"name":"requests","extractor":{"type":"request-formula","expression":"1"}},
	  "price":{"amount_wei":"1","per_units":1}
	}`
	resp, err := http.Post(server.URL+"/admin/v1/offers", "application/json", bytes.NewBufferString(offerBody))
	if err != nil {
		t.Fatalf("POST /admin/v1/offers error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/v1/offers status=%d body=%s", resp.StatusCode, string(body))
	}

	joinBody := `{
	  "id":"join-flow",
	  "member_eth_address":"0xmember",
	  "display_name":"member-a",
	  "payout_mode":"onchain",
	  "requested_backends":[
	    {
	      "id":"backend-flow",
	      "transport":"http",
	      "url":"` + probe.URL + `/v1/rerank",
	      "auth":{"method":"none"},
	      "health_probe":{"type":"http-status","config":{"url":"` + probe.URL + `/healthz"}},
	      "claimed_capabilities":[
	        {"capability_id":"rerank","offering_id":"zerank-2-default","interaction_mode":"http-reqresp@v0"}
	      ]
	    }
	  ]
	}`
	resp, err = http.Post(server.URL+"/member/v1/join-requests", "application/json", bytes.NewBufferString(joinBody))
	if err != nil {
		t.Fatalf("POST /member/v1/join-requests error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /member/v1/join-requests status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/join-requests/join-flow/refresh", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/join-requests/join-flow/refresh error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"verification_status":"passing"`) {
		t.Fatalf("refresh join-flow status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/join-requests/join-flow/approve", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/join-requests/join-flow/approve error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve join-flow status=%d body=%s", resp.StatusCode, string(body))
	}

	assignmentBody := `{"id":"assign-flow","offer_id":"rerank-zerank2","member_backend_id":"backend-flow"}`
	resp, err = http.Post(server.URL+"/admin/v1/assignments", "application/json", bytes.NewBufferString(assignmentBody))
	if err != nil {
		t.Fatalf("POST /admin/v1/assignments error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/v1/assignments status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/assignment-preview", "application/json", bytes.NewBufferString(`{"offer_id":"rerank-zerank2","member_backend_id":"backend-flow"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/assignment-preview error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"compatible":true`) || !strings.Contains(string(body), `"checks":[`) || !strings.Contains(string(body), `"matched_claim"`) {
		t.Fatalf("assignment-preview status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(server.URL + "/admin/v1/broker-runtime")
	if err != nil {
		t.Fatalf("GET /admin/v1/broker-runtime error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"dirty":true`) {
		t.Fatalf("GET /admin/v1/broker-runtime dirty status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Post(server.URL+"/admin/v1/broker-runtime/mark-applied", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/broker-runtime/mark-applied error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"dirty":false`) || !strings.Contains(string(body), `"last_apply_status":"applied"`) {
		t.Fatalf("mark-applied status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestRuntimeStateSyncAccountingMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte(`
identity:
  orch_eth_address: 0x123
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
              extractor: { type: openai-usage, field: total_tokens }
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              openai: { model: llama-3-70b }
              provider: vllm
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML() error = %v", err)
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: path, repo: stateRepo}
	if err := state.Replace(cfg, rendered, "startup", nil); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	if err := stateRepo.SaveWorkReceipt(types.WorkReceipt{
		ID:               "work-1",
		RequestID:        "req-1",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		MemberEthAddress: "0xabc",
		BackendID:        "b1",
		Status:           "final",
	}); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	if err := stateRepo.SaveRoundReceipt(types.RoundReceipt{
		ID:               "round-1",
		RoundID:          "123",
		PoolRevenueWei:   "100",
		PoolCutWei:       "10",
		DistributableWei: "90",
	}); err != nil {
		t.Fatalf("SaveRoundReceipt() error = %v", err)
	}
	if err := stateRepo.SavePayoutIntent(types.PayoutIntent{
		ID:                 "payout-1",
		RoundReceiptID:     "round-1",
		RoundID:            "123",
		MemberEthAddress:   "0xabc",
		DestinationAddress: "0xabc",
		ChainID:            1,
		Asset:              "ETH",
		AmountWei:          "90",
		Status:             "leased",
	}); err != nil {
		t.Fatalf("SavePayoutIntent() error = %v", err)
	}

	if err := state.syncAccountingMetrics(); err != nil {
		t.Fatalf("syncAccountingMetrics() error = %v", err)
	}

	if got := testutil.ToFloat64(observability.TestWorkReceiptStatusGauge("final")); got < 1 {
		t.Fatalf("final work receipt gauge = %v; want >= 1", got)
	}
	if got := testutil.ToFloat64(observability.TestRoundReceiptGauge()); got < 1 {
		t.Fatalf("round receipt gauge = %v; want >= 1", got)
	}
	if got := testutil.ToFloat64(observability.TestPayoutIntentStatusGauge("leased")); got < 1 {
		t.Fatalf("leased payout intent gauge = %v; want >= 1", got)
	}
}

func TestServeHandlerAdminAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	t.Setenv("POOL_CONTROLLER_ADMIN_TOKEN", "super-secret")
	if err := os.WriteFile(path, []byte(`
identity:
  orch_eth_address: 0x123
admin_auth:
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
              extractor: { type: openai-usage, field: total_tokens }
            price:
              amount_wei: "1"
              per_units: 1
            extra:
              openai: { model: llama-3-70b }
              provider: vllm
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	token, err := resolveAdminToken(cfg)
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}
	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML() error = %v", err)
	}
	stateRepo, err := repo.Open(dataDir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: path, repo: stateRepo, adminToken: token}
	if err := state.Replace(cfg, rendered, "startup", nil); err != nil {
		t.Fatalf("state.Replace() error = %v", err)
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/v1/state")
	if err != nil {
		t.Fatalf("GET unauthorized admin endpoint error = %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(server.URL + "/public/v1/summary")
	if err != nil {
		t.Fatalf("GET public summary error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public summary status = %d, want 200", resp.StatusCode)
	}
	var publicSummary struct {
		WorstOfferings []struct {
			Key               string         `json:"key"`
			TopRoutingReasons map[string]int `json:"top_routing_reasons"`
		} `json:"worst_offerings"`
	}
	if err := json.Unmarshal(body, &publicSummary); err != nil {
		t.Fatalf("json.Unmarshal(public summary) error = %v\nbody=%s", err, string(body))
	}
	if len(publicSummary.WorstOfferings) != 0 {
		t.Fatalf("public summary worst_offerings = %+v; want empty before unhealthy state", publicSummary.WorstOfferings)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/state", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer super-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized GET error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
