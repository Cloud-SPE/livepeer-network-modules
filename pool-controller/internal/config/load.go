package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Load(raw)
}

func Load(raw []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Listen.Paid == "" {
		cfg.Listen.Paid = ":8080"
	}
	if cfg.Listen.Metrics == "" {
		cfg.Listen.Metrics = ":9090"
	}
	if cfg.Scoring.CooldownDurationMS == 0 {
		cfg.Scoring.CooldownDurationMS = 300000
	}
	if cfg.Scoring.CooldownFailureTrigger == 0 {
		cfg.Scoring.CooldownFailureTrigger = 5
	}
	if cfg.Scoring.EMAHalfLifeMS == 0 {
		cfg.Scoring.EMAHalfLifeMS = 86400000
	}
	if cfg.Scoring.LatencyTargetMS == 0 {
		cfg.Scoring.LatencyTargetMS = 1200
	}
	if cfg.Scoring.RecentWindowStaleAfterMS == 0 {
		cfg.Scoring.RecentWindowStaleAfterMS = 300000
	}
	switch {
	case cfg.Scoring.WindowScoreWeight == 0 && cfg.Scoring.EMAScoreWeight > 0:
		cfg.Scoring.WindowScoreWeight = 1 - cfg.Scoring.EMAScoreWeight
	case cfg.Scoring.EMAScoreWeight == 0 && cfg.Scoring.WindowScoreWeight > 0:
		cfg.Scoring.EMAScoreWeight = 1 - cfg.Scoring.WindowScoreWeight
	case cfg.Scoring.WindowScoreWeight == 0 && cfg.Scoring.EMAScoreWeight == 0:
		cfg.Scoring.WindowScoreWeight = 0.7
		cfg.Scoring.EMAScoreWeight = 0.3
	}
	if cfg.Scoring.WarmupModifier == 0 {
		cfg.Scoring.WarmupModifier = 0.25
	}
	if cfg.Scoring.WarmupExitSamples == 0 {
		cfg.Scoring.WarmupExitSamples = 20
	}
	if cfg.Scoring.TopDegradedLimit == 0 {
		cfg.Scoring.TopDegradedLimit = 10
	}
	if cfg.Scoring.TopExcludedLimit == 0 {
		cfg.Scoring.TopExcludedLimit = 10
	}
	if cfg.Scoring.WorstOfferingsLimit == 0 {
		cfg.Scoring.WorstOfferingsLimit = 10
	}
	if cfg.Scoring.PublicWorstOfferingsLimit == 0 {
		cfg.Scoring.PublicWorstOfferingsLimit = 5
	}
	if cfg.Bootstrap.BrokerAdminTimeoutMS == 0 {
		cfg.Bootstrap.BrokerAdminTimeoutMS = 5000
	}
}

func validate(cfg *Config) error {
	if cfg.Identity.OrchEthAddress == "" {
		return fmt.Errorf("identity.orch_eth_address is required")
	}
	if cfg.AdminAuth.BearerToken != "" && cfg.AdminAuth.BearerTokenRef != "" {
		return fmt.Errorf("admin_auth.bearer_token and admin_auth.bearer_token_ref are mutually exclusive")
	}
	if cfg.AdminAuth.BearerTokenRef != "" && !strings.HasPrefix(cfg.AdminAuth.BearerTokenRef, "env://") {
		return fmt.Errorf("admin_auth.bearer_token_ref must use env://")
	}
	if cfg.Bootstrap.BrokerAdminURL != "" {
		u, err := url.Parse(cfg.Bootstrap.BrokerAdminURL)
		if err != nil {
			return fmt.Errorf("bootstrap.broker_admin_url is invalid: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("bootstrap.broker_admin_url scheme must be http or https (got %q)", u.Scheme)
		}
		switch cfg.Bootstrap.BrokerAdminAuth.Method {
		case "", "none":
		case "bearer":
			if cfg.Bootstrap.BrokerAdminAuth.SecretRef == "" {
				return fmt.Errorf("bootstrap.broker_admin_auth.secret_ref is required when method=bearer")
			}
			if !strings.Contains(cfg.Bootstrap.BrokerAdminAuth.SecretRef, "://") {
				return fmt.Errorf("bootstrap.broker_admin_auth.secret_ref should be a URI-style reference (got %q)", cfg.Bootstrap.BrokerAdminAuth.SecretRef)
			}
		default:
			return fmt.Errorf("bootstrap.broker_admin_auth.method %q is not supported", cfg.Bootstrap.BrokerAdminAuth.Method)
		}
	}
	// A pool that declared a fleet and got no usable target from it has
	// almost certainly typed a key wrong. Pushing nowhere is a valid
	// standalone configuration, but only when the operator asked for
	// nothing — reaching it by way of a misspelling would leave every
	// broker on a stale offer set with nothing said anywhere.
	if len(cfg.Bootstrap.Brokers) > 0 {
		for i, broker := range cfg.Bootstrap.Brokers {
			if strings.TrimSpace(broker.AdminURL) == "" {
				return fmt.Errorf("bootstrap.brokers[%d]: admin_url is required", i)
			}
			u, err := url.Parse(broker.AdminURL)
			if err != nil {
				return fmt.Errorf("bootstrap.brokers[%d].admin_url is invalid: %w", i, err)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("bootstrap.brokers[%d].admin_url scheme must be http or https (got %q)", i, u.Scheme)
			}
			switch broker.Auth.Method {
			case "", "none":
			case "bearer":
				if broker.Auth.SecretRef == "" {
					return fmt.Errorf("bootstrap.brokers[%d].auth.secret_ref is required when method=bearer", i)
				}
				if !strings.Contains(broker.Auth.SecretRef, "://") {
					return fmt.Errorf("bootstrap.brokers[%d].auth.secret_ref should be a URI-style reference (got %q)", i, broker.Auth.SecretRef)
				}
			default:
				return fmt.Errorf("bootstrap.brokers[%d].auth.method %q is not supported", i, broker.Auth.Method)
			}
			if broker.TimeoutMS < 0 {
				return fmt.Errorf("bootstrap.brokers[%d].timeout_ms must be >= 0", i)
			}
		}
	}
	if cfg.Bootstrap.BrokerAdminTimeoutMS < 0 {
		return fmt.Errorf("bootstrap.broker_admin_timeout_ms must be >= 0")
	}
	if cfg.Bootstrap.PublicControllerURL != "" {
		if err := validateHTTPURL("bootstrap.public_controller_url", cfg.Bootstrap.PublicControllerURL); err != nil {
			return err
		}
	}
	if cfg.Bootstrap.PublicBrokerURL != "" {
		if err := validateHTTPURL("bootstrap.public_broker_url", cfg.Bootstrap.PublicBrokerURL); err != nil {
			return err
		}
	}
	if cfg.Bootstrap.PublicBrokerQUICAddr != "" {
		if err := validateHostPort("bootstrap.public_broker_quic_addr", cfg.Bootstrap.PublicBrokerQUICAddr); err != nil {
			return err
		}
	}
	if cfg.Scoring.CooldownDurationMS < 0 {
		return fmt.Errorf("scoring.cooldown_duration_ms must be >= 0")
	}
	if cfg.Scoring.CooldownFailureTrigger < 0 {
		return fmt.Errorf("scoring.cooldown_failure_trigger must be >= 0")
	}
	if cfg.Scoring.EMAHalfLifeMS < 0 {
		return fmt.Errorf("scoring.ema_half_life_ms must be >= 0")
	}
	if cfg.Scoring.LatencyTargetMS < 0 {
		return fmt.Errorf("scoring.latency_target_ms must be >= 0")
	}
	if cfg.Scoring.RecentWindowStaleAfterMS < 0 {
		return fmt.Errorf("scoring.recent_window_stale_after_ms must be >= 0")
	}
	if cfg.Scoring.WindowScoreWeight < 0 || cfg.Scoring.WindowScoreWeight > 1 {
		return fmt.Errorf("scoring.window_score_weight must be between 0 and 1")
	}
	if cfg.Scoring.EMAScoreWeight < 0 || cfg.Scoring.EMAScoreWeight > 1 {
		return fmt.Errorf("scoring.ema_score_weight must be between 0 and 1")
	}
	if cfg.Scoring.WarmupModifier < 0 {
		return fmt.Errorf("scoring.warmup_modifier must be >= 0")
	}
	if cfg.Scoring.WarmupExitSamples < 0 {
		return fmt.Errorf("scoring.warmup_exit_samples must be >= 0")
	}
	if cfg.Scoring.TopDegradedLimit < 0 {
		return fmt.Errorf("scoring.top_degraded_limit must be >= 0")
	}
	if cfg.Scoring.TopExcludedLimit < 0 {
		return fmt.Errorf("scoring.top_excluded_limit must be >= 0")
	}
	if cfg.Scoring.WorstOfferingsLimit < 0 {
		return fmt.Errorf("scoring.worst_offerings_limit must be >= 0")
	}
	if cfg.Scoring.PublicWorstOfferingsLimit < 0 {
		return fmt.Errorf("scoring.public_worst_offerings_limit must be >= 0")
	}
	if cfg.Scoring.CooldownDurationMS > 0 && cfg.Scoring.CooldownFailureTrigger == 0 {
		return fmt.Errorf("scoring.cooldown_failure_trigger must be > 0 when cooldown_duration_ms is set")
	}
	if cfg.Scoring.WindowScoreWeight > 0 && cfg.Scoring.EMAScoreWeight > 0 {
		sum := cfg.Scoring.WindowScoreWeight + cfg.Scoring.EMAScoreWeight
		if sum < 0.999 || sum > 1.001 {
			return fmt.Errorf("scoring.window_score_weight + scoring.ema_score_weight must equal 1")
		}
	}
	if cfg.ReceiptSink.URL != "" {
		u, err := url.Parse(cfg.ReceiptSink.URL)
		if err != nil {
			return fmt.Errorf("receipt_sink.url is invalid: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("receipt_sink.url scheme must be http or https (got %q)", u.Scheme)
		}
		switch cfg.ReceiptSink.Auth.Method {
		case "", "none":
		case "bearer":
			if cfg.ReceiptSink.Auth.SecretRef == "" {
				return fmt.Errorf("receipt_sink.auth.secret_ref is required when method=bearer")
			}
			if !strings.Contains(cfg.ReceiptSink.Auth.SecretRef, "://") {
				return fmt.Errorf("receipt_sink.auth.secret_ref should be a URI-style reference (got %q)", cfg.ReceiptSink.Auth.SecretRef)
			}
		default:
			return fmt.Errorf("receipt_sink.auth.method %q is not supported", cfg.ReceiptSink.Auth.Method)
		}
		if cfg.ReceiptSink.TimeoutMS < 0 {
			return fmt.Errorf("receipt_sink.timeout_ms must be >= 0")
		}
	}
	return nil
}

func validateHTTPURL(field, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", field, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https (got %q)", field, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s host is required", field)
	}
	return nil
}

func validateHostPort(field, raw string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s must be host:port: %w", field, err)
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("%s must include host and port", field)
	}
	return nil
}
