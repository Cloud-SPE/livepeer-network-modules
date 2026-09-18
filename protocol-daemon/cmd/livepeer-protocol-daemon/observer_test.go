package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// Exercise the production provider path, including reopening its durable store,
// against an RPC that records every attempted signing/broadcast operation.
func TestProductionObserverProvidersRestartWithoutKeys(t *testing.T) {
	var writes atomic.Int32
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(req.Method, "send") || strings.Contains(req.Method, "sign") || req.Method == "eth_getBalance" {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0xa4b1"}`, req.ID)
		case "eth_call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x%064x"}`, req.ID, 1)
		case "eth_getCode":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x6000"}`, req.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"unavailable"}}`, req.ID)
		}
	}))
	defer rpc.Close()
	cfg := config.Default()
	cfg.Mode = types.ModeReadOnly
	cfg.Chain.EthURLs = []string{rpc.URL}
	cfg.Chain.ChainID = 42161
	cfg.Chain.ControllerAddr[19] = 1
	cfg.Chain.StorePath = filepath.Join(t.TempDir(), "observer.db")
	cfg.Chain.KeystorePath = "/nonexistent/key"
	cfg.Chain.RPC.MaxRetries = 0
	cfg.Chain.RPC.CallTimeout = time.Second
	cfg.SocketPath = filepath.Join(t.TempDir(), "observer.sock")
	for n := 0; n < 2; n++ {
		deps, cleanup, err := buildProviders(context.Background(), cfg, logger.Slog(io.Discard, nil))
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		if deps.Keystore != nil || deps.GasOracle != nil || deps.Receipts != nil {
			cleanup()
			t.Fatal("observer constructed signing dependencies")
		}
		manager, err := txintent.New(cfg.Chain.TxIntent, deps.Store, nil, nil, nil, nil)
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		id, err := manager.Submit(context.Background(), txintent.Params{Kind: "InitializeRound", KeyParams: []byte{1}, GasLimit: 100000})
		if err != nil {
			cleanup()
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		code := runObserver(ctx, cfg, deps, logger.Slog(io.Discard, nil))
		cancel()
		intent, err := manager.Status(context.Background(), id)
		cleanup()
		if code != 0 || err != nil || intent.Status != txintent.StatusPending || len(intent.Attempts) != 0 {
			t.Fatalf("restart %d: code=%d intent=%+v err=%v", n, code, intent, err)
		}

	}
	if writes.Load() != 0 {
		t.Fatalf("observer attempted %d financial RPCs", writes.Load())
	}
}
