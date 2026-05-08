// Package serviceregistry provides the minimal ServiceRegistry calldata
// builders protocol-daemon needs for operator-triggered writes.
package serviceregistry

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
)

var selectorSetServiceURI = crypto.Keccak256([]byte("setServiceURI(string)"))[:4]
var selectorGetServiceURI = crypto.Keccak256([]byte("getServiceURI(address)"))[:4]

// Bindings is the protocol-daemon-facing ServiceRegistry write surface.
type Bindings struct {
	addr chain.Address
	rpc  rpc.RPC
}

// New validates the configured ServiceRegistry contract address.
func New(addr chain.Address, rpcs ...rpc.RPC) (*Bindings, error) {
	if addr == (chain.Address{}) {
		return nil, errors.New("serviceregistry: address is required")
	}
	var client rpc.RPC
	if len(rpcs) > 0 {
		client = rpcs[0]
	}
	return &Bindings{addr: addr, rpc: client}, nil
}

// Address returns the bound contract address.
func (b *Bindings) Address() chain.Address { return b.addr }

// PackSetServiceURI ABI-encodes ServiceRegistry.setServiceURI(string).
func (b *Bindings) PackSetServiceURI(uri string) ([]byte, error) {
	if uri == "" {
		return nil, errors.New("serviceregistry: uri is required")
	}

	uriBytes := []byte(uri)
	paddedLen := ((len(uriBytes) + 31) / 32) * 32
	out := make([]byte, 4+32+32+paddedLen)
	copy(out[:4], selectorSetServiceURI)

	// Single dynamic arg: head slot points 32 bytes forward to the tail.
	binary.BigEndian.PutUint64(out[4+24:4+32], 32)
	binary.BigEndian.PutUint64(out[4+32+24:4+64], uint64(len(uriBytes)))
	copy(out[4+64:], uriBytes)

	return out, nil
}

// GetServiceURI reads ServiceRegistry.getServiceURI(orch) through the
// bound RPC client. Returns ("", nil) when no URI is set on chain.
func (b *Bindings) GetServiceURI(ctx context.Context, orch chain.Address) (string, error) {
	if b.rpc == nil {
		return "", errors.New("serviceregistry: rpc client is required for reads")
	}
	calldata := make([]byte, 4+32)
	copy(calldata[:4], selectorGetServiceURI)
	copy(calldata[4+12:], orch.Bytes())
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &b.addr,
		Data: calldata,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("serviceregistry: getServiceURI call: %w", err)
	}
	if len(out) == 0 {
		return "", nil
	}
	uri, err := decodeABIString(out)
	if err != nil {
		return "", fmt.Errorf("serviceregistry: decode getServiceURI return: %w", err)
	}
	return uri, nil
}

func decodeABIString(b []byte) (string, error) {
	if len(b) < 64 {
		return "", fmt.Errorf("abi string return too short: %d bytes", len(b))
	}
	offset := binary.BigEndian.Uint64(b[24:32])
	if offset > uint64(len(b)) {
		return "", fmt.Errorf("abi string offset %d > data %d", offset, len(b))
	}
	if offset+32 > uint64(len(b)) {
		return "", errors.New("abi string length out of bounds")
	}
	length := binary.BigEndian.Uint64(b[offset+24 : offset+32])
	start := offset + 32
	if length > uint64(len(b)) || start > uint64(len(b))-length {
		return "", fmt.Errorf("abi string data out of bounds: start=%d length=%d total=%d", start, length, len(b))
	}
	return string(b[start : start+length]), nil
}

var _ = (*big.Int)(nil)
