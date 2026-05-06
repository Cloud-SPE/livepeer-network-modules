package onchain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type rpcReq struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
	ID      int               `json:"id"`
}
type rpcResp struct {
	JSONRPC string `json:"jsonrpc"`
	Result  string `json:"result"`
	ID      int    `json:"id"`
}

func TestClock_InitialSync(t *testing.T) {
	rounds := ethcommon.HexToAddress("0x0000000000000000000000000000000000000010")
	bonding := ethcommon.HexToAddress("0x0000000000000000000000000000000000000020")

	roundResHex := func() string {
		out, _ := roundsABI.Methods["lastInitializedRound"].Outputs.Pack(big.NewInt(12345))
		return "0x" + hex.EncodeToString(out)
	}()
	hashResHex := func() string {
		var h [32]byte
		for i := range h {
			h[i] = 0xab
		}
		out, _ := roundsABI.Methods["blockHashForRound"].Outputs.Pack(h)
		return "0x" + hex.EncodeToString(out)
	}()
	poolResHex := func() string {
		out, _ := bondingABI.Methods["getTranscoderPoolSize"].Outputs.Pack(big.NewInt(100))
		return "0x" + hex.EncodeToString(out)
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_call":
			var call struct{ Data, Input string }
			_ = json.Unmarshal(req.Params[0], &call)
			data := call.Data
			if data == "" {
				data = call.Input
			}
			selector := ""
			if len(data) >= 10 {
				selector = strings.ToLower(data[:10])
			}
			lirSel := "0x" + hex.EncodeToString(roundsABI.Methods["lastInitializedRound"].ID)
			bhrSel := "0x" + hex.EncodeToString(roundsABI.Methods["blockHashForRound"].ID)
			poolSel := "0x" + hex.EncodeToString(bondingABI.Methods["getTranscoderPoolSize"].ID)
			var result string
			switch selector {
			case strings.ToLower(lirSel):
				result = roundResHex
			case strings.ToLower(bhrSel):
				result = hashResHex
			case strings.ToLower(poolSel):
				result = poolResHex
			default:
				http.Error(w, "unknown selector "+selector, 400)
				return
			}
			_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: result, ID: req.ID})
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: "0x1234", ID: req.ID})
		default:
			http.Error(w, "unstubbed method "+req.Method, 400)
		}
	}))
	defer srv.Close()
	cl, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	c, err := New(context.Background(), Config{
		RoundsManager:   rounds,
		BondingManager:  bonding,
		RefreshInterval: 30 * time.Second,
	}, cl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := c.LastInitializedRound(); got != 12345 {
		t.Errorf("round = %d; want 12345", got)
	}
	hash := c.LastInitializedL1BlockHash()
	if len(hash) != 32 || hash[0] != 0xab {
		t.Errorf("hash = %x", hash)
	}
	if got := c.LastSeenL1Block(); got.Cmp(big.NewInt(0x1234)) != 0 {
		t.Errorf("l1 block = %s; want 0x1234", got.String())
	}
	if got := c.GetTranscoderPoolSize(); got.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("poolSize = %s; want 100", got.String())
	}
}

func TestClock_BlockHashCache(t *testing.T) {
	var bhrCalls int32
	rounds := ethcommon.HexToAddress("0x0000000000000000000000000000000000000010")
	bonding := ethcommon.HexToAddress("0x0000000000000000000000000000000000000020")

	roundResHex := func() string {
		out, _ := roundsABI.Methods["lastInitializedRound"].Outputs.Pack(big.NewInt(7))
		return "0x" + hex.EncodeToString(out)
	}()
	hashResHex := func() string {
		var h [32]byte
		out, _ := roundsABI.Methods["blockHashForRound"].Outputs.Pack(h)
		return "0x" + hex.EncodeToString(out)
	}()
	poolResHex := func() string {
		out, _ := bondingABI.Methods["getTranscoderPoolSize"].Outputs.Pack(big.NewInt(1))
		return "0x" + hex.EncodeToString(out)
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_call":
			var call struct{ Data, Input string }
			_ = json.Unmarshal(req.Params[0], &call)
			data := call.Data
			if data == "" {
				data = call.Input
			}
			selector := strings.ToLower(data[:10])
			bhrSel := strings.ToLower("0x" + hex.EncodeToString(roundsABI.Methods["blockHashForRound"].ID))
			lirSel := strings.ToLower("0x" + hex.EncodeToString(roundsABI.Methods["lastInitializedRound"].ID))
			poolSel := strings.ToLower("0x" + hex.EncodeToString(bondingABI.Methods["getTranscoderPoolSize"].ID))
			switch selector {
			case lirSel:
				_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: roundResHex, ID: req.ID})
			case bhrSel:
				atomic.AddInt32(&bhrCalls, 1)
				_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: hashResHex, ID: req.ID})
			case poolSel:
				_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: poolResHex, ID: req.ID})
			default:
				http.Error(w, "unknown selector", 400)
			}
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: "0x1", ID: req.ID})
		}
	}))
	defer srv.Close()
	cl, _ := ethclient.Dial(srv.URL)
	defer cl.Close()
	c, err := New(context.Background(), Config{RoundsManager: rounds, BondingManager: bonding, RefreshInterval: time.Hour}, cl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := atomic.LoadInt32(&bhrCalls); got != 1 {
		t.Errorf("blockHashForRound calls after first sync = %d; want 1", got)
	}
	// Manually re-call refresh: same round, cache hit, no extra RPC.
	if err := c.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := atomic.LoadInt32(&bhrCalls); got != 1 {
		t.Errorf("blockHashForRound calls after second sync = %d; want still 1 (cached)", got)
	}
}
