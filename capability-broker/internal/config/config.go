// Package config defines the broker's host-config.yaml grammar and provides
// loading + validation primitives. The grammar mirrors
// capability-broker/examples/host-config.example.yaml and is the operator's
// entire day-to-day surface.
package config

// Config is the top-level host-config.yaml schema.
type Config struct {
	Identity      Identity      `yaml:"identity"`
	Listen        Listen        `yaml:"listen,omitempty"`
	PaymentDaemon PaymentDaemon `yaml:"payment_daemon,omitempty"`
	Capabilities  []Capability  `yaml:"capabilities"`
}

// Identity carries the orch's chain identity. Must be present.
type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

// Listen declares the broker's bind addresses. If omitted, defaults are used.
type Listen struct {
	Paid    string `yaml:"paid,omitempty"`    // default ":8080"
	Metrics string `yaml:"metrics,omitempty"` // default ":9090"
}

// PaymentDaemon describes how to reach the co-located payment-daemon. v0.1
// uses a stub client when Mock is true.
type PaymentDaemon struct {
	Socket string `yaml:"socket,omitempty"`
	Mock   bool   `yaml:"mock,omitempty"`
}

// Capability is one entry in the host-config.yaml capabilities array.
type Capability struct {
	ID              string         `yaml:"id"`
	OfferingID      string         `yaml:"offering_id"`
	InteractionMode string         `yaml:"interaction_mode"`
	WorkUnit        WorkUnit       `yaml:"work_unit"`
	Price           Price          `yaml:"price"`
	Backend         Backend        `yaml:"backend"`
	Extra           map[string]any `yaml:"extra,omitempty"`
	Constraints     map[string]any `yaml:"constraints,omitempty"`
}

// WorkUnit declares the metering dimension and the recipe used to compute it.
// Extractor is a type-tagged map; the broker dispatches by Extractor["type"].
type WorkUnit struct {
	Name      string         `yaml:"name"`
	Extractor map[string]any `yaml:"extractor"`
}

// Price is wei-per-unit; AmountWei is a decimal string to preserve precision
// beyond JSON's safe-integer range (per manifest schema).
type Price struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

// Backend describes how the broker forwards a request to the upstream backend.
type Backend struct {
	Transport string     `yaml:"transport"`
	URL       string     `yaml:"url,omitempty"`
	Auth      AuthConfig `yaml:"auth,omitempty"`
}
