package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

func encodeAddr(a ethcommon.Address) string {
	out := make([]byte, 32)
	copy(out[12:], a[:])
	return "0x" + hex.EncodeToString(out)
}

func TestResolve_ResolvesAllNames(t *testing.T) {
	ticket := ethcommon.HexToAddress("0x0000000000000000000000000000000000000001")
	rounds := ethcommon.HexToAddress("0x0000000000000000000000000000000000000002")
	bonding := ethcommon.HexToAddress("0x0000000000000000000000000000000000000003")
	tickHash := crypto.Keccak256([]byte("TicketBroker"))
	roundsHash := crypto.Keccak256([]byte("RoundsManager"))
	bondingHash := crypto.Keccak256([]byte("BondingManager"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "eth_call" {
			var call struct {
				Data, Input string
			}
			_ = json.Unmarshal(req.Params[0], &call)
			data := call.Data
			if data == "" {
				data = call.Input
			}
			raw, _ := hex.DecodeString(strings.TrimPrefix(data, "0x"))
			if len(raw) < 36 {
				http.Error(w, "short", 400)
				return
			}
			arg := raw[4:36]
			var result string
			switch {
			case strings.EqualFold(hex.EncodeToString(arg), hex.EncodeToString(tickHash)):
				result = encodeAddr(ticket)
			case strings.EqualFold(hex.EncodeToString(arg), hex.EncodeToString(roundsHash)):
				result = encodeAddr(rounds)
			case strings.EqualFold(hex.EncodeToString(arg), hex.EncodeToString(bondingHash)):
				result = encodeAddr(bonding)
			default:
				http.Error(w, "unknown name", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: result, ID: req.ID})
			return
		}
		http.Error(w, "method not stubbed: "+req.Method, 400)
	}))
	defer srv.Close()
	cl, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	r := NewResolver(cl, ArbitrumOneController)
	addrs, err := r.Resolve(context.Background(), Overrides{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if addrs.TicketBroker != ticket {
		t.Errorf("ticket = %s; want %s", addrs.TicketBroker, ticket)
	}
	if addrs.RoundsManager != rounds {
		t.Errorf("rounds = %s; want %s", addrs.RoundsManager, rounds)
	}
	if addrs.BondingManager != bonding {
		t.Errorf("bonding = %s; want %s", addrs.BondingManager, bonding)
	}
}

func TestResolve_HonorsOverrides(t *testing.T) {
	override := ethcommon.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	rounds := ethcommon.HexToAddress("0x0000000000000000000000000000000000000002")
	bonding := ethcommon.HexToAddress("0x0000000000000000000000000000000000000003")
	roundsHash := crypto.Keccak256([]byte("RoundsManager"))
	bondingHash := crypto.Keccak256([]byte("BondingManager"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		var call struct{ Data, Input string }
		_ = json.Unmarshal(req.Params[0], &call)
		data := call.Data
		if data == "" {
			data = call.Input
		}
		raw, _ := hex.DecodeString(strings.TrimPrefix(data, "0x"))
		arg := raw[4:36]
		var result string
		if strings.EqualFold(hex.EncodeToString(arg), hex.EncodeToString(roundsHash)) {
			result = encodeAddr(rounds)
		} else if strings.EqualFold(hex.EncodeToString(arg), hex.EncodeToString(bondingHash)) {
			result = encodeAddr(bonding)
		} else {
			http.Error(w, "name not stubbed (override should have prevented call)", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: result, ID: req.ID})
	}))
	defer srv.Close()
	cl, _ := ethclient.Dial(srv.URL)
	defer cl.Close()
	r := NewResolver(cl, ArbitrumOneController)
	addrs, err := r.Resolve(context.Background(), Overrides{TicketBroker: override})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if addrs.TicketBroker != override {
		t.Errorf("override not honored; got %s", addrs.TicketBroker)
	}
}
