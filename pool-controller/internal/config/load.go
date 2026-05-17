package config

import (
	"fmt"
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
	for i := range cfg.Members {
		if cfg.Members[i].PayoutMode == "" {
			cfg.Members[i].PayoutMode = "onchain"
		}
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
	if len(cfg.Members) == 0 {
		return fmt.Errorf("members must contain at least one member")
	}

	seenMembers := map[string]struct{}{}
	for memberIndex, member := range cfg.Members {
		if member.EthAddress == "" {
			return fmt.Errorf("members[%d].eth_address is required", memberIndex)
		}
		switch member.PayoutMode {
		case "", "onchain", "manual":
		default:
			return fmt.Errorf("members[%d].payout_mode must be one of onchain|manual", memberIndex)
		}
		if _, ok := seenMembers[member.EthAddress]; ok {
			return fmt.Errorf("duplicate member eth_address %q", member.EthAddress)
		}
		seenMembers[member.EthAddress] = struct{}{}
		if len(member.Backends) == 0 {
			return fmt.Errorf("members[%d].backends must contain at least one backend", memberIndex)
		}

		seenBackendIDs := map[string]struct{}{}
		for backendIndex, backend := range member.Backends {
			if backend.ID == "" {
				return fmt.Errorf("members[%d].backends[%d].id is required", memberIndex, backendIndex)
			}
			if _, ok := seenBackendIDs[backend.ID]; ok {
				return fmt.Errorf("duplicate backend id %q for member %q", backend.ID, member.EthAddress)
			}
			seenBackendIDs[backend.ID] = struct{}{}
			if backend.Transport == "" {
				return fmt.Errorf("members[%d].backends[%d].transport is required", memberIndex, backendIndex)
			}
			if backend.URL == "" && backend.Transport == "http" {
				return fmt.Errorf("members[%d].backends[%d].url is required for http transport", memberIndex, backendIndex)
			}
			switch backend.Auth.Method {
			case "", "none":
			case "bearer":
				if backend.Auth.SecretRef == "" {
					return fmt.Errorf("members[%d].backends[%d].auth.secret_ref is required when auth.method=bearer", memberIndex, backendIndex)
				}
			default:
				return fmt.Errorf("members[%d].backends[%d].auth.method %q is not supported", memberIndex, backendIndex, backend.Auth.Method)
			}
			if len(backend.Offerings) == 0 {
				return fmt.Errorf("members[%d].backends[%d].offerings must contain at least one offering", memberIndex, backendIndex)
			}

			for offeringIndex, offering := range backend.Offerings {
				if offering.CapabilityID == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].capability_id is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.OfferingID == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].offering_id is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.InteractionMode == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].interaction_mode is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.WorkUnit.Name == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].work_unit.name is required", memberIndex, backendIndex, offeringIndex)
				}
				if len(offering.WorkUnit.Extractor) == 0 {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].work_unit.extractor is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.Price.AmountWei == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].price.amount_wei is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.Price.PerUnits == 0 {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].price.per_units must be > 0", memberIndex, backendIndex, offeringIndex)
				}

			}
		}
	}

	return nil
}
