package config

import "strings"

type Config struct {
	// Placement is the pool's stacking policy (plan 0040 §4.4). Empty
	// means the built-in stances apply.
	Placement Placement `yaml:"placement,omitempty" json:"placement,omitempty"`
	// TemplateCatalogDir is the directory of curated workload
	// templates (plan 0044 §3.2). Empty means this pool ships no
	// catalog, which is a valid state for a controller that only does
	// accounting — it is not an error, and must not stop it booting.
	TemplateCatalogDir string        `yaml:"template_catalog_dir,omitempty" json:"template_catalog_dir,omitempty"`
	Identity           Identity      `yaml:"identity"`
	AdminAuth          AdminAuth     `yaml:"admin_auth,omitempty"`
	Listen             Listen        `yaml:"listen,omitempty"`
	Scoring            Scoring       `yaml:"scoring,omitempty"`
	PaymentDaemon      PaymentDaemon `yaml:"payment_daemon,omitempty"`
	ReceiptSink        ReceiptSink   `yaml:"receipt_sink,omitempty"`
	Bootstrap          Bootstrap     `yaml:"bootstrap,omitempty"`
}

type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

type Listen struct {
	Paid       string `yaml:"paid,omitempty"`
	Metrics    string `yaml:"metrics,omitempty"`
	WorkerQUIC string `yaml:"worker_quic,omitempty"`
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
	// BrokerAdminURL names a single broker. Brokers below supersedes it
	// for a pool that runs more than one; the single-broker keys stay
	// because a dev deployment is one broker and should not have to
	// learn a list to start.
	BrokerAdminURL       string     `yaml:"broker_admin_url,omitempty"`
	BrokerAdminAuth      AuthConfig `yaml:"broker_admin_auth,omitempty"`
	BrokerAdminTimeoutMS int        `yaml:"broker_admin_timeout_ms,omitempty"`
	// Brokers is the pool's broker fleet. Every enabled template is
	// pushed to each of them (plan 0044 §3.2).
	Brokers              []Broker `yaml:"brokers,omitempty"`
	PublicControllerURL  string   `yaml:"public_controller_url,omitempty"`
	PublicBrokerURL      string   `yaml:"public_broker_url,omitempty"`
	PublicBrokerQUICAddr string   `yaml:"public_broker_quic_addr,omitempty"`
}

// Broker is one push target. Name is for logs and status only; the URL
// is the identity.
type Broker struct {
	Name      string     `yaml:"name,omitempty" json:"name,omitempty"`
	AdminURL  string     `yaml:"admin_url" json:"admin_url"`
	Auth      AuthConfig `yaml:"auth,omitempty" json:"auth,omitempty"`
	TimeoutMS int        `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
}

// BrokerTargets is the fleet to push to, however it was configured. A
// pool that set neither gets an empty list and pushes nowhere, which is
// a valid standalone deployment rather than an error.
func (b Bootstrap) BrokerTargets() []Broker {
	if len(b.Brokers) > 0 {
		out := make([]Broker, 0, len(b.Brokers))
		for _, broker := range b.Brokers {
			if strings.TrimSpace(broker.AdminURL) == "" {
				continue
			}
			if strings.TrimSpace(broker.Name) == "" {
				broker.Name = broker.AdminURL
			}
			out = append(out, broker)
		}
		return out
	}
	if strings.TrimSpace(b.BrokerAdminURL) == "" {
		return nil
	}
	return []Broker{{
		Name:      b.BrokerAdminURL,
		AdminURL:  b.BrokerAdminURL,
		Auth:      b.BrokerAdminAuth,
		TimeoutMS: b.BrokerAdminTimeoutMS,
	}}
}

// Placement overrides how many templates a GPU class runs at once.
// Keys are pool GPU classes ("rtx-4090"); a class not named here keeps
// its built-in stance, and a class the pool has no stance for at all
// runs one template.
type Placement struct {
	MaxTemplatesPerClass map[string]int `yaml:"max_templates_per_class,omitempty" json:"max_templates_per_class,omitempty"`
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

func NormalizeWorkUnit(workUnit WorkUnit) WorkUnit {
	workUnit.Extractor = normalizeExtractorConfig(workUnit.Extractor)
	return workUnit
}

func normalizeExtractorConfig(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := cloneAnyMap(src)
	typ, _ := dst["type"].(string)
	if strings.TrimSpace(typ) == "request-formula" {
		if _, ok := dst["fields"]; !ok {
			dst["fields"] = map[string]any{}
		}
	}
	return dst
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneAnyValue(v)
	}
	return dst
}

func cloneAnySlice(src []any) []any {
	if src == nil {
		return nil
	}
	dst := make([]any, len(src))
	for i, v := range src {
		dst[i] = cloneAnyValue(v)
	}
	return dst
}

func cloneAnyValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		return cloneAnySlice(typed)
	default:
		return v
	}
}
