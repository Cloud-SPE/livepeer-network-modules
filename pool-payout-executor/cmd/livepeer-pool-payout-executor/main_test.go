package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/poolcontroller"
)

func TestUsageError(t *testing.T) {
	var out strings.Builder
	err := usageError(&out)
	if err == nil {
		t.Fatal("usageError() error = nil, want error")
	}
	if !strings.Contains(out.String(), "prepare-batch") || !strings.Contains(out.String(), "reconcile-loop") {
		t.Fatalf("usage output = %q", out.String())
	}
}

func TestRunMarkFailedRequiresReason(t *testing.T) {
	err := run([]string{"mark-failed", "--config", "x", "--ids", "a"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run() error = nil, want required reason error")
	}
}

func TestParseCSVStrings(t *testing.T) {
	got := parseCSVStrings("a, b ,,c")
	if len(got) != 3 || got[1] != "b" {
		t.Fatalf("parseCSVStrings() = %#v", got)
	}
}

func TestAddDecimalStrings(t *testing.T) {
	if got := addDecimalStrings("1800", "200"); got != "2000" {
		t.Fatalf("addDecimalStrings() = %q", got)
	}
}

func TestValidateNativeIntent(t *testing.T) {
	err := validateNativeIntent(poolcontroller.PayoutIntent{
		ID:                 "payout-1",
		DestinationAddress: "0x1111111111111111111111111111111111111111",
		ChainID:            42161,
		Asset:              "native_eth",
		AmountWei:          "1800",
	}, 42161)
	if err != nil {
		t.Fatalf("validateNativeIntent() error = %v", err)
	}
}

func TestRunExecutorCommandsAgainstPoolController(t *testing.T) {
	var gotStatusReq poolcontroller.UpdatePayoutIntentStatusRequest
	var gotRequeueReq poolcontroller.RequeuePayoutIntentsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-alerts":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("status") == "failed" {
				_, _ = io.WriteString(w, `{"summary":{"alert_count":2,"critical_count":1,"warning_count":1,"submitted_stale_count":0,"failed_stale_count":1,"lease_expiring_soon_count":0,"retry_limit_count":1,"recent_requeue_count":1},"alerts":[{"type":"failed_stale","severity":"warning","message":"payout intent has remained failed for 2h0m0s","age_seconds":7200,"intent":{"id":"payout-failed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"failed","tx_hash":"0xfail999","failed_at":"2026-05-17T08:00:00Z","retry_count":3,"last_requeued_at":"2026-05-17T07:00:00Z","failure_reason":"rpc timeout"}},{"type":"retry_limit_reached","severity":"critical","message":"payout intent has been requeued 3 times","age_seconds":7200,"intent":{"id":"payout-failed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"failed","tx_hash":"0xfail999","failed_at":"2026-05-17T08:00:00Z","retry_count":3,"last_requeued_at":"2026-05-17T07:00:00Z","failure_reason":"rpc timeout"}}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"summary":{"alert_count":1,"critical_count":1,"warning_count":0,"submitted_stale_count":1,"failed_stale_count":0,"lease_expiring_soon_count":0,"retry_limit_count":0,"recent_requeue_count":0},"alerts":[{"type":"submitted_stale","severity":"critical","message":"payout intent has been submitted for 2h0m0s without confirmation","age_seconds":7200,"intent":{"id":"payout-submitted-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","tx_hash":"0xabc123"}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents":
			if r.URL.Query().Get("status") == "failed" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"intents":[{"id":"payout-failed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"failed","external_ref":"batch-18","tx_hash":"0xfail999","failed_at":"2026-05-17T08:00:00Z","retry_count":1,"last_requeued_at":"2026-05-17T07:00:00Z","failure_reason":"rpc timeout"}]}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-124-0xabc","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"exported"},{"id":"payout-124-0xdef","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"exported","external_ref":"batch-old"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/requeue":
			if err := json.NewDecoder(r.Body).Decode(&gotRequeueReq); err != nil {
				t.Fatalf("decode requeue body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-failed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"exported","retry_count":2,"last_requeued_at":"2026-05-17T10:00:00Z"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			if err := json.NewDecoder(r.Body).Decode(&gotStatusReq); err != nil {
				t.Fatalf("decode status body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-124-0xabc","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","external_ref":"batch-17","tx_hash":"0xabc123"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "executor.yaml")
	if err := os.WriteFile(configPath, []byte("pool_controller:\n  url: "+server.URL+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out strings.Builder
	if err := run([]string{"list-intents", "--config", configPath, "--status", "exported", "--limit", "2"}, &out, io.Discard); err != nil {
		t.Fatalf("run(list-intents) error = %v", err)
	}
	if !strings.Contains(out.String(), `"id": "payout-124-0xabc"`) || !strings.Contains(out.String(), `"asset": "native_eth"`) {
		t.Fatalf("list-intents output unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"prepare-batch", "--config", configPath, "--status", "exported", "--limit", "2"}, &out, io.Discard); err != nil {
		t.Fatalf("run(prepare-batch) error = %v", err)
	}
	if !strings.Contains(out.String(), `"total_wei": "2000"`) || !strings.Contains(out.String(), `"count": 2`) {
		t.Fatalf("prepare-batch output unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"send-native-batch", "--config", configPath, "--status", "exported", "--limit", "2", "--dry-run"}, &out, io.Discard); err != nil {
		t.Fatalf("run(send-native-batch dry-run) error = %v", err)
	}
	if !strings.Contains(out.String(), `"dry_run": true`) || !strings.Contains(out.String(), `"intent already has settlement metadata"`) {
		t.Fatalf("send-native-batch dry-run output unexpected:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"amount_wei": "1800"`) || !strings.Contains(out.String(), `"total_wei": "1800"`) {
		t.Fatalf("send-native-batch dry-run totals unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"list-alerts", "--config", configPath, "--submitted-older-than-seconds", "60"}, &out, io.Discard); err != nil {
		t.Fatalf("run(list-alerts) error = %v", err)
	}
	if !strings.Contains(out.String(), `"alert_count": 1`) || !strings.Contains(out.String(), `"retry_limit_count": 0`) || !strings.Contains(out.String(), `"type": "submitted_stale"`) || !strings.Contains(out.String(), `"severity": "critical"`) {
		t.Fatalf("list-alerts output unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"requeue-failed", "--config", configPath, "--status", "failed", "--limit", "1"}, &out, io.Discard); err != nil {
		t.Fatalf("run(requeue-failed) error = %v", err)
	}
	if len(gotRequeueReq.IDs) != 1 || gotRequeueReq.IDs[0] != "payout-failed-1" {
		t.Fatalf("requeue request = %+v", gotRequeueReq)
	}
	if !strings.Contains(out.String(), `"status": "requeued"`) || !strings.Contains(out.String(), `"count": 1`) || !strings.Contains(out.String(), `"id": "payout-failed-1"`) {
		t.Fatalf("requeue-failed output unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"requeue-alerted-failed", "--config", configPath, "--failed-older-than-seconds", "60"}, &out, io.Discard); err != nil {
		t.Fatalf("run(requeue-alerted-failed) error = %v", err)
	}
	if len(gotRequeueReq.IDs) != 1 || gotRequeueReq.IDs[0] != "payout-failed-1" {
		t.Fatalf("alerted requeue request = %+v", gotRequeueReq)
	}
	if !strings.Contains(out.String(), `"status": "requeued"`) || !strings.Contains(out.String(), `"count": 1`) {
		t.Fatalf("requeue-alerted-failed output unexpected:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"mark-submitted", "--config", configPath, "--ids", "payout-124-0xabc", "--external-ref", "batch-17", "--tx-hash", "0xabc123"}, &out, io.Discard); err != nil {
		t.Fatalf("run(mark-submitted) error = %v", err)
	}
	if gotStatusReq.Status != "submitted" || gotStatusReq.ExternalRef != "batch-17" || gotStatusReq.TxHash != "0xabc123" {
		t.Fatalf("status request = %+v", gotStatusReq)
	}
	if !strings.Contains(out.String(), `"status": "submitted"`) || !strings.Contains(out.String(), `"external_ref": "batch-17"`) {
		t.Fatalf("mark-submitted output unexpected:\n%s", out.String())
	}
}

func TestRunReconcileOnceDryRun(t *testing.T) {
	t.Setenv("POOL_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")

	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "submitted":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-submitted-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","tx_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "exported":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-exported-1","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"exported"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			t.Fatalf("unexpected status update during dry-run")
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any      `json:"id"`
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  "0xa4b1",
			})
		case "eth_getTransactionReceipt":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"status":            "0x1",
					"blockNumber":       "0x10",
					"transactionHash":   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"transactionIndex":  "0x0",
					"blockHash":         "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					"gasUsed":           "0x5208",
					"cumulativeGasUsed": "0x5208",
					"logs":              []any{},
					"logsBloom":         "0x" + strings.Repeat("0", 512),
				},
			})
		default:
			t.Fatalf("unexpected rpc method %q", req.Method)
		}
	}))
	defer rpcServer.Close()

	configPath := filepath.Join(t.TempDir(), "executor.yaml")
	statePath := filepath.Join(t.TempDir(), "executor-state.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controllerServer.URL+`
executor:
  rpc_urls: [`+rpcServer.URL+`]
  private_key_ref: env://POOL_EXECUTOR_TEST_KEY
  chain_id: 42161
  state_path: `+statePath+`
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out strings.Builder
	if err := run([]string{"reconcile-once", "--config", configPath, "--dry-run"}, &out, io.Discard); err != nil {
		t.Fatalf("run(reconcile-once dry-run) error = %v", err)
	}
	if !strings.Contains(out.String(), `"dry_run": true`) {
		t.Fatalf("reconcile-once output missing dry_run:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"status": "would_mark_paid"`) {
		t.Fatalf("reconcile-once output missing confirm preview:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"status": "dry_run"`) || !strings.Contains(out.String(), `"total_wei": "200"`) {
		t.Fatalf("reconcile-once output missing send preview:\n%s", out.String())
	}
}

func TestRunReconcileLoopDryRun(t *testing.T) {
	t.Setenv("POOL_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")

	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "submitted":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-submitted-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","tx_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "exported":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-exported-1","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"exported"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			t.Fatalf("unexpected status update during dry-run loop")
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xa4b1"})
		case "eth_getTransactionReceipt":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"status":            "0x1",
					"blockNumber":       "0x10",
					"transactionHash":   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"transactionIndex":  "0x0",
					"blockHash":         "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					"gasUsed":           "0x5208",
					"cumulativeGasUsed": "0x5208",
					"logs":              []any{},
					"logsBloom":         "0x" + strings.Repeat("0", 512),
				},
			})
		default:
			t.Fatalf("unexpected rpc method %q", req.Method)
		}
	}))
	defer rpcServer.Close()

	configPath := filepath.Join(t.TempDir(), "executor.yaml")
	statePath := filepath.Join(t.TempDir(), "executor-state.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controllerServer.URL+`
executor:
  rpc_urls: [`+rpcServer.URL+`]
  private_key_ref: env://POOL_EXECUTOR_TEST_KEY
  chain_id: 42161
  state_path: `+statePath+`
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out strings.Builder
	if err := run([]string{"reconcile-loop", "--config", configPath, "--dry-run", "--iterations", "2", "--interval-ms", "1"}, &out, io.Discard); err != nil {
		t.Fatalf("run(reconcile-loop dry-run) error = %v", err)
	}
	if !strings.Contains(out.String(), `"iterations": 2`) {
		t.Fatalf("reconcile-loop output missing iteration count:\n%s", out.String())
	}
	if strings.Count(out.String(), `"status": "would_mark_paid"`) != 2 {
		t.Fatalf("reconcile-loop output unexpected confirm count:\n%s", out.String())
	}
	if strings.Count(out.String(), `"status": "dry_run"`) != 2 {
		t.Fatalf("reconcile-loop output unexpected send count:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"state-summary", "--config", configPath, "--runs-limit", "5", "--intents-limit", "5"}, &out, io.Discard); err != nil {
		t.Fatalf("run(state-summary) error = %v", err)
	}
	if !strings.Contains(out.String(), `"state_path": "`+statePath+`"`) {
		t.Fatalf("state-summary missing state path:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"dry_run": true`) || !strings.Contains(out.String(), `"dispatch_attempts": 0`) {
		t.Fatalf("state-summary unexpected contents:\n%s", out.String())
	}
}

func TestRunReconcileLoopAppliesConfirmBackoff(t *testing.T) {
	t.Setenv("POOL_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")

	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "submitted":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-submitted-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","tx_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "exported":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-exported-1","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"exported"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			t.Fatalf("unexpected status update during dry-run backoff test")
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xa4b1"})
		case "eth_getTransactionReceipt":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
		default:
			t.Fatalf("unexpected rpc method %q", req.Method)
		}
	}))
	defer rpcServer.Close()

	configPath := filepath.Join(t.TempDir(), "executor.yaml")
	statePath := filepath.Join(t.TempDir(), "executor-state.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controllerServer.URL+`
executor:
  rpc_urls: [`+rpcServer.URL+`]
  private_key_ref: env://POOL_EXECUTOR_TEST_KEY
  chain_id: 42161
  state_path: `+statePath+`
  backoff_base_ms: 60000
  backoff_max_ms: 60000
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out strings.Builder
	if err := run([]string{"reconcile-loop", "--config", configPath, "--dry-run", "--iterations", "2", "--interval-ms", "1"}, &out, io.Discard); err != nil {
		t.Fatalf("run(reconcile-loop dry-run backoff) error = %v", err)
	}
	if strings.Count(out.String(), `"status": "would_mark_failed"`) != 1 {
		t.Fatalf("expected one initial failure preview:\n%s", out.String())
	}
	if strings.Count(out.String(), `"status": "backoff_skipped"`) != 1 {
		t.Fatalf("expected one backoff skip:\n%s", out.String())
	}
}

func TestLoadDispatchIntentsPrefersOwnedLease(t *testing.T) {
	var claimCalls int
	var renewCalls int
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "leased":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[
				{"id":"payout-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"leased","lease_id":"lease-1","lease_owner":"executor-a"},
				{"id":"payout-2","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"leased","lease_id":"lease-1","lease_owner":"executor-a"},
				{"id":"payout-3","round_id":"124","member_eth_address":"0xghi","destination_address":"0x3333333333333333333333333333333333333333","chain_id":42161,"asset":"native_eth","amount_wei":"50","status":"leased","lease_id":"lease-other","lease_owner":"executor-b"}
			]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/claim":
			claimCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"new-lease","intents":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/renew":
			renewCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"lease-1","intents":[
				{"id":"payout-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"leased","lease_id":"lease-1","lease_owner":"executor-a"},
				{"id":"payout-2","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"leased","lease_id":"lease-1","lease_owner":"executor-a"}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	client, err := poolcontroller.NewClient(config.PoolController{URL: controllerServer.URL})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	leaseID, intents, err := loadDispatchIntents(t.Context(), client, config.Executor{
		ExecutorID:      "executor-a",
		LeaseTTLSeconds: 300,
	}, poolcontroller.ListPayoutIntentsOptions{
		RoundID: "124",
		Status:  "exported",
		Limit:   10,
	}, false)
	if err != nil {
		t.Fatalf("loadDispatchIntents() error = %v", err)
	}
	if claimCalls != 0 {
		t.Fatalf("claimCalls = %d, want 0", claimCalls)
	}
	if renewCalls != 1 {
		t.Fatalf("renewCalls = %d, want 1", renewCalls)
	}
	if leaseID != "lease-1" || len(intents) != 2 {
		t.Fatalf("loadDispatchIntents() = %q %#v", leaseID, intents)
	}
}

func TestSendNativeBatchReleasesLeaseWhenNothingEligible(t *testing.T) {
	var claimCalls int
	var releaseCalls int
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "leased":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/claim":
			claimCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"lease-claimed","intents":[{"id":"payout-claimed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"leased","lease_id":"lease-claimed","lease_owner":"executor-a","external_ref":"already-sent"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/release":
			releaseCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"lease-claimed","intents":[{"id":"payout-claimed-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"exported"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	cfg := &config.Config{
		PoolController: config.PoolController{URL: controllerServer.URL},
		Executor: config.Executor{
			ExecutorID:      "executor-a",
			LeaseTTLSeconds: 300,
			ChainID:         42161,
		},
	}
	result, err := sendNativeBatch(t.Context(), cfg, nil, poolcontroller.ListPayoutIntentsOptions{
		RoundID: "124",
		Status:  "exported",
		Limit:   1,
	}, false)
	if err != nil {
		t.Fatalf("sendNativeBatch() error = %v", err)
	}
	if claimCalls != 1 || releaseCalls != 1 {
		t.Fatalf("claim/release calls = %d/%d", claimCalls, releaseCalls)
	}
	if result.LeaseID != "lease-claimed" || len(result.Results) != 1 || !strings.Contains(result.Results[0]["error"].(string), "settlement metadata") {
		t.Fatalf("sendNativeBatch() result = %#v", result)
	}
}

func TestSendNativeBatchReleasesUntouchedLeaseRemainderAfterPartialSubmit(t *testing.T) {
	t.Setenv("POOL_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")

	var releaseBody poolcontroller.ReleasePayoutIntentsRequest
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "leased":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/claim":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"lease-claimed","intents":[
				{"id":"payout-sendable-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"leased","lease_id":"lease-claimed","lease_owner":"executor-a"},
				{"id":"payout-untouched-1","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"leased","lease_id":"lease-claimed","lease_owner":"executor-a","external_ref":"already-sent"}
			]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
			var req poolcontroller.UpdatePayoutIntentStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode status body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"payout-sendable-1","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"submitted","lease_id":"","lease_owner":"","external_ref":"nonce-7","tx_hash":"0xabc123"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/release":
			if err := json.NewDecoder(r.Body).Decode(&releaseBody); err != nil {
				t.Fatalf("decode release body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"lease_id":"lease-claimed","intents":[{"id":"payout-untouched-1","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"exported"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xa4b1"})
		case "eth_getBalance":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xffffffffffff"})
		case "eth_getTransactionCount":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x7"})
		case "eth_maxPriorityFeePerGas":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x1"})
		case "eth_getBlockByNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"parentHash":       "0x1111111111111111111111111111111111111111111111111111111111111111",
				"sha3Uncles":       "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
				"miner":            "0x0000000000000000000000000000000000000000",
				"stateRoot":        "0x2222222222222222222222222222222222222222222222222222222222222222",
				"transactionsRoot": "0x3333333333333333333333333333333333333333333333333333333333333333",
				"receiptsRoot":     "0x4444444444444444444444444444444444444444444444444444444444444444",
				"logsBloom":        "0x" + strings.Repeat("0", 512),
				"difficulty":       "0x0",
				"number":           "0x10",
				"gasLimit":         "0x1c9c380",
				"gasUsed":          "0x0",
				"timestamp":        "0x1",
				"extraData":        "0x",
				"mixHash":          "0x5555555555555555555555555555555555555555555555555555555555555555",
				"nonce":            "0x0000000000000000",
				"baseFeePerGas":    "0x1",
			}})
		case "eth_estimateGas":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x5208"})
		case "eth_sendRawTransaction":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xabc123"})
		default:
			t.Fatalf("unexpected rpc method %q", req.Method)
		}
	}))
	defer rpcServer.Close()

	cfg := &config.Config{
		PoolController: config.PoolController{URL: controllerServer.URL},
		Executor: config.Executor{
			ExecutorID:      "executor-a",
			LeaseTTLSeconds: 300,
			ChainID:         42161,
			RPCURLs:         []string{rpcServer.URL},
			PrivateKeyRef:   "env://POOL_EXECUTOR_TEST_KEY",
		},
	}
	result, err := sendNativeBatch(t.Context(), cfg, nil, poolcontroller.ListPayoutIntentsOptions{
		RoundID: "124",
		Status:  "exported",
		Limit:   2,
	}, false)
	if err != nil {
		t.Fatalf("sendNativeBatch() error = %v", err)
	}
	if result.LeaseID != "lease-claimed" {
		t.Fatalf("result.LeaseID = %q", result.LeaseID)
	}
	if len(releaseBody.IDs) != 1 || releaseBody.IDs[0] != "payout-untouched-1" {
		t.Fatalf("release body = %+v", releaseBody)
	}
}

func TestAutoRequeueFailedRequeuesOnlyEligibleTransientFailures(t *testing.T) {
	var gotRequeueReq poolcontroller.RequeuePayoutIntentsRequest
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents" && r.URL.Query().Get("status") == "failed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[
				{"id":"retry-eligible","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"failed","failed_at":"2026-05-17T08:00:00Z","retry_count":1,"failure_reason":"rpc timeout"},
				{"id":"retry-permanent","round_id":"124","member_eth_address":"0xdef","destination_address":"0x2222222222222222222222222222222222222222","chain_id":42161,"asset":"native_eth","amount_wei":"200","status":"failed","failed_at":"2026-05-17T08:00:00Z","retry_count":1,"failure_reason":"insufficient funds"},
				{"id":"retry-maxed","round_id":"124","member_eth_address":"0xghi","destination_address":"0x3333333333333333333333333333333333333333","chain_id":42161,"asset":"native_eth","amount_wei":"50","status":"failed","failed_at":"2026-05-17T08:00:00Z","retry_count":3,"failure_reason":"rpc timeout"},
				{"id":"retry-cooling","round_id":"124","member_eth_address":"0xjkl","destination_address":"0x4444444444444444444444444444444444444444","chain_id":42161,"asset":"native_eth","amount_wei":"75","status":"failed","failed_at":"2999-01-01T00:00:00Z","retry_count":0,"failure_reason":"rpc timeout"}
			]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/requeue":
			if err := json.NewDecoder(r.Body).Decode(&gotRequeueReq); err != nil {
				t.Fatalf("decode requeue body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"intents":[{"id":"retry-eligible","round_id":"124","member_eth_address":"0xabc","destination_address":"0x1111111111111111111111111111111111111111","chain_id":42161,"asset":"native_eth","amount_wei":"1800","status":"exported","retry_count":2,"last_requeued_at":"2026-05-17T10:00:00Z"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()

	cfg := &config.Config{
		PoolController: config.PoolController{URL: controllerServer.URL},
		Executor: config.Executor{
			AutoRequeueFailed:      true,
			MaxRetries:             3,
			RequeueCooldownSeconds: 3600,
		},
	}
	result, err := autoRequeueFailed(t.Context(), cfg, poolcontroller.ListPayoutIntentsOptions{
		RoundID: "124",
		Status:  "failed",
		Limit:   10,
	}, false)
	if err != nil {
		t.Fatalf("autoRequeueFailed() error = %v", err)
	}
	if !result.Enabled {
		t.Fatal("autoRequeueFailed() expected enabled result")
	}
	if len(gotRequeueReq.IDs) != 1 || gotRequeueReq.IDs[0] != "retry-eligible" {
		t.Fatalf("requeue request = %+v", gotRequeueReq)
	}
	if len(result.Actions) != 1 || result.Actions[0].IntentID != "retry-eligible" || result.Actions[0].Status != "requeued" {
		t.Fatalf("autoRequeueFailed() actions = %#v", result.Actions)
	}
	if len(result.Results) != 4 {
		t.Fatalf("autoRequeueFailed() results len = %d", len(result.Results))
	}
}

func TestRunReconcileLoopMultiRoundSoak(t *testing.T) {
	t.Setenv("POOL_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")

	store := newTestIntentStore([]poolcontroller.PayoutIntent{
		{ID: "payout-201-0xaaa", RoundID: "201", MemberEthAddress: "0xaaa", DestinationAddress: "0x1111111111111111111111111111111111111111", ChainID: 42161, Asset: "native_eth", AmountWei: "100", Status: "exported"},
		{ID: "payout-202-0xbbb", RoundID: "202", MemberEthAddress: "0xbbb", DestinationAddress: "0x2222222222222222222222222222222222222222", ChainID: 42161, Asset: "native_eth", AmountWei: "200", Status: "exported"},
		{ID: "payout-203-0xccc", RoundID: "203", MemberEthAddress: "0xccc", DestinationAddress: "0x3333333333333333333333333333333333333333", ChainID: 42161, Asset: "native_eth", AmountWei: "300", Status: "exported"},
	})
	controllerServer := httptest.NewServer(http.HandlerFunc(store.handle))
	defer controllerServer.Close()

	rpc := newTestRPCServer()
	rpcServer := httptest.NewServer(http.HandlerFunc(rpc.handle))
	defer rpcServer.Close()

	configPath := filepath.Join(t.TempDir(), "executor.yaml")
	statePath := filepath.Join(t.TempDir(), "executor-state.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controllerServer.URL+`
executor:
  rpc_urls: [`+rpcServer.URL+`]
  private_key_ref: env://POOL_EXECUTOR_TEST_KEY
  chain_id: 42161
  batch_size: 1
  executor_id: executor-soak
  lease_ttl_seconds: 300
  state_path: `+statePath+`
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out strings.Builder
	if err := run([]string{"reconcile-loop", "--config", configPath, "--interval-ms", "1", "--iterations", "5"}, &out, io.Discard); err != nil {
		t.Fatalf("run(reconcile-loop) error = %v", err)
	}

	gotStatuses := store.statuses()
	for _, id := range []string{"payout-201-0xaaa", "payout-202-0xbbb", "payout-203-0xccc"} {
		if gotStatuses[id] != "paid" {
			t.Fatalf("intent %s status = %q, want paid; all statuses = %#v", id, gotStatuses[id], gotStatuses)
		}
	}
	if len(rpc.sentTxHashes()) != 3 {
		t.Fatalf("sent tx count = %d, want 3", len(rpc.sentTxHashes()))
	}
	if !strings.Contains(out.String(), `"iterations": 5`) {
		t.Fatalf("reconcile-loop output unexpected:\n%s", out.String())
	}
}

type testIntentStore struct {
	mu        sync.Mutex
	intents   map[string]poolcontroller.PayoutIntent
	leaseSeq  int
	submitted int
	paid      int
}

func newTestIntentStore(items []poolcontroller.PayoutIntent) *testIntentStore {
	intents := make(map[string]poolcontroller.PayoutIntent, len(items))
	for _, item := range items {
		intents[item.ID] = item
	}
	return &testIntentStore{intents: intents}
}

func (s *testIntentStore) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/payout-intents":
		s.handleList(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/claim":
		s.handleClaim(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/release":
		s.handleRelease(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/payout-intents/status":
		s.handleStatus(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *testIntentStore) handleList(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]poolcontroller.PayoutIntent, 0)
	for _, item := range s.intents {
		if status != "" && item.Status != status {
			continue
		}
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b poolcontroller.PayoutIntent) int {
		return strings.Compare(a.ID, b.ID)
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"intents": items})
}

func (s *testIntentStore) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req poolcontroller.ClaimPayoutIntentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaseSeq++
	leaseID := fmt.Sprintf("%s-%d", req.ExecutorID, s.leaseSeq)
	claimed := make([]poolcontroller.PayoutIntent, 0, req.Limit)
	for _, id := range s.sortedIDsLocked() {
		if len(claimed) == req.Limit {
			break
		}
		item := s.intents[id]
		if item.Status != "exported" {
			continue
		}
		item.Status = "leased"
		item.LeaseID = leaseID
		item.LeaseOwner = req.ExecutorID
		s.intents[id] = item
		claimed = append(claimed, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"lease_id": leaseID, "intents": claimed})
}

func (s *testIntentStore) handleStatus(w http.ResponseWriter, r *http.Request) {
	var req poolcontroller.UpdatePayoutIntentStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := make([]poolcontroller.PayoutIntent, 0, len(req.IDs))
	for _, id := range req.IDs {
		item := s.intents[id]
		switch req.Status {
		case "submitted":
			item.Status = "submitted"
			item.LeaseID = ""
			item.LeaseOwner = ""
			item.ExternalRef = req.ExternalRef
			item.TxHash = req.TxHash
			s.submitted++
		case "paid":
			item.Status = "paid"
			item.TxHash = req.TxHash
			s.paid++
		case "failed":
			item.Status = "failed"
			item.FailureReason = req.FailureReason
		}
		s.intents[id] = item
		updated = append(updated, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"intents": updated})
}

func (s *testIntentStore) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req poolcontroller.ReleasePayoutIntentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	released := make([]poolcontroller.PayoutIntent, 0)
	for _, id := range req.IDs {
		item, ok := s.intents[id]
		if !ok {
			continue
		}
		item.Status = "exported"
		item.LeaseID = ""
		item.LeaseOwner = ""
		s.intents[id] = item
		released = append(released, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"lease_id": req.LeaseID, "intents": released})
}

func (s *testIntentStore) sortedIDsLocked() []string {
	ids := make([]string, 0, len(s.intents))
	for id := range s.intents {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (s *testIntentStore) statuses() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.intents))
	for id, item := range s.intents {
		out[id] = item.Status
	}
	return out
}

type testRPCServer struct {
	mu        sync.Mutex
	nextNonce uint64
	txCount   int
	txHashes  []string
}

func newTestRPCServer() *testRPCServer {
	return &testRPCServer{nextNonce: 1}
}

func (s *testRPCServer) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "eth_chainId":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xa4b1"})
	case "eth_getBalance":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0xffffffffffff"})
	case "eth_getTransactionCount":
		s.mu.Lock()
		nonce := s.nextNonce
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": fmt.Sprintf("0x%x", nonce)})
	case "eth_maxPriorityFeePerGas":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x1"})
	case "eth_getBlockByNumber":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"parentHash":       "0x1111111111111111111111111111111111111111111111111111111111111111",
			"sha3Uncles":       "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
			"miner":            "0x0000000000000000000000000000000000000000",
			"stateRoot":        "0x2222222222222222222222222222222222222222222222222222222222222222",
			"transactionsRoot": "0x3333333333333333333333333333333333333333333333333333333333333333",
			"receiptsRoot":     "0x4444444444444444444444444444444444444444444444444444444444444444",
			"logsBloom":        "0x" + strings.Repeat("0", 512),
			"difficulty":       "0x0",
			"number":           "0x10",
			"gasLimit":         "0x1c9c380",
			"gasUsed":          "0x0",
			"timestamp":        "0x1",
			"extraData":        "0x",
			"mixHash":          "0x5555555555555555555555555555555555555555555555555555555555555555",
			"nonce":            "0x0000000000000000",
			"baseFeePerGas":    "0x1",
		}})
	case "eth_estimateGas":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": "0x673c"})
	case "eth_sendRawTransaction":
		s.mu.Lock()
		s.txCount++
		hash := fmt.Sprintf("0x%064x", s.txCount)
		s.txHashes = append(s.txHashes, hash)
		s.nextNonce++
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": hash})
	case "eth_getTransactionReceipt":
		txHash := ""
		if len(req.Params) > 0 {
			txHash, _ = req.Params[0].(string)
		}
		if strings.TrimSpace(txHash) == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"status":            "0x1",
			"blockNumber":       "0x10",
			"transactionHash":   txHash,
			"transactionIndex":  "0x0",
			"blockHash":         "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"gasUsed":           "0x673c",
			"cumulativeGasUsed": "0x673c",
			"logs":              []any{},
			"logsBloom":         "0x" + strings.Repeat("0", 512),
		}})
	default:
		panic(fmt.Sprintf("unexpected rpc method %q", req.Method))
	}
}

func (s *testRPCServer) sentTxHashes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.txHashes...)
}
