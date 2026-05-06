package ticketbroker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
)

// rpcReq is the minimal JSON-RPC request shape the test stub decodes.
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

// stubRPC mounts an httptest server that answers `eth_call` with a
// caller-supplied return-data hex string. Other methods 404. Returns
// the *ethclient.Client and a teardown.
func stubRPC(t *testing.T, returnHex string) (*ethclient.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if req.Method != "eth_call" {
			http.Error(w, "only eth_call supported in this stub: "+req.Method, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResp{
			JSONRPC: "2.0", Result: returnHex, ID: req.ID,
		})
	}))
	cl, err := ethclient.Dial(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("ethclient dial: %v", err)
	}
	return cl, func() { cl.Close(); srv.Close() }
}

// abiHex returns ParsedABI.Pack(method, args...) as hex (with 0x prefix).
func abiHex(t *testing.T, method string, args ...any) string {
	t.Helper()
	out, err := ParsedABI.Methods[method].Outputs.Pack(args...)
	if err != nil {
		t.Fatalf("pack %s: %v", method, err)
	}
	return "0x" + hex.EncodeToString(out)
}

func TestGetSenderInfo_DecodesTuples(t *testing.T) {
	deposit := big.NewInt(1_000_000_000_000_000_000)
	withdrawRound := big.NewInt(0)
	fundsRemaining := big.NewInt(500_000_000_000_000_000)
	claimedInRound := big.NewInt(0)

	// First call: getSenderInfo. Second: claimedReserve.
	// Use a per-call counter via a small switch via the call data.
	getSenderHex := abiHex(t, "getSenderInfo",
		struct {
			Deposit       *big.Int `abi:"deposit"`
			WithdrawRound *big.Int `abi:"withdrawRound"`
		}{Deposit: deposit, WithdrawRound: withdrawRound},
		struct {
			FundsRemaining        *big.Int `abi:"fundsRemaining"`
			ClaimedInCurrentRound *big.Int `abi:"claimedInCurrentRound"`
		}{FundsRemaining: fundsRemaining, ClaimedInCurrentRound: claimedInRound},
	)
	claimedReserveHex := abiHex(t, "claimedReserve", big.NewInt(42))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcReq
		_ = json.Unmarshal(body, &req)
		var call struct {
			To    string `json:"to"`
			Data  string `json:"data"`
			Input string `json:"input"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params[0], &call)
		}
		w.Header().Set("Content-Type", "application/json")
		dataField := call.Data
		if dataField == "" {
			dataField = call.Input
		}
		selector := ""
		if len(dataField) >= 10 {
			selector = dataField[:10]
		}
		var result string
		gsiSel := "0x" + hex.EncodeToString(ParsedABI.Methods["getSenderInfo"].ID)
		crSel := "0x" + hex.EncodeToString(ParsedABI.Methods["claimedReserve"].ID)
		switch strings.ToLower(selector) {
		case strings.ToLower(gsiSel):
			result = getSenderHex
		case strings.ToLower(crSel):
			result = claimedReserveHex
		default:
			http.Error(w, "unknown selector "+selector+" body="+string(body), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", Result: result, ID: req.ID})
	}))
	defer srv.Close()
	cl, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	b, err := New(Config{
		Address:  ethcommon.HexToAddress("0x1111111111111111111111111111111111111111"),
		Claimant: ethcommon.HexToAddress("0x2222222222222222222222222222222222222222"),
		ChainID:  big.NewInt(42161),
	}, cl, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	info, err := b.GetSenderInfo(context.Background(), ethcommon.HexToAddress("0x3333333333333333333333333333333333333333").Bytes())
	if err != nil {
		t.Fatalf("GetSenderInfo: %v", err)
	}
	if info.Deposit.Cmp(deposit) != 0 {
		t.Errorf("deposit = %s; want %s", info.Deposit, deposit)
	}
	if info.WithdrawRound != 0 {
		t.Errorf("withdrawRound = %d; want 0", info.WithdrawRound)
	}
	if info.Reserve == nil || info.Reserve.FundsRemaining.Cmp(fundsRemaining) != 0 {
		t.Errorf("FundsRemaining = %v; want %s", info.Reserve, fundsRemaining)
	}
	if got := info.Reserve.Claimed["0x2222222222222222222222222222222222222222"]; got == nil || got.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("Claimed[my-claimant] = %v; want 42", got)
	}
}

func TestIsUsedTicket_DecodesBool(t *testing.T) {
	trueHex := abiHex(t, "usedTickets", true)
	cl, teardown := stubRPC(t, trueHex)
	defer teardown()
	b, err := New(Config{
		Address: ethcommon.HexToAddress("0x1111111111111111111111111111111111111111"),
		ChainID: big.NewInt(42161),
	}, cl, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ticketHash := make([]byte, 32)
	ticketHash[0] = 0xde
	used, err := b.IsUsedTicket(context.Background(), ticketHash)
	if err != nil {
		t.Fatalf("IsUsedTicket: %v", err)
	}
	if !used {
		t.Error("expected used=true")
	}
}

func TestRedeem_RejectsWithoutSigner(t *testing.T) {
	cl, teardown := stubRPC(t, "0x")
	defer teardown()
	b, err := New(Config{
		Address: ethcommon.HexToAddress("0x1111111111111111111111111111111111111111"),
		ChainID: big.NewInt(42161),
	}, cl, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = b.RedeemWinningTicket(context.Background(), &providers.Ticket{
		Recipient:         make([]byte, 20),
		Sender:            make([]byte, 20),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: make([]byte, 32),
	}, make([]byte, 65), big.NewInt(1))
	if err == nil {
		t.Fatal("expected error: read-only broker")
	}
	if !strings.Contains(err.Error(), "TxSigner") {
		t.Errorf("err = %v; want mention of TxSigner", err)
	}
}

func TestToSolTicket_AuxDataLayout(t *testing.T) {
	tk := &providers.Ticket{
		Recipient:         make([]byte, 20),
		Sender:            make([]byte, 20),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(2),
		SenderNonce:       3,
		RecipientRandHash: make([]byte, 32),
		CreationRound:     12345,
		CreationRoundHash: make([]byte, 32),
	}
	for i := range tk.CreationRoundHash {
		tk.CreationRoundHash[i] = 0x70
	}
	st := toSolTicket(tk)
	if got, want := len(st.AuxData), 64; got != want {
		t.Errorf("AuxData length = %d; want %d", got, want)
	}
	// First 32 bytes: zero-padded round.
	if st.AuxData[31] != 0x39 || st.AuxData[30] != 0x30 { // 12345 == 0x3039
		t.Errorf("AuxData round-encoded bytes: %x", st.AuxData[:32])
	}
	// Last 32 bytes: the 0x70-filled hash.
	if st.AuxData[63] != 0x70 {
		t.Errorf("AuxData hash byte: %x", st.AuxData[32:])
	}
}

func TestToSolTicket_AuxDataEmpty(t *testing.T) {
	tk := &providers.Ticket{
		Recipient:         make([]byte, 20),
		Sender:            make([]byte, 20),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(2),
		SenderNonce:       3,
		RecipientRandHash: make([]byte, 32),
		CreationRound:     0,
		CreationRoundHash: make([]byte, 32),
	}
	st := toSolTicket(tk)
	if len(st.AuxData) != 0 {
		t.Errorf("AuxData should be empty when both fields zero; got %d bytes", len(st.AuxData))
	}
}
