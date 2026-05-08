// Package orchstatus serves read-only on-chain orchestrator status queries.
package orchstatus

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
)

// RegistryReader reads ServiceRegistry state for an orchestrator.
type RegistryReader interface {
	GetServiceURI(ctx context.Context, orch chain.Address) (string, error)
}

// Config wires the dependencies for the orchstatus service.
type Config struct {
	Registry      RegistryReader
	RPC           rpc.RPC
	OrchAddress   chain.Address
	WalletAddress chain.Address
}

// Service provides read-only orchestrator status lookups.
type Service struct {
	cfg Config
}

// New validates and constructs a read-only orchstatus service.
func New(cfg Config) (*Service, error) {
	if cfg.Registry == nil {
		return nil, errors.New("orchstatus: Registry is required")
	}
	if cfg.RPC == nil {
		return nil, errors.New("orchstatus: RPC is required")
	}
	if cfg.OrchAddress == (chain.Address{}) {
		return nil, errors.New("orchstatus: OrchAddress is required")
	}
	if cfg.WalletAddress == (chain.Address{}) {
		return nil, errors.New("orchstatus: WalletAddress is required")
	}
	return &Service{cfg: cfg}, nil
}

// GetOnChainServiceURI returns the ServiceRegistry URI for the configured orchestrator.
func (s *Service) GetOnChainServiceURI(ctx context.Context) (string, error) {
	return s.cfg.Registry.GetServiceURI(ctx, s.cfg.OrchAddress)
}

// IsRegistered reports whether the configured orchestrator has a non-empty on-chain URI.
func (s *Service) IsRegistered(ctx context.Context) (bool, error) {
	uri, err := s.GetOnChainServiceURI(ctx)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(uri) != "", nil
}

// GetWalletBalance returns the configured wallet address and its current balance.
func (s *Service) GetWalletBalance(ctx context.Context) (chain.Address, *big.Int, error) {
	bal, err := s.cfg.RPC.BalanceAt(ctx, s.cfg.WalletAddress, nil)
	if err != nil {
		return chain.Address{}, nil, err
	}
	return s.cfg.WalletAddress, bal, nil
}
