package chain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestNewEth_BadClient(t *testing.T) {
	_, err := NewEth(EthConfig{Client: nil, ServiceRegistryAddress: "0x0000000000000000000000000000000000000000"})
	if err == nil {
		t.Fatal("expected error on nil client")
	}
}

func TestNewEth_BadAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cli, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Skip("skipping: ethclient.Dial unavailable")
	}
	defer cli.Close()
	if _, err := NewEth(EthConfig{Client: cli, ServiceRegistryAddress: "not-an-address"}); err == nil {
		t.Fatal("expected error on bad address")
	}
}

// TestEth_GetServiceURI_HappyPath exercises the JSON-RPC eth_call path.
// The httptest.Server hand-crafts an ABI-encoded string return.
func TestEth_GetServiceURI_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Minimal eth_call response: an ABI-encoded string "https://x".
		// offset(0x20) + length(9) + data padded to 32 bytes
		data := make([]byte, 96)
		data[31] = 0x20
		data[63] = 9
		copy(data[64:], "https://x")
		hex := "0x" + bytesToHex(data)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + hex + `"}`))
	}))
	defer srv.Close()
	cli, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Skip("ethclient.Dial unavailable")
	}
	defer cli.Close()

	e, err := NewEth(EthConfig{Client: cli, ServiceRegistryAddress: "0x0000000000000000000000000000000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	uri, err := e.GetServiceURI(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetServiceURI: %v", err)
	}
	if uri != "https://x" {
		t.Fatalf("got %q", uri)
	}
}

// TestEth_GetServiceURI_RPCError covers the chain-unavailable mapping.
func TestEth_GetServiceURI_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`))
	}))
	defer srv.Close()
	cli, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Skip("ethclient.Dial unavailable")
	}
	defer cli.Close()

	e, _ := NewEth(EthConfig{Client: cli, ServiceRegistryAddress: "0x0000000000000000000000000000000000000001"})
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	_, err = e.GetServiceURI(context.Background(), addr)
	if !errors.Is(err, types.ErrChainUnavailable) {
		t.Fatalf("expected ErrChainUnavailable, got %v", err)
	}
}

// TestEth_GetServiceURI_EmptyResult covers the not-found shape.
func TestEth_GetServiceURI_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x"}`))
	}))
	defer srv.Close()
	cli, err := ethclient.Dial(srv.URL)
	if err != nil {
		t.Skip("ethclient.Dial unavailable")
	}
	defer cli.Close()

	e, _ := NewEth(EthConfig{Client: cli, ServiceRegistryAddress: "0x0000000000000000000000000000000000000001"})
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	_, err = e.GetServiceURI(context.Background(), addr)
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDecodeABIString_OffsetOutOfBounds(t *testing.T) {
	b := make([]byte, 64)
	// offset = a large number
	for i := 24; i < 32; i++ {
		b[i] = 0xFF
	}
	if _, err := decodeABIString(b); err == nil {
		t.Fatal("expected error for offset out of bounds")
	}
}

func TestDecodeABIString_LengthOutOfBounds(t *testing.T) {
	b := make([]byte, 96)
	b[31] = 0x20
	// length = huge
	for i := 56; i < 64; i++ {
		b[i] = 0xFF
	}
	if _, err := decodeABIString(b); err == nil {
		t.Fatal("expected error for length out of bounds")
	}
}

// bytesToHex is a tiny helper for the test fixtures.
func bytesToHex(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&0x0f])
	}
	return sb.String()
}
