package configgen

import (
	"fmt"
	"sort"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"gopkg.in/yaml.v3"
)

type brokerConfig struct {
	Identity      config.Identity      `yaml:"identity"`
	Listen        config.Listen        `yaml:"listen,omitempty"`
	PaymentDaemon config.PaymentDaemon `yaml:"payment_daemon,omitempty"`
	ReceiptSink   config.ReceiptSink   `yaml:"receipt_sink,omitempty"`
	Capabilities  []brokerCapability   `yaml:"capabilities"`
}

type brokerCapability struct {
	ID              string          `yaml:"id"`
	OfferingID      string          `yaml:"offering_id"`
	InteractionMode string          `yaml:"interaction_mode"`
	WorkUnit        config.WorkUnit `yaml:"work_unit"`
	Health          config.Health   `yaml:"health,omitempty"`
	Price           config.Price    `yaml:"price"`
	Backend         brokerBackend   `yaml:"backend"`
	Extra           map[string]any  `yaml:"extra,omitempty"`
	Constraints     map[string]any  `yaml:"constraints,omitempty"`
}

type brokerBackend struct {
	ID        string            `yaml:"id,omitempty"`
	Transport string            `yaml:"transport"`
	URL       string            `yaml:"url,omitempty"`
	Auth      config.AuthConfig `yaml:"auth,omitempty"`
}

func GenerateYAML(cfg *config.Config) ([]byte, error) {
	model, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("marshal broker config: %w", err)
	}
	return out, nil
}

func Build(cfg *config.Config) (*brokerConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	capabilities := make([]brokerCapability, 0)
	for _, member := range cfg.Members {
		for _, backend := range member.Backends {
			for _, offering := range backend.Offerings {
				extra := cloneMap(offering.Extra)
				if extra == nil {
					extra = map[string]any{}
				}
				extra["pool"] = map[string]any{
					"member_eth_address":  member.EthAddress,
					"member_display_name": member.DisplayName,
					"member_backend_id":   backend.ID,
					"payout_mode":         member.PayoutMode,
				}

				capabilities = append(capabilities, brokerCapability{
					ID:              offering.CapabilityID,
					OfferingID:      offering.OfferingID,
					InteractionMode: offering.InteractionMode,
					WorkUnit:        offering.WorkUnit,
					Health:          offering.Health,
					Price:           offering.Price,
					Backend: brokerBackend{
						ID:        backend.ID,
						Transport: backend.Transport,
						URL:       backend.URL,
						Auth:      backend.Auth,
					},
					Extra:       extra,
					Constraints: cloneMap(offering.Constraints),
				})
			}
		}
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

	return &brokerConfig{
		Identity:      cfg.Identity,
		Listen:        cfg.Listen,
		PaymentDaemon: cfg.PaymentDaemon,
		ReceiptSink:   cfg.ReceiptSink,
		Capabilities:  capabilities,
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
