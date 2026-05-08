package chain

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestInMemory_PreLoadGetSet(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	c := NewInMemory(addr)
	c.PreLoad(other, "https://other.example.com")

	if uri, err := c.GetServiceURI(context.Background(), other); err != nil || uri != "https://other.example.com" {
		t.Fatalf("preload roundtrip: %q %v", uri, err)
	}
	if _, err := c.GetServiceURI(context.Background(), addr); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

}

func TestDecodeABIString_HappyPath(t *testing.T) {
	// ABI-encoded "hello":
	// offset = 0x20
	// length = 5
	// data   = "hello" + 27 bytes of zero padding
	b := make([]byte, 96)
	b[31] = 0x20
	b[63] = 5
	copy(b[64:], "hello")
	got, err := decodeABIString(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestDecodeABIString_TooShort(t *testing.T) {
	if _, err := decodeABIString(make([]byte, 32)); err == nil {
		t.Fatal("expected error for short input")
	}
}
