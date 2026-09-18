// Package publisher implements the publisher-mode service: building,
// signing, and (optionally) writing the on-chain pointer for a
// manifest. Pure logic with all I/O behind providers/.
package publisher

import (
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// Service is the publisher business-logic surface.
type Service struct {
	chain  chain.Chain
	signer signer.Signer
	audit  audit.Repo
	clock  clock.Clock
	log    logger.Logger
	rec    metrics.Recorder
}

// Config wires the service.
type Config struct {
	Chain    chain.Chain
	Signer   signer.Signer
	Audit    audit.Repo
	Clock    clock.Clock
	Logger   logger.Logger
	Recorder metrics.Recorder
}

// New constructs a publisher Service.
func New(c Config) *Service {
	if c.Clock == nil {
		c.Clock = clock.System{}
	}
	if c.Logger == nil {
		c.Logger = logger.Discard()
	}
	if c.Recorder == nil {
		c.Recorder = metrics.NewNoop()
	}
	return &Service{
		chain:  c.Chain,
		signer: c.Signer,
		audit:  c.Audit,
		clock:  c.Clock,
		log:    c.Logger,
		rec:    c.Recorder,
	}
}

// BuildSpec captures the minimal inputs to build a manifest. Fields
// the publisher fills in (schema_version, eth_address, issued_at,
// signature) are NOT in the spec.
type BuildSpec struct {
	EthAddress types.EthAddress
	Nodes      []types.Node
}

// Identity returns the loaded cold-key eth address.
func (s *Service) Identity() (types.EthAddress, error) {
	if s.signer == nil {
		return "", types.ErrKeystoreLocked
	}
	addr := s.signer.Address()
	if addr == "" {
		return "", types.ErrKeystoreLocked
	}
	return addr, nil
}
