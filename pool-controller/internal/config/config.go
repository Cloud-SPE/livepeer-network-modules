package config

type Config struct {
	Identity      Identity      `yaml:"identity"`
	AdminAuth     AdminAuth     `yaml:"admin_auth,omitempty"`
	Listen        Listen        `yaml:"listen,omitempty"`
	PaymentDaemon PaymentDaemon `yaml:"payment_daemon,omitempty"`
	ReceiptSink   ReceiptSink   `yaml:"receipt_sink,omitempty"`
	Members       []Member      `yaml:"members"`
}

type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

type Listen struct {
	Paid    string `yaml:"paid,omitempty"`
	Metrics string `yaml:"metrics,omitempty"`
}

type PaymentDaemon struct {
	Socket string `yaml:"socket,omitempty"`
	Mock   bool   `yaml:"mock,omitempty"`
}

type ReceiptSink struct {
	URL       string     `yaml:"url,omitempty"`
	Auth      AuthConfig `yaml:"auth,omitempty"`
	TimeoutMS int        `yaml:"timeout_ms,omitempty"`
}

type AdminAuth struct {
	BearerToken    string `yaml:"bearer_token,omitempty"`
	BearerTokenRef string `yaml:"bearer_token_ref,omitempty"`
}

type Member struct {
	EthAddress  string    `yaml:"eth_address"`
	DisplayName string    `yaml:"display_name,omitempty"`
	PayoutMode  string    `yaml:"payout_mode,omitempty"`
	Backends    []Backend `yaml:"backends"`
}

type Backend struct {
	ID        string         `yaml:"id"`
	Transport string         `yaml:"transport"`
	URL       string         `yaml:"url,omitempty"`
	Auth      AuthConfig     `yaml:"auth,omitempty"`
	Offerings []Offering     `yaml:"offerings"`
	Extra     map[string]any `yaml:"extra,omitempty"`
}

type Offering struct {
	CapabilityID    string         `yaml:"capability_id"`
	OfferingID      string         `yaml:"offering_id"`
	InteractionMode string         `yaml:"interaction_mode"`
	WorkUnit        WorkUnit       `yaml:"work_unit"`
	Health          Health         `yaml:"health,omitempty"`
	Price           Price          `yaml:"price"`
	Extra           map[string]any `yaml:"extra,omitempty"`
	Constraints     map[string]any `yaml:"constraints,omitempty"`
}

type WorkUnit struct {
	Name      string         `yaml:"name"`
	Extractor map[string]any `yaml:"extractor"`
}

type Price struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

type Health struct {
	InitialStatus string      `yaml:"initial_status,omitempty"`
	Drain         HealthDrain `yaml:"drain,omitempty"`
	Probe         HealthProbe `yaml:"probe,omitempty"`
}

type HealthDrain struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

type HealthProbe struct {
	Type           string         `yaml:"type,omitempty"`
	IntervalMS     int            `yaml:"interval_ms,omitempty"`
	TimeoutMS      int            `yaml:"timeout_ms,omitempty"`
	UnhealthyAfter int            `yaml:"unhealthy_after,omitempty"`
	HealthyAfter   int            `yaml:"healthy_after,omitempty"`
	Config         map[string]any `yaml:"config,omitempty"`
}

type AuthConfig struct {
	Method    string `yaml:"method,omitempty"`
	SecretRef string `yaml:"secret_ref,omitempty"`
}
