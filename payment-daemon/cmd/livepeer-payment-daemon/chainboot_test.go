package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	chaincfg "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

// fastPolicy makes chain-commons failover decisions immediate so a dead
// primary costs a test milliseconds, not the production backoff ladder.
var fastPolicy = chaincfg.RPCPolicy{
	MaxRetries:              0,
	InitialBackoff:          time.Millisecond,
	BackoffFactor:           1,
	MaxBackoff:              time.Millisecond,
	HealthProbeInterval:     time.Hour,
	CircuitBreakerThreshold: 1,
	CircuitBreakerCooloff:   time.Hour,
	CallTimeout:             5 * time.Second,
}

// jsonRPCStub is the smallest JSON-RPC server a chain-mode boot needs:
// eth_chainId, the Controller's getContract for every name chain-commons
// resolves, the three clock reads, the head header, and a balance.
type jsonRPCStub struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls map[string]int

	chainID    string
	controller ethcommon.Address
	contracts  map[string]ethcommon.Address // name -> address returned by getContract
}

func newJSONRPCStub(t *testing.T) *jsonRPCStub {
	t.Helper()
	s := &jsonRPCStub{
		calls:      map[string]int{},
		chainID:    "0xa4b1", // 42161
		controller: chain.ArbitrumOneController,
		contracts: map[string]ethcommon.Address{
			"TicketBroker":    ethcommon.HexToAddress("0x0000000000000000000000000000000000000a01"),
			"RoundsManager":   ethcommon.HexToAddress("0x0000000000000000000000000000000000000a02"),
			"BondingManager":  ethcommon.HexToAddress("0x0000000000000000000000000000000000000a03"),
			"Minter":          ethcommon.HexToAddress("0x0000000000000000000000000000000000000a04"),
			"ServiceRegistry": ethcommon.HexToAddress("0x0000000000000000000000000000000000000a05"),
			"LivepeerToken":   ethcommon.HexToAddress("0x0000000000000000000000000000000000000a06"),
		},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *jsonRPCStub) URL() string { return s.srv.URL }

func (s *jsonRPCStub) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

type stubReq struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     json.RawMessage   `json:"id"`
}

func word(v *big.Int) string {
	return "0x" + hex.EncodeToString(ethcommon.LeftPadBytes(v.Bytes(), 32))
}

func (s *jsonRPCStub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req stubReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	s.mu.Lock()
	s.calls[req.Method]++
	s.mu.Unlock()

	reply := func(result any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": msg}})
	}

	switch req.Method {
	case "eth_chainId":
		reply(s.chainID)
	case "eth_getBalance":
		reply("0x1")
	case "eth_getBlockByNumber":
		hdr := &ethtypes.Header{Number: big.NewInt(100), Difficulty: big.NewInt(0)}
		raw, _ := json.Marshal(hdr)
		reply(json.RawMessage(raw))
	case "eth_call":
		var call struct {
			To    string `json:"to"`
			Data  string `json:"data"`
			Input string `json:"input"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params[0], &call)
		}
		data := call.Input
		if data == "" {
			data = call.Data
		}
		raw, _ := hex.DecodeString(strings.TrimPrefix(data, "0x"))
		if len(raw) < 4 {
			fail("short calldata")
			return
		}
		to := ethcommon.HexToAddress(call.To)
		sel := hex.EncodeToString(raw[:4])
		switch {
		case to == s.controller && sel == hex.EncodeToString(crypto.Keccak256([]byte("getContract(bytes32)"))[:4]):
			if len(raw) < 36 {
				fail("short getContract")
				return
			}
			for name, addr := range s.contracts {
				if bytes.Equal(raw[4:36], crypto.Keccak256([]byte(name))) {
					reply(word(addr.Big()))
					return
				}
			}
			fail("unknown contract name")
		case to == s.contracts["RoundsManager"] && sel == hex.EncodeToString(crypto.Keccak256([]byte("lastInitializedRound()"))[:4]):
			reply(word(big.NewInt(4242)))
		case to == s.contracts["RoundsManager"] && sel == hex.EncodeToString(crypto.Keccak256([]byte("blockHashForRound(uint256)"))[:4]):
			reply("0x" + strings.Repeat("ab", 32))
		case to == s.contracts["BondingManager"] && sel == hex.EncodeToString(crypto.Keccak256([]byte("getTranscoderPoolSize()"))[:4]):
			reply(word(big.NewInt(10)))
		default:
			fail("unstubbed eth_call to " + call.To + " selector " + sel)
		}
	default:
		fail("unstubbed method " + req.Method)
	}
}

// syncBuffer is a bytes.Buffer safe for the daemon's goroutines to log to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func productionSenderCfg(t *testing.T, urls ...string) bootConfig {
	t.Helper()
	t.Setenv(passwordEnvVar, "boot-pw")
	tmp := t.TempDir()
	ksPath, _ := writeV3Keystore(t, tmp, "boot-pw")
	return bootConfig{
		mode:                 "sender",
		socketPath:           filepath.Join(tmp, "sender.sock"),
		dbPath:               filepath.Join(tmp, "sessions.db"),
		maxPaymentWei:        "1000",
		chainRPCURLs:         urls,
		keystorePath:         ksPath,
		controllerAddrHex:    chain.ArbitrumOneController.Hex(),
		expectedChainID:      chain.ArbitrumOneChainID,
		clockRefreshInterval: time.Hour,
		rpcPolicy:            &fastPolicy,
	}
}

// waitForSocket polls until the daemon has bound its unix socket, or
// the boot goroutine returned first.
func waitForSocket(t *testing.T, path string, errCh <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("boot returned before binding the socket: %v", err)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("socket never appeared")
}

// The list is one failover client, not a startup pick: with a dead
// primary the sender still boots against the backup, and the log says
// the chain id was verified over the list.
func TestRunSender_ProductionBootFailsOverToBackupRPC(t *testing.T) {
	stub := newJSONRPCStub(t)
	cfg := productionSenderCfg(t, "http://127.0.0.1:1/dead-primary", stub.URL())
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- runWithContext(ctx, logger, cfg) }()
	waitForSocket(t, cfg.socketPath, errCh)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run returned %v", err)
	}

	if stub.count("eth_chainId") < 1 {
		t.Error("backup endpoint never saw eth_chainId")
	}
	// Six Controller names + three clock reads on the initial sync.
	if got := stub.count("eth_call"); got < 9 {
		t.Errorf("eth_call count = %d; want at least 9", got)
	}
	if stub.count("eth_getBlockByNumber") < 1 {
		t.Error("clock never read the head header")
	}
	out := logs.String()
	if !strings.Contains(out, "chain id verified") {
		t.Errorf("log missing chain-id verification:\n%s", out)
	}
	if strings.Contains(out, "dead-primary") {
		t.Errorf("endpoint path leaked into the log (must be host only):\n%s", out)
	}
	if !strings.Contains(out, "url=127.0.0.1:1") {
		t.Errorf("failover to the backup should be logged with the dead host:\n%s", out)
	}
}

func TestRunSender_WrongChainIDIsConfigError(t *testing.T) {
	stub := newJSONRPCStub(t)
	stub.chainID = "0x1" // mainnet, not Arbitrum
	cfg := productionSenderCfg(t, stub.URL())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := runWithContext(context.Background(), logger, cfg)
	var cfgErr *configError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "chain id mismatch") {
		t.Fatalf("err = %v; want *configError with chain id mismatch", err)
	}
	if _, statErr := os.Stat(cfg.socketPath); !os.IsNotExist(statErr) {
		t.Errorf("socket must not bind on a config error: %v", statErr)
	}
}

func TestRunSender_AllEndpointsDeadIsConfigError(t *testing.T) {
	cfg := productionSenderCfg(t, "http://127.0.0.1:1/dead", "http://127.0.0.1:2/dead")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := runWithContext(context.Background(), logger, cfg)
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v; want *configError", err)
	}
}

func TestOpenChain_ZeroAddressFromControllerRejected(t *testing.T) {
	stub := newJSONRPCStub(t)
	stub.contracts["BondingManager"] = ethcommon.Address{}
	cfg := productionSenderCfg(t, stub.URL())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := openChain(context.Background(), logger, metrics.NewNoop(), cfg)
	if err == nil || !strings.Contains(err.Error(), "BondingManager is zero address") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenChain_OverrideSkipsControllerLookup(t *testing.T) {
	stub := newJSONRPCStub(t)
	delete(stub.contracts, "TicketBroker") // the Controller cannot answer for it
	override := ethcommon.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	cfg := productionSenderCfg(t, stub.URL())
	cfg.ticketBrokerAddrHex = override.Hex()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := openChain(context.Background(), logger, metrics.NewNoop(), cfg)
	if err != nil {
		t.Fatalf("openChain: %v", err)
	}
	defer deps.close()
	if deps.addrs.TicketBroker != override {
		t.Errorf("TicketBroker = %s; want override %s", deps.addrs.TicketBroker, override)
	}
	if deps.addrs.RoundsManager != stub.contracts["RoundsManager"] {
		t.Errorf("RoundsManager = %s; want Controller-resolved", deps.addrs.RoundsManager)
	}
}

func TestOpenChain_UnresolvableNameIsNotConfigError(t *testing.T) {
	stub := newJSONRPCStub(t)
	delete(stub.contracts, "Minter")
	cfg := productionSenderCfg(t, stub.URL())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := openChain(context.Background(), logger, metrics.NewNoop(), cfg)
	var cfgErr *configError
	if err == nil || errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "resolve contracts") {
		t.Fatalf("err = %v; want a plain resolve error", err)
	}
}

func TestOpenChain_BadURLIsConfigError(t *testing.T) {
	cfg := productionSenderCfg(t, "not a url")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := openChain(context.Background(), logger, metrics.NewNoop(), cfg)
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v; want *configError", err)
	}
}

func TestChainStatus_HostsOnly(t *testing.T) {
	if got := chainStatus(nil); got != "dev (fakes)" {
		t.Errorf("dev: %q", got)
	}
	got := chainStatus([]string{"https://user:pw@rpc.example.com/v2/SECRET", "https://b.example.com/KEY"})
	if strings.Contains(got, "SECRET") || strings.Contains(got, "KEY") || got != "production (rpc.example.com,b.example.com)" {
		t.Errorf("chainStatus = %q", got)
	}
}
