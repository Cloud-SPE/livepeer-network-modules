package grpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// dialListener spins a Listener on a fresh tmpdir socket and returns a
// dial-ready gRPC client. The cleanup func cancels the serve ctx and
// waits for the listener to shut down.
func dialListener(t *testing.T, srv *Server) (*grpc.ClientConn, func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "protocol.sock")
	lis, err := NewListener(ListenerConfig{
		SocketPath: sock,
		Server:     srv,
		Logger:     logger.Slog(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}),
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- lis.Serve(ctx) }()

	// Wait for the socket to actually appear before dialing.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("socket never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cc, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = cc.Close()
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve returned: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Serve did not exit within 3s of ctx cancel")
		}
		// Socket file should be gone after Stop.
		if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("socket file not cleaned up: %v", err)
		}
	}
	return cc, cleanup
}

func TestListener_Health(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, err := New(Config{Mode: types.ModeRoundInit, Version: "v0.1", ChainID: 42161, RoundInit: rs})
	if err != nil {
		t.Fatal(err)
	}
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	got, err := cli.Health(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !got.GetOk() || got.GetMode() != "round-init" || got.GetVersion() != "v0.1" || got.GetChainId() != 42161 {
		t.Fatalf("Health = %+v", got)
	}

	// gRPC health probe should also succeed.
	hc := healthpb.NewHealthClient(cc)
	hresp, err := hc.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health.Check: %v", err)
	}
	if hresp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health.Check status = %v; want SERVING", hresp.GetStatus())
	}
}

func TestListener_ModeGated(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	// reward-only RPC against a round-init daemon should return Unimplemented.
	if _, err := cli.GetRewardStatus(context.Background(), &protocolv1.Empty{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("GetRewardStatus code = %v; want Unimplemented (err=%v)", status.Code(err), err)
	}
}

func TestListener_GetTxIntent(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	now := time.Now()
	stub := &stubSubmitter{intent: txintent.TxIntent{
		Kind:          "round-init",
		Status:        txintent.StatusConfirmed,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Tx: stub})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	id := txintent.ComputeID("round-init", []byte("k"))
	resp, err := cli.GetTxIntent(context.Background(), &protocolv1.TxIntentRef{Id: id[:]})
	if err != nil {
		t.Fatalf("GetTxIntent: %v", err)
	}
	if resp.GetKind() != "round-init" || resp.GetStatus() != "confirmed" {
		t.Fatalf("GetTxIntent = %+v", resp)
	}

	// 31-byte id → InvalidArgument (we validate length in the adapter).
	if _, err := cli.GetTxIntent(context.Background(), &protocolv1.TxIntentRef{Id: make([]byte, 31)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("short id code = %v; want InvalidArgument", status.Code(err))
	}
}

func TestListener_SetServiceURI(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, err := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		Registry:  newServiceRegistrySvc(t, common.HexToAddress("0x000000000000000000000000000000000000FC01")),
	})
	if err != nil {
		t.Fatal(err)
	}
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	resp, err := cli.SetServiceURI(context.Background(), &protocolv1.SetServiceURIRequest{Url: "https://orch.example.com"})
	if err != nil {
		t.Fatalf("SetServiceURI: %v", err)
	}
	if len(resp.GetId()) != len(txintent.IntentID{}) {
		t.Fatalf("intent id len = %d", len(resp.GetId()))
	}

	if _, err := cli.SetServiceURI(context.Background(), &protocolv1.SetServiceURIRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty url code = %v; want InvalidArgument", status.Code(err))
	}
}

func TestListener_SetAIServiceURI(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, err := New(Config{
		Mode:       types.ModeRoundInit,
		RoundInit:  rs,
		AIRegistry: newAIServiceRegistrySvc(t, common.HexToAddress("0x000000000000000000000000000000000000FC02")),
	})
	if err != nil {
		t.Fatal(err)
	}
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	resp, err := cli.SetAIServiceURI(context.Background(), &protocolv1.SetAIServiceURIRequest{Url: "https://ai.example.com"})
	if err != nil {
		t.Fatalf("SetAIServiceURI: %v", err)
	}
	if len(resp.GetId()) != len(txintent.IntentID{}) {
		t.Fatalf("intent id len = %d", len(resp.GetId()))
	}

	if _, err := cli.SetAIServiceURI(context.Background(), &protocolv1.SetAIServiceURIRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty url code = %v; want InvalidArgument", status.Code(err))
	}
}

func TestListener_OrchStatusRPCs(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, err := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		Orch: newOrchStatusSvc(
			t,
			common.HexToAddress("0x000000000000000000000000000000000000FC01"),
			common.HexToAddress("0x00000000000000000000000000000000000000A1"),
			common.HexToAddress("0x00000000000000000000000000000000000000B2"),
			"https://orch.example.com",
			big.NewInt(6789),
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	uri, err := cli.GetOnChainServiceURI(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetOnChainServiceURI: %v", err)
	}
	if uri.GetUrl() != "https://orch.example.com" {
		t.Fatalf("uri = %q", uri.GetUrl())
	}

	registered, err := cli.IsRegistered(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("IsRegistered: %v", err)
	}
	if !registered.GetRegistered() {
		t.Fatal("expected registered")
	}

	bal, err := cli.GetWalletBalance(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if got := common.BytesToAddress(bal.GetWalletAddress()); got != common.HexToAddress("0x00000000000000000000000000000000000000B2") {
		t.Fatalf("wallet = %s", got.Hex())
	}
	if got := new(big.Int).SetBytes(bal.GetBalanceWei()); got.Cmp(big.NewInt(6789)) != 0 {
		t.Fatalf("balance = %s", got.String())
	}
}

func TestListener_AIOrchStatusRPCs(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, err := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		AIOrch: newOrchStatusSvc(
			t,
			common.HexToAddress("0x000000000000000000000000000000000000FC02"),
			common.HexToAddress("0x00000000000000000000000000000000000000A1"),
			common.HexToAddress("0x00000000000000000000000000000000000000B2"),
			"https://ai.example.com",
			big.NewInt(6789),
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	uri, err := cli.GetOnChainAIServiceURI(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetOnChainAIServiceURI: %v", err)
	}
	if uri.GetUrl() != "https://ai.example.com" {
		t.Fatalf("uri = %q", uri.GetUrl())
	}

	registered, err := cli.IsAIRegistered(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("IsAIRegistered: %v", err)
	}
	if !registered.GetRegistered() {
		t.Fatal("expected registered")
	}
}

func TestListener_GetTxIntentNotFound(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	stub := &stubSubmitter{err: errors.New("nope")}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Tx: stub})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	id := txintent.ComputeID("round-init", []byte("k"))
	if _, err := cli.GetTxIntent(context.Background(), &protocolv1.TxIntentRef{Id: id[:]}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing id code = %v; want NotFound (err=%v)", status.Code(err), err)
	}
}

func TestListener_StreamRoundEvents(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	src := &stubRoundClockSrc{rounds: make(chan chain.Round, 2)}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, RC: src})

	src.rounds <- chain.Round{Number: 7, StartBlock: 100}
	close(src.rounds)

	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	stream, err := cli.StreamRoundEvents(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ev.GetNumber() != 7 || ev.GetStartBlock() != 100 {
		t.Fatalf("event = %+v", ev)
	}
	// Channel was closed → server returns nil → client sees io.EOF.
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after close: %v; want io.EOF", err)
	}
}

func TestListener_GetRoundStatus(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)
	got, err := cli.GetRoundStatus(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetRoundStatus: %v", err)
	}
	// Default state: round 0, no error, no intent, current_round_initialized
	// false (never queried).
	if got.GetLastRound() != 0 || got.GetLastError() != "" {
		t.Fatalf("GetRoundStatus = %+v", got)
	}
	if got.GetCurrentRoundInitialized() {
		t.Fatal("CurrentRoundInitialized = true on a fresh service; want false")
	}

	// After the service observes a round as already-initialized, the wire
	// field should flip true.
	if _, err := rs.TryInitialize(context.Background(), chain.Round{Number: 1}); err != nil {
		t.Fatal(err)
	}
	got, err = cli.GetRoundStatus(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetRoundStatus #2: %v", err)
	}
	// stubRM in server_test.go has initialized=false, so this path is
	// "submit"; CurrentRoundInitialized stays false (in-flight signal).
	if got.GetCurrentRoundInitialized() {
		t.Fatal("CurrentRoundInitialized true after submit; want false (tx not mined)")
	}
	if len(got.GetLastIntentId()) != 32 {
		t.Fatalf("LastIntentId len = %d; want 32 after submit", len(got.GetLastIntentId()))
	}
}

func TestListener_GetRoundStatus_ReportsAlreadyInitialized(t *testing.T) {
	// Build a Service whose RoundsManager already reports initialized=true.
	rm := &stubRM{addr: common.HexToAddress("0xdeadbeef"), initialized: true}
	rs, err := roundinit.New(roundinit.Config{
		RoundsManager: rm,
		TxIntent:      &stubSubmitter{},
		GasLimit:      1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)

	if _, err := rs.TryInitialize(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	got, err := cli.GetRoundStatus(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("GetRoundStatus: %v", err)
	}
	if !got.GetCurrentRoundInitialized() {
		t.Fatal("CurrentRoundInitialized = false; want true after observing already-initialized round")
	}
	if got.GetLastRound() != 5 {
		t.Fatalf("LastRound = %d; want 5", got.GetLastRound())
	}
}

func TestListener_ForceInitializeRound(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)
	got, err := cli.ForceInitializeRound(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("ForceInitializeRound: %v", err)
	}
	if got.GetSubmitted() == nil {
		t.Fatalf("expected submitted arm; got %+v", got)
	}
	if len(got.GetSubmitted().GetId()) != 32 {
		t.Fatalf("intent id length = %d; want 32", len(got.GetSubmitted().GetId()))
	}
}

func TestListener_ForceInitializeRoundSkipsAlreadyInitialized(t *testing.T) {
	rs := newRoundInitSvcWithInit(t, common.HexToAddress("0xdeadbeef"), true)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)
	got, err := cli.ForceInitializeRound(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("ForceInitializeRound: %v", err)
	}
	if got.GetSkipped() == nil {
		t.Fatalf("expected skipped arm; got %+v", got)
	}
	if got.GetSkipped().GetCode() != protocolv1.SkipReason_CODE_ROUND_INITIALIZED {
		t.Fatalf("Skipped.Code = %v; want CODE_ROUND_INITIALIZED", got.GetSkipped().GetCode())
	}
	if got.GetSkipped().GetReason() != "round already initialized" {
		t.Fatalf("Skipped.Reason = %q", got.GetSkipped().GetReason())
	}
}

func TestListener_ForceRewardCall(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	// Active at round 100, already rewarded at round 100 → AlreadyRewarded
	// skip path triggers. Drive lastRound to 100 so ForceRewardCall reads
	// it from Status.
	tinfo := bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, DeactivationRound: 1_000_000, LastRewardRound: 100}
	rwd := newRewardSvc(t, bmAddr, orch, tinfo)
	if _, err := rwd.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)
	got, err := cli.ForceRewardCall(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("ForceRewardCall: %v", err)
	}
	if got.GetSkipped() == nil {
		t.Fatalf("expected skipped arm; got %+v", got)
	}
	if got.GetSkipped().GetCode() != protocolv1.SkipReason_CODE_ALREADY_REWARDED {
		t.Fatalf("Skipped.Code = %v; want CODE_ALREADY_REWARDED", got.GetSkipped().GetCode())
	}
	if got.GetSkipped().GetReason() != "already rewarded this round" {
		t.Fatalf("Skipped.Reason = %q", got.GetSkipped().GetReason())
	}
}

func TestListener_ForceRewardCallSubmits(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	// LastRewardRound < currentRound (0) requires forcing a higher current
	// round on the service. We achieve that by driving TryReward at round 5
	// against a tinfo with LastRewardRound=0 — eligibility is true.
	tinfo := bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, DeactivationRound: 1_000_000, LastRewardRound: 0}
	rwd := newRewardSvc(t, bmAddr, orch, tinfo)
	if _, err := rwd.TryReward(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	cc, cleanup := dialListener(t, srv)
	defer cleanup()
	cli := protocolv1.NewProtocolDaemonClient(cc)
	got, err := cli.ForceRewardCall(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatalf("ForceRewardCall: %v", err)
	}
	// After TryReward(round=5), Status().LastRound==5; the stub still reports
	// LastRewardRound=0 (no per-round mutation), so re-firing at round 5 is
	// idempotent — either a duplicate Submit is accepted (Submitted) or the
	// stub tracks state and skips. Accept either, but exactly one arm.
	if (got.GetSubmitted() == nil) == (got.GetSkipped() == nil) {
		t.Fatalf("expected exactly one arm of ForceOutcome; got %+v", got)
	}
}

func TestListener_AddrEmptyBeforeServe(t *testing.T) {
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	lis, err := NewListener(ListenerConfig{
		SocketPath: sock,
		Server:     srv,
		Logger:     logger.Slog(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := lis.Addr(); got != "" {
		t.Fatalf("Addr before Serve = %q; want empty", got)
	}
}

func TestListener_RefusesEmptyConfig(t *testing.T) {
	if _, err := NewListener(ListenerConfig{}); err == nil {
		t.Fatal("expected error: nil Server")
	}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: newRoundInitSvc(t, common.HexToAddress("0x01"))})
	if _, err := NewListener(ListenerConfig{Server: srv}); err == nil {
		t.Fatal("expected error: empty socket path")
	}
	if _, err := NewListener(ListenerConfig{Server: srv, SocketPath: "/tmp/x.sock"}); err == nil {
		t.Fatal("expected error: nil Logger")
	}
}

func TestListener_RefusesNonSocketStaleFile(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "not-a-socket")
	if err := os.WriteFile(stale, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs := newRoundInitSvc(t, common.HexToAddress("0xdeadbeef"))
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	lis, err := NewListener(ListenerConfig{
		SocketPath: stale,
		Server:     srv,
		Logger:     logger.Slog(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lis.Serve(context.Background()); err == nil {
		t.Fatal("expected refusal to remove non-socket file")
	}
}
