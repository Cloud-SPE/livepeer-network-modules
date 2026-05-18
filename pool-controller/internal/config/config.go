package config

type Config struct {
	Identity        Identity        `yaml:"identity"`
	AdminAuth       AdminAuth       `yaml:"admin_auth,omitempty"`
	Listen          Listen          `yaml:"listen,omitempty"`
	SyntheticProbes SyntheticProbes `yaml:"synthetic_probes,omitempty"`
	Scoring         Scoring         `yaml:"scoring,omitempty"`
	PaymentDaemon   PaymentDaemon   `yaml:"payment_daemon,omitempty"`
	ReceiptSink     ReceiptSink     `yaml:"receipt_sink,omitempty"`
	Bootstrap       Bootstrap       `yaml:"bootstrap,omitempty"`
	Members         []Member        `yaml:"members,omitempty"`
}

type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

type Listen struct {
	Paid    string `yaml:"paid,omitempty"`
	Metrics string `yaml:"metrics,omitempty"`
}

type SyntheticProbes struct {
	Enabled    bool `yaml:"enabled,omitempty"`
	IntervalMS int  `yaml:"interval_ms,omitempty"`
	TimeoutMS  int  `yaml:"timeout_ms,omitempty"`
}

type Scoring struct {
	CooldownDurationMS        int     `yaml:"cooldown_duration_ms,omitempty" json:"cooldown_duration_ms,omitempty"`
	CooldownFailureTrigger    int     `yaml:"cooldown_failure_trigger,omitempty" json:"cooldown_failure_trigger,omitempty"`
	EMAHalfLifeMS             int     `yaml:"ema_half_life_ms,omitempty" json:"ema_half_life_ms,omitempty"`
	LatencyTargetMS           float64 `yaml:"latency_target_ms,omitempty" json:"latency_target_ms,omitempty"`
	RecentWindowStaleAfterMS  int     `yaml:"recent_window_stale_after_ms,omitempty" json:"recent_window_stale_after_ms,omitempty"`
	WindowScoreWeight         float64 `yaml:"window_score_weight,omitempty" json:"window_score_weight,omitempty"`
	EMAScoreWeight            float64 `yaml:"ema_score_weight,omitempty" json:"ema_score_weight,omitempty"`
	WarmupModifier            float64 `yaml:"warmup_modifier,omitempty" json:"warmup_modifier,omitempty"`
	WarmupExitSamples         int     `yaml:"warmup_exit_samples,omitempty" json:"warmup_exit_samples,omitempty"`
	TopDegradedLimit          int     `yaml:"top_degraded_limit,omitempty" json:"top_degraded_limit,omitempty"`
	TopExcludedLimit          int     `yaml:"top_excluded_limit,omitempty" json:"top_excluded_limit,omitempty"`
	WorstOfferingsLimit       int     `yaml:"worst_offerings_limit,omitempty" json:"worst_offerings_limit,omitempty"`
	PublicWorstOfferingsLimit int     `yaml:"public_worst_offerings_limit,omitempty" json:"public_worst_offerings_limit,omitempty"`
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

type Bootstrap struct {
	ImportLegacyConfigPath string     `yaml:"import_legacy_config_path,omitempty"`
	AutoImportLegacyConfig bool       `yaml:"auto_import_legacy_config,omitempty"`
	BrokerApplyCommand     []string   `yaml:"broker_apply_command,omitempty"`
	BrokerApplyTimeoutMS   int        `yaml:"broker_apply_timeout_ms,omitempty"`
	BrokerAdminURL         string     `yaml:"broker_admin_url,omitempty"`
	BrokerAdminAuth        AuthConfig `yaml:"broker_admin_auth,omitempty"`
	BrokerAdminTimeoutMS   int        `yaml:"broker_admin_timeout_ms,omitempty"`
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
	Name      string         `yaml:"name" json:"name"`
	Extractor map[string]any `yaml:"extractor" json:"extractor"`
}

type Price struct {
	AmountWei string `yaml:"amount_wei" json:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units" json:"per_units"`
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
