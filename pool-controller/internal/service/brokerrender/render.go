package brokerrender

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"gopkg.in/yaml.v3"
)

type BootstrapBrokerSettings struct {
	Identity      config.Identity
	Listen        config.Listen
	PaymentDaemon config.PaymentDaemon
	ReceiptSink   config.ReceiptSink
}

type RenderInput struct {
	Bootstrap   BootstrapBrokerSettings
	Offers      []types.Offer
	Members     []types.MemberRecord
	Backends    []types.MemberBackend
	Assignments []types.Assignment
}

type RenderResult struct {
	ConfigYAML []byte
	Revision   string
	Model      BrokerConfig
}

type BrokerConfig struct {
	Identity      config.Identity      `yaml:"identity"`
	Listen        config.Listen        `yaml:"listen,omitempty"`
	PaymentDaemon config.PaymentDaemon `yaml:"payment_daemon,omitempty"`
	ReceiptSink   config.ReceiptSink   `yaml:"receipt_sink,omitempty"`
	Capabilities  []BrokerCapability   `yaml:"capabilities"`
}

type BrokerCapability struct {
	ID              string          `yaml:"id"`
	OfferingID      string          `yaml:"offering_id"`
	InteractionMode string          `yaml:"interaction_mode"`
	WorkUnit        config.WorkUnit `yaml:"work_unit"`
	Health          config.Health   `yaml:"health,omitempty"`
	Price           config.Price    `yaml:"price"`
	Backend         BrokerBackend   `yaml:"backend"`
	Extra           map[string]any  `yaml:"extra,omitempty"`
	Constraints     map[string]any  `yaml:"constraints,omitempty"`
}

type BrokerBackend struct {
	ID        string            `yaml:"id,omitempty"`
	Transport string            `yaml:"transport"`
	URL       string            `yaml:"url,omitempty"`
	Auth      config.AuthConfig `yaml:"auth,omitempty"`
}

func Render(input RenderInput) (RenderResult, error) {
	offersByID := make(map[string]types.Offer, len(input.Offers))
	for _, offer := range input.Offers {
		offersByID[offer.ID] = offer
	}
	membersByID := make(map[string]types.MemberRecord, len(input.Members))
	for _, member := range input.Members {
		membersByID[member.ID] = member
	}
	backendsByID := make(map[string]types.MemberBackend, len(input.Backends))
	for _, backend := range input.Backends {
		backendsByID[backend.ID] = backend
	}

	capabilities := make([]BrokerCapability, 0)
	for _, assignment := range input.Assignments {
		if assignment.Status != types.AssignmentStatusActive {
			continue
		}
		offer, ok := offersByID[assignment.OfferID]
		if !ok || offer.Status != types.OfferStatusActive {
			continue
		}
		backend, ok := backendsByID[assignment.MemberBackendID]
		if !ok || backend.Status != types.BackendStatusActive {
			continue
		}
		member, ok := membersByID[backend.MemberID]
		if !ok || member.Status != types.MemberStatusActive {
			continue
		}
		extra := cloneMap(offer.Extra)
		if extra == nil {
			extra = map[string]any{}
		}
		extra["pool"] = map[string]any{
			"member_eth_address":  member.EthAddress,
			"member_display_name": member.DisplayName,
			"member_backend_id":   backend.ID,
			"payout_mode":         member.PayoutMode,
		}
		capabilities = append(capabilities, BrokerCapability{
			ID:              offer.CapabilityID,
			OfferingID:      offer.OfferingID,
			InteractionMode: offer.InteractionMode,
			WorkUnit:        offer.WorkUnit,
			Health:          config.Health{Probe: backend.HealthProbe},
			Price:           offer.Price,
			Backend: BrokerBackend{
				ID:        backend.ID,
				Transport: backend.Transport,
				URL:       backend.URL,
				Auth:      backend.Auth,
			},
			Extra:       extra,
			Constraints: cloneMap(offer.Constraints),
		})
	}

	sort.Slice(capabilities, func(i, j int) bool {
		left := capabilities[i]
		right := capabilities[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.OfferingID != right.OfferingID {
			return left.OfferingID < right.OfferingID
		}
		leftPool, _ := left.Extra["pool"].(map[string]any)
		rightPool, _ := right.Extra["pool"].(map[string]any)
		leftMember, _ := leftPool["member_eth_address"].(string)
		rightMember, _ := rightPool["member_eth_address"].(string)
		if leftMember != rightMember {
			return leftMember < rightMember
		}
		leftBackend, _ := leftPool["member_backend_id"].(string)
		rightBackend, _ := rightPool["member_backend_id"].(string)
		return leftBackend < rightBackend
	})

	model := BrokerConfig{
		Identity:      input.Bootstrap.Identity,
		Listen:        input.Bootstrap.Listen,
		PaymentDaemon: input.Bootstrap.PaymentDaemon,
		ReceiptSink:   input.Bootstrap.ReceiptSink,
		Capabilities:  capabilities,
	}
	raw, err := yaml.Marshal(model)
	if err != nil {
		return RenderResult{}, fmt.Errorf("marshal broker config: %w", err)
	}
	sum := sha256.Sum256(raw)
	return RenderResult{
		ConfigYAML: raw,
		Revision:   hex.EncodeToString(sum[:]),
		Model:      model,
	}, nil
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
