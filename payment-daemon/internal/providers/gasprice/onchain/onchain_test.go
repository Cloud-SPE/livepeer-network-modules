package onchain

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestGasPrice_AppliesMultiplier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method != "eth_gasPrice" {
			http.Error(w, "unstubbed: "+req.Method, 400)
			return
		}
		// 100 wei
		_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: "0x64", ID: req.ID})
	}))
	defer srv.Close()
	cl, _ := ethclient.Dial(srv.URL)
	defer cl.Close()

	g, err := New(context.Background(), Config{MultiplierPct: 200, RefreshInterval: time.Hour}, cl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := g.Current()
	if want := big.NewInt(200); got.Cmp(want) != 0 {
		t.Errorf("Current = %s; want %s (100 * 200 / 100)", got, want)
	}
}

func TestGasPrice_DefaultMultiplier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: "0x10", ID: req.ID})
	}))
	defer srv.Close()
	cl, _ := ethclient.Dial(srv.URL)
	defer cl.Close()
	g, err := New(context.Background(), Config{}, cl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 16 * 200 / 100 = 32
	if got, want := g.Current(), big.NewInt(32); got.Cmp(want) != 0 {
		t.Errorf("Current = %s; want %s", got, want)
	}
}
