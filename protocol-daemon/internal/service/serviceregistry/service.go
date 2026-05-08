// Package serviceregistry submits ServiceRegistry writes through the
// shared txintent pipeline owned by protocol-daemon.
package serviceregistry

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
)

// Registry is the minimal ServiceRegistry binding surface this service uses.
type Registry interface {
	Address() chain.Address
	PackSetServiceURI(uri string) ([]byte, error)
}

// TxSubmitter is the subset of chain-commons.txintent.Manager used here.
type TxSubmitter interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
}

// Config wires the ServiceRegistry write service.
type Config struct {
	Registry Registry
	TxIntent TxSubmitter
	GasLimit uint64
}

// Service owns operator-triggered ServiceRegistry writes.
type Service struct {
	cfg Config
}

// New validates dependencies and constructs a Service.
func New(cfg Config) (*Service, error) {
	if cfg.Registry == nil {
		return nil, errors.New("serviceregistry: Registry is required")
	}
	if cfg.TxIntent == nil {
		return nil, errors.New("serviceregistry: TxIntent is required")
	}
	if cfg.GasLimit == 0 {
		return nil, errors.New("serviceregistry: GasLimit is required (>0)")
	}
	return &Service{cfg: cfg}, nil
}

// SetServiceURI submits a txintent-backed ServiceRegistry.setServiceURI call.
func (s *Service) SetServiceURI(ctx context.Context, uri string) (txintent.IntentID, error) {
	normalized, err := normalizeURI(uri)
	if err != nil {
		return txintent.IntentID{}, err
	}
	calldata, err := s.cfg.Registry.PackSetServiceURI(normalized)
	if err != nil {
		return txintent.IntentID{}, fmt.Errorf("PackSetServiceURI: %w", err)
	}
	return s.cfg.TxIntent.Submit(ctx, txintent.Params{
		Kind:      "SetServiceURI",
		KeyParams: serviceURIKey(s.cfg.Registry.Address(), normalized),
		To:        s.cfg.Registry.Address(),
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  s.cfg.GasLimit,
		Metadata: map[string]string{
			"service_uri": normalized,
		},
	})
}

func serviceURIKey(addr chain.Address, uri string) []byte {
	out := make([]byte, 0, len(addr)+len(uri))
	out = append(out, addr.Bytes()...)
	out = append(out, uri...)
	return out
}

func normalizeURI(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("service URI is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid service URI: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("service URI must use http or https")
	}
	if u.Host == "" {
		return "", errors.New("service URI host is required")
	}
	return u.String(), nil
}
