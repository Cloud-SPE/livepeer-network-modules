package aiserviceregistry

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewValidatesAndBindsAddress(t *testing.T) {
	if _, err := New(common.Address{}); err == nil {
		t.Fatal("expected missing address error")
	}

	addr := common.HexToAddress("0x000000000000000000000000000000000000FC02")
	b, err := New(addr)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Address(); got != addr {
		t.Fatalf("address = %s; want %s", got.Hex(), addr.Hex())
	}
	if _, err := b.PackSetServiceURI("https://ai.example.com"); err != nil {
		t.Fatalf("PackSetServiceURI: %v", err)
	}
}
