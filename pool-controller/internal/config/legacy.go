package config

// LegacyMember, LegacyBackend, and LegacyOffering define the migration-only
// nested member YAML shape from the earlier config-driven Pool model.
//
// M4 keeps them available only so bootstrap-time import and compatibility
// commands can continue to function while the persisted control-plane state
// becomes the sole runtime source of truth.
type LegacyMember struct {
	EthAddress  string          `yaml:"eth_address"`
	DisplayName string          `yaml:"display_name,omitempty"`
	PayoutMode  string          `yaml:"payout_mode,omitempty"`
	Backends    []LegacyBackend `yaml:"backends"`
}

type LegacyBackend struct {
	ID        string           `yaml:"id"`
	Transport string           `yaml:"transport"`
	URL       string           `yaml:"url,omitempty"`
	Auth      AuthConfig       `yaml:"auth,omitempty"`
	Offerings []LegacyOffering `yaml:"offerings"`
	Extra     map[string]any   `yaml:"extra,omitempty"`
}

type LegacyOffering struct {
	CapabilityID    string         `yaml:"capability_id"`
	OfferingID      string         `yaml:"offering_id"`
	InteractionMode string         `yaml:"interaction_mode"`
	WorkUnit        WorkUnit       `yaml:"work_unit"`
	Health          Health         `yaml:"health,omitempty"`
	Price           Price          `yaml:"price"`
	Extra           map[string]any `yaml:"extra,omitempty"`
	Constraints     map[string]any `yaml:"constraints,omitempty"`
}

// Temporary aliases keep the migration cut low-risk while the rest of the code
// moves away from the old names.
type Member = LegacyMember
type Backend = LegacyBackend
type Offering = LegacyOffering
