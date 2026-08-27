// Package publisher implements the publisher-mode service: building,
// signing, and (optionally) writing the on-chain pointer for a
// manifest. Pure logic with all I/O behind providers/.
package publisher

import (
	"fmt"

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

func validateEthAddressField(proposed, loaded types.EthAddress, field string) (types.EthAddress, error) {
	addr, err := types.ParseEthAddress(proposed.String())
	if err != nil {
		return "", types.NewValidation(types.ErrInvalidEthAddress, field, err.Error())
	}
	if addr != loaded {
		return "", types.NewValidation(types.ErrInvalidEthAddress, field,
			fmt.Sprintf("does not match loaded publisher identity %s", loaded))
	}
	return addr, nil
}

// hex is a tiny zero-allocation hex encoder local to the package.
func hex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
