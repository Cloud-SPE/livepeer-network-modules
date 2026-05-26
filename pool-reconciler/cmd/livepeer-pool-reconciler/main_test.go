package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"google.golang.org/grpc"
)

func TestRunSubmitRoundCloseRequiresArgs(t *testing.T) {
	err := run([]string{"submit-round-close"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run() error = nil, want required flag error")
	}
}

func TestValidateRoundCloseRequest(t *testing.T) {
	err := validateRoundCloseRequest(types.RoundCloseRequest{
		ID:                     "close-1",
		RoundID:                "124",
		PoolRevenueWei:         "2000",
		PoolCutWei:             "200",
		IncludedWorkReceiptIDs: []string{"work-1"},
	})
	if err != nil {
		t.Fatalf("validateRoundCloseRequest() error = %v", err)
	}
}

func TestUsageError(t *testing.T) {
	var out strings.Builder
	err := usageError(&out)
	if err == nil {
		t.Fatal("usageError() error = nil, want error")
	}
	if !strings.Contains(out.String(), "livepeer-pool-reconciler") {
		t.Fatalf("usage output = %q", out.String())
	}
	if !strings.Contains(out.String(), "get-round-status") {
		t.Fatalf("usage output = %q; want get-round-status command", out.String())
	}
	if !strings.Contains(out.String(), "prepare-round-close") {
		t.Fatalf("usage output = %q; want prepare-round-close command", out.String())
	}
	if !strings.Contains(out.String(), "close-round") {
		t.Fatalf("usage output = %q; want close-round command", out.String())
	}
	if !strings.Contains(out.String(), "watch-rounds") {
		t.Fatalf("usage output = %q; want watch-rounds command", out.String())
	}
	if !strings.Contains(out.String(), "get-round-revenue") {
		t.Fatalf("usage output = %q; want get-round-revenue command", out.String())
	}
}

func TestRunSubmitRoundCloseReadsFiles(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	requestPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: http://127.0.0.1:1
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.WriteFile(requestPath, []byte(`{"id":"close-1","round_id":"124","pool_revenue_wei":"2000","pool_cut_wei":"200","included_work_receipt_ids":["work-1"]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(request) error = %v", err)
	}
	var out strings.Builder
	err := run([]string{"submit-round-close", "--config", configPath, "--request", requestPath}, &out, io.Discard)
	if err == nil {
		t.Fatal("run() error = nil, want downstream HTTP error")
	}
}

func TestValidateRoundCloseAgainstRoundSourceSkipsWhenUnconfigured(t *testing.T) {
	err := validateRoundCloseAgainstRoundSource(context.Background(), &config.Config{}, types.RoundCloseRequest{
		ID:                     "close-1",
		RoundID:                "124",
		PoolRevenueWei:         "2000",
		PoolCutWei:             "200",
		IncludedWorkReceiptIDs: []string{"work-1"},
	})
	if err != nil {
		t.Fatalf("validateRoundCloseAgainstRoundSource() error = %v", err)
	}
}

func TestLoadConfigForRoundSourceRequiresSocket(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: http://pool-controller:8080
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	_, err := loadConfigForRoundSource([]string{"--config", configPath}, "get-round-status")
	if err == nil {
		t.Fatal("loadConfigForRoundSource() error = nil, want missing socket error")
	}
}

func TestWriteJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round-close.json")
	err := writeJSONFile(path, types.RoundCloseRequest{
		ID:                     "close-1",
		RoundID:                "124",
		PoolRevenueWei:         "0",
		PoolCutWei:             "0",
		IncludedWorkReceiptIDs: []string{},
	})
	if err != nil {
		t.Fatalf("writeJSONFile() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "\"round_id\": \"124\"") {
		t.Fatalf("output = %q", string(raw))
	}
}

type testProtocolDaemon struct {
	protocolv1.UnimplementedProtocolDaemonServer
	lastRound uint64
}

func (s *testProtocolDaemon) GetRoundStatus(context.Context, *protocolv1.Empty) (*protocolv1.RoundStatus, error) {
	return &protocolv1.RoundStatus{LastRound: s.lastRound, CurrentRoundInitialized: true}, nil
}

type testPayeeDaemon struct {
	paymentsv1.UnimplementedPayeeDaemonServer
	revenue []byte
}

func (s *testPayeeDaemon) GetRoundRevenue(context.Context, *paymentsv1.GetRoundRevenueRequest) (*paymentsv1.GetRoundRevenueResponse, error) {
	return &paymentsv1.GetRoundRevenueResponse{
		RoundId:              124,
		ConfirmedRevenueWei:  s.revenue,
		ConfirmedTicketCount: 2,
	}, nil
}

func TestCloseRoundIntegration(t *testing.T) {
	var gotCloseReq types.RoundCloseRequest
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/work-receipts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"receipts":[{"id":"work-1","round_id":"124","request_id":"req-1","capability_id":"openai:chat-completions","offering_id":"default","member_eth_address":"0xabc","backend_id":"b1","actual_units":42,"gateway_revenue_wei":"2000","status":"final"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/round-close":
			if err := json.NewDecoder(r.Body).Decode(&gotCloseReq); err != nil {
				t.Fatalf("decode round-close body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	protocolSocket := startProtocolDaemonServer(t, &testProtocolDaemon{lastRound: 125})
	paymentSocket := startPayeeDaemonServer(t, &testPayeeDaemon{revenue: []byte{0x0b, 0xb8}})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "reconciler.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controller.URL+`
payment_daemon:
  socket: `+paymentSocket+`
pool:
  commission_bps: 1000
reconcile:
  state_path: `+statePath+`
  backfill_limit: 4
round_source:
  protocol_daemon_socket: `+protocolSocket+`
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	req, err := prepareRoundCloseRequest(context.Background(), cfg, 0)
	if err != nil {
		t.Fatalf("prepareRoundCloseRequest() error = %v", err)
	}
	if req.RoundID != "124" || req.PoolRevenueWei != "3000" || req.PoolCutWei != "300" {
		t.Fatalf("prepared request = %+v", req)
	}
	if len(req.IncludedWorkReceiptIDs) != 1 || req.IncludedWorkReceiptIDs[0] != "work-1" {
		t.Fatalf("prepared receipt ids = %#v", req.IncludedWorkReceiptIDs)
	}

	var out strings.Builder
	if err := run([]string{"close-round", "--config", configPath}, &out, io.Discard); err != nil {
		t.Fatalf("run(close-round) error = %v", err)
	}
	if !strings.Contains(out.String(), "closed round 124") {
		t.Fatalf("close-round output = %q", out.String())
	}
	if gotCloseReq.RoundID != "124" || gotCloseReq.PoolRevenueWei != "3000" || gotCloseReq.PoolCutWei != "300" {
		t.Fatalf("submitted round-close request = %+v", gotCloseReq)
	}
	if len(gotCloseReq.IncludedWorkReceiptIDs) != 1 || gotCloseReq.IncludedWorkReceiptIDs[0] != "work-1" {
		t.Fatalf("submitted included_work_receipt_ids = %#v", gotCloseReq.IncludedWorkReceiptIDs)
	}
}

type testRoundRevenueDaemon struct {
	paymentsv1.UnimplementedPayeeDaemonServer
	revenueByRound map[uint64][]byte
}

func (s *testRoundRevenueDaemon) GetRoundRevenue(_ context.Context, req *paymentsv1.GetRoundRevenueRequest) (*paymentsv1.GetRoundRevenueResponse, error) {
	revenue := s.revenueByRound[uint64(req.RoundId)]
	return &paymentsv1.GetRoundRevenueResponse{
		RoundId:              req.RoundId,
		ConfirmedRevenueWei:  revenue,
		ConfirmedTicketCount: 1,
	}, nil
}

func TestCloseRoundMultiRoundSoak(t *testing.T) {
	closeReqs := make([]types.RoundCloseRequest, 0, 3)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/work-receipts":
			roundID := r.URL.Query().Get("round_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"receipts":[{"id":"work-%s","round_id":"%s","request_id":"req-%s","capability_id":"openai:chat-completions","offering_id":"default","member_eth_address":"0xabc","backend_id":"b1","actual_units":42,"gateway_revenue_wei":"1000","status":"final"}]}`, roundID, roundID, roundID))
		case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/round-close":
			var req types.RoundCloseRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode round-close body: %v", err)
			}
			closeReqs = append(closeReqs, req)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	protocolSocket := startProtocolDaemonServer(t, &testProtocolDaemon{lastRound: 130})
	paymentSocket := startPayeeDaemonServer(t, &testRoundRevenueDaemon{
		revenueByRound: map[uint64][]byte{
			124: {0x03, 0xe8},
			125: {0x07, 0xd0},
			126: {0x0b, 0xb8},
		},
	})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "reconciler.db")
	if err := os.WriteFile(configPath, []byte(`
pool_controller:
  url: `+controller.URL+`
payment_daemon:
  socket: `+paymentSocket+`
pool:
  commission_bps: 1000
reconcile:
  state_path: `+statePath+`
  backfill_limit: 8
round_source:
  protocol_daemon_socket: `+protocolSocket+`
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	for _, roundID := range []string{"124", "125", "126"} {
		var out strings.Builder
		if err := run([]string{"close-round", "--config", configPath, "--round-id", roundID}, &out, io.Discard); err != nil {
			t.Fatalf("run(close-round round=%s) error = %v", roundID, err)
		}
		if !strings.Contains(out.String(), "closed round "+roundID) {
			t.Fatalf("close-round output for %s = %q", roundID, out.String())
		}
	}

	if len(closeReqs) != 3 {
		t.Fatalf("close request count = %d, want 3", len(closeReqs))
	}
	wantRevenue := map[string]string{
		"124": "1000",
		"125": "2000",
		"126": "3000",
	}
	wantCut := map[string]string{
		"124": "100",
		"125": "200",
		"126": "300",
	}
	for _, req := range closeReqs {
		if req.PoolRevenueWei != wantRevenue[req.RoundID] || req.PoolCutWei != wantCut[req.RoundID] {
			t.Fatalf("round %s request = %+v", req.RoundID, req)
		}
		if len(req.IncludedWorkReceiptIDs) != 1 || req.IncludedWorkReceiptIDs[0] != "work-"+req.RoundID {
			t.Fatalf("round %s included receipts = %#v", req.RoundID, req.IncludedWorkReceiptIDs)
		}
	}
}

func startProtocolDaemonServer(t *testing.T, srv protocolv1.ProtocolDaemonServer) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "protocol.sock")
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix protocol) error = %v", err)
	}
	gsrv := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(gsrv, srv)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			gsrv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			gsrv.Stop()
		}
		_ = lis.Close()
	})
	return socketPath
}

func startPayeeDaemonServer(t *testing.T, srv paymentsv1.PayeeDaemonServer) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "payment.sock")
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix payment) error = %v", err)
	}
	gsrv := grpc.NewServer()
	paymentsv1.RegisterPayeeDaemonServer(gsrv, srv)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			gsrv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			gsrv.Stop()
		}
		_ = lis.Close()
	})
	return socketPath
}
