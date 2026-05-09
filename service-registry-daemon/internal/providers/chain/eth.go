package chain

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Eth is the production Chain implementation: eth_call against the
// ServiceRegistry contract via go-ethereum's ethclient.
//
// Reads are fully implemented (manual ABI encoding for getServiceURI).
type Eth struct {
	cli        *ethclient.Client
	registries []common.Address
}

// EthConfig captures the parameters NewEth needs.
type EthConfig struct {
	Client                   *ethclient.Client
	ServiceRegistryAddress   string // 0x-prefixed
	AIServiceRegistryAddress string // optional 0x-prefixed; when set, resolver lookups use this registry instead of the primary
}

// NewEth constructs an Eth chain provider.
func NewEth(cfg EthConfig) (*Eth, error) {
	if cfg.Client == nil {
		return nil, errors.New("chain.NewEth: Client is nil")
	}
	if cfg.AIServiceRegistryAddress != "" {
		if !common.IsHexAddress(cfg.AIServiceRegistryAddress) {
			return nil, fmt.Errorf("chain.NewEth: invalid AIServiceRegistryAddress %q", cfg.AIServiceRegistryAddress)
		}
		return &Eth{
			cli:        cfg.Client,
			registries: []common.Address{common.HexToAddress(cfg.AIServiceRegistryAddress)},
		}, nil
	}
	if !common.IsHexAddress(cfg.ServiceRegistryAddress) {
		return nil, fmt.Errorf("chain.NewEth: invalid ServiceRegistryAddress %q", cfg.ServiceRegistryAddress)
	}
	return &Eth{
		cli:        cfg.Client,
		registries: []common.Address{common.HexToAddress(cfg.ServiceRegistryAddress)},
	}, nil
}

// getServiceURISelector caches the 4-byte function selector for
// getServiceURI(address).
var getServiceURISelector = func() []byte {
	h := crypto.Keccak256([]byte("getServiceURI(address)"))
	return h[:4]
}()

// GetServiceURI implements Chain.GetServiceURI by issuing an eth_call
// to ServiceRegistry.getServiceURI(addr) and decoding the returned
// ABI-encoded string.
func (e *Eth) GetServiceURI(ctx context.Context, addr types.EthAddress) (string, error) {
	calldata := make([]byte, 4+32)
	copy(calldata[:4], getServiceURISelector)
	// left-pad the 20-byte address to 32 bytes
	addrBytes := common.HexToAddress(string(addr)).Bytes()
	copy(calldata[4+12:], addrBytes)

	var lastNotFound bool
	for _, registry := range e.registries {
		msg := ethereum.CallMsg{
			To:   &registry,
			Data: calldata,
		}
		out, err := e.cli.CallContract(ctx, msg, nil)
		if err != nil {
			return "", fmt.Errorf("%w: %w", types.ErrChainUnavailable, err)
		}
		if len(out) == 0 {
			lastNotFound = true
			continue
		}
		uri, err := decodeABIString(out)
		if err != nil {
			return "", fmt.Errorf("chain: decode getServiceURI return: %w", err)
		}
		if uri == "" {
			lastNotFound = true
			continue
		}
		return uri, nil
	}
	if lastNotFound {
		return "", types.ErrNotFound
	}
	return "", types.ErrNotFound
}

// decodeABIString decodes a single string returned by an eth_call.
// ABI layout: bytes[0:32] = offset (always 0x20 for a single string
// return), bytes[32:64] = length, bytes[64:64+length] = data, padded
// to 32-byte boundary.
func decodeABIString(b []byte) (string, error) {
	if len(b) < 64 {
		return "", fmt.Errorf("ABI string return too short: %d bytes", len(b))
	}
	// offset (we accept anything >= 0x20)
	offset := binary.BigEndian.Uint64(b[24:32])
	if offset > uint64(len(b)) {
		return "", fmt.Errorf("ABI string offset %d > data %d", offset, len(b))
	}
	if offset+32 > uint64(len(b)) {
		return "", fmt.Errorf("ABI string length out of bounds")
	}
	length := binary.BigEndian.Uint64(b[offset+24 : offset+32])
	start := offset + 32
	// Guard against integer overflow on hostile inputs.
	if length > uint64(len(b)) || start > uint64(len(b))-length {
		return "", fmt.Errorf("ABI string data out of bounds: start=%d length=%d total=%d", start, length, len(b))
	}
	return string(b[start : start+length]), nil
}
