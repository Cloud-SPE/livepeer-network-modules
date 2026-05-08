// Package publisher implements the publisher-mode service: building,
// signing, and (optionally) writing the on-chain pointer for a
// manifest. Pure logic with all I/O behind providers/.
package publisher

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
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

// BuildManifest constructs an unsigned manifest from the spec. Idempotent.
func (s *Service) BuildManifest(spec BuildSpec) (*types.Manifest, error) {
	s.rec.IncPublisherBuild()
	addr, err := s.Identity()
	if err != nil {
		return nil, err
	}
	proposed, err := validateEthAddressField(spec.EthAddress, addr, "proposed_eth_address")
	if err != nil {
		return nil, err
	}
	if len(spec.Nodes) == 0 {
		return nil, types.NewValidation(types.ErrEmptyNodes, "proposed_nodes", "publisher needs at least one node")
	}
	m := &types.Manifest{
		SchemaVersion: types.SchemaVersion,
		EthAddress:    string(proposed),
		IssuedAt:      s.clock.Now(),
		Nodes:         append([]types.Node(nil), spec.Nodes...),
		Signature:     types.Signature{Alg: types.SignatureAlgEthPersonal},
	}
	return m, nil
}

// BuildAndSign is the operator-facing one-shot path: builds the
// manifest from the spec, then signs it with the loaded keystore.
// Equivalent to BuildManifest followed by SignManifest, but keeps
// the operator from ever observing the unsigned interim state. Used
// by the livepeer-registry-refresh CLI driving manifest regeneration
// from the secure orch.
//
// Byte-identical canonical output to the two-call path: the same
// CanonicalBytes function is invoked under the hood by SignManifest.
func (s *Service) BuildAndSign(spec BuildSpec) (*types.Manifest, error) {
	m, err := s.BuildManifest(spec)
	if err != nil {
		return nil, err
	}
	return s.SignManifest(m)
}

// SignManifest computes canonical bytes, signs them, and returns the
// signed manifest. Idempotent for the same input bytes (deterministic
// signing per go-ethereum's RFC6979 nonce convention).
func (s *Service) SignManifest(m *types.Manifest) (*types.Manifest, error) {
	if m == nil {
		s.rec.IncPublisherSign(metrics.OutcomeParseError)
		return nil, fmt.Errorf("publisher: nil manifest")
	}
	addr, err := s.Identity()
	if err != nil {
		s.rec.IncPublisherSign(metrics.OutcomeKeystoreLocked)
		return nil, err
	}
	if _, err := validateEthAddressField(types.EthAddress(m.EthAddress), addr, "eth_address"); err != nil {
		s.rec.IncPublisherSign(metrics.OutcomeParseError)
		return nil, err
	}
	out := m.Clone()
	out.SchemaVersion = types.SchemaVersion
	out.IssuedAt = s.clock.Now()
	out.Signature = types.Signature{Alg: types.SignatureAlgEthPersonal}
	canonical, err := types.CanonicalBytes(out)
	if err != nil {
		s.rec.IncPublisherSign(metrics.OutcomeParseError)
		return nil, fmt.Errorf("publisher: canonical: %w", err)
	}
	sig, err := s.signer.SignCanonical(canonical)
	if err != nil {
		s.rec.IncPublisherSign(metrics.OutcomeKeystoreLocked)
		return nil, fmt.Errorf("publisher: sign: %w", err)
	}
	out.Signature = types.Signature{
		Alg:                        types.SignatureAlgEthPersonal,
		Value:                      "0x" + hex(sig),
		SignedCanonicalBytesSHA256: types.CanonicalSHA256(canonical),
	}
	if s.audit != nil {
		_ = s.audit.Append(types.AuditEvent{
			At: s.clock.Now(), EthAddress: s.signer.Address(),
			Kind: types.AuditPublishWritten, Detail: fmt.Sprintf("nodes=%d", len(out.Nodes)),
		})
	}
	s.rec.IncPublisherSign(metrics.OutcomeOK)
	return out, nil
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
