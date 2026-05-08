package multi

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// smokeServer responds with method-specific results so every rpc.RPC
// wrapper method we test gets a sensible-shaped JSON-RPC response. This
// covers each method's go-ethereum decoding path.
func smokeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readBodyForTest(r)
		method := extractJSONRPCMethod(body)
		var result string
		switch method {
		case "eth_chainId":
			result = `"0xa4b1"`
		case "eth_blockNumber":
			result = `"0x1"`
		case "eth_gasPrice", "eth_maxPriorityFeePerGas":
			result = `"0x3b9aca00"` // 1 gwei
		case "eth_getTransactionCount":
			result = `"0x7"`
		case "eth_getBalance":
			result = `"0x10"`
		case "eth_getCode":
			result = `"0x6060"`
		case "eth_call":
			result = `"0x"`
		case "eth_sendRawTransaction":
			result = `"0x0000000000000000000000000000000000000000000000000000000000000001"`
		case "eth_getTransactionByHash", "eth_getTransactionReceipt", "eth_getBlockByHash":
			result = `null` // not-found is fine for smoke
		case "eth_getBlockByNumber":
			// Minimal valid header — some go-ethereum decoders reject null
			// here even though it's documented as a valid not-found signal.
			result = `{"parentHash":"0x0000000000000000000000000000000000000000000000000000000000000000","sha3Uncles":"0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347","miner":"0x0000000000000000000000000000000000000000","stateRoot":"0x0000000000000000000000000000000000000000000000000000000000000000","transactionsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","receiptsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","logsBloom":"0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","difficulty":"0x0","number":"0x1","gasLimit":"0x1c9c380","gasUsed":"0x0","timestamp":"0x0","extraData":"0x","mixHash":"0x0000000000000000000000000000000000000000000000000000000000000000","nonce":"0x0000000000000000","baseFeePerGas":"0x1","hash":"0xb6918fb1c3a2f8b6e1e0a0f2c7e6a5b4a3a2a1a0a09988776655443322110000"}`
		case "eth_getLogs":
			result = `[]`
		default:
			result = `null`
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, result)
	}))
}

func readBodyForTest(r *http.Request) string {
	b := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(b)
}

// TestSmoke_AllWrappersExercise exercises every rpc.RPC wrapper method
// against a fake RPC server. The test doesn't deeply assert results — it
// confirms the wrapper paths compile, proxy correctly, and decode without
// panics. Per-method semantics live in the tests above.
func TestSmoke_AllWrappersExercise(t *testing.T) {
	srv := smokeServer()
	defer srv.Close()

	policy := defaultPolicy()
	policy.CircuitBreakerThreshold = 100   // disable opening during smoke
	policy.MaxRetries = 0                   // each call is one attempt

	m, err := Open(Options{URLs: []string{srv.URL}, Policy: policy})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	addr := chain.Address{0xab}
	hash := chain.TxHash{0x01}

	// Reads
	if _, err := m.ChainID(ctx); err != nil {
		t.Errorf("ChainID: %v", err)
	}
	// HeaderByNumber proxies through; some go-ethereum versions are picky
	// about exact field shapes. Smoke just confirms no panic.
	_, _ = m.HeaderByNumber(ctx, big.NewInt(1))
	if _, err := m.SuggestGasPrice(ctx); err != nil {
		t.Errorf("SuggestGasPrice: %v", err)
	}
	if _, err := m.SuggestGasTipCap(ctx); err != nil {
		t.Errorf("SuggestGasTipCap: %v", err)
	}
	if _, err := m.PendingNonceAt(ctx, addr); err != nil {
		t.Errorf("PendingNonceAt: %v", err)
	}
	if _, err := m.BalanceAt(ctx, addr, nil); err != nil {
		t.Errorf("BalanceAt: %v", err)
	}
	if _, err := m.CodeAt(ctx, addr, nil); err != nil {
		t.Errorf("CodeAt: %v", err)
	}
	if _, err := m.CallContract(ctx, ethereum.CallMsg{To: &addr}, nil); err != nil {
		t.Errorf("CallContract: %v", err)
	}
	if _, err := m.PendingCallContract(ctx, ethereum.CallMsg{To: &addr}); err != nil {
		t.Errorf("PendingCallContract: %v", err)
	}
	if _, err := m.FilterLogs(ctx, ethereum.FilterQuery{}); err != nil {
		t.Errorf("FilterLogs: %v", err)
	}

	// Tx-related: errors are fine for smoke (server returns null), just
	// confirm no panic.
	_, _, _ = m.TransactionByHash(ctx, hash)
	_, _ = m.TransactionReceipt(ctx, hash)
	_, _ = m.BlockByNumber(ctx, big.NewInt(1))

	// SendTransaction needs a syntactically-valid signed tx; we skip rather
	// than mock signing here. The TxIntent processor tests cover this path
	// via FakeRPC.
	_ = (*types.Transaction)(nil)
}
